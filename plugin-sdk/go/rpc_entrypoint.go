package pluginsdk

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

const (
	rpcProbePackageDigest  = "nre-ci-package"
	rpcProbeArtifactDigest = "nre-ci-artifact"
	rpcProbeGeneration     = "nre-ci-generation"
)

// RPCLifecycleFactory constructs the isolated lifecycle used by the canonical
// build-time handshake probe.
type RPCLifecycleFactory func(RPCHandshakeRequest) (RPCLifecycle, error)

// RPCRuntimeLifecycleFactory constructs the lifecycle used for the supervised
// runtime after argument and probe handling is complete.
type RPCRuntimeLifecycleFactory func() (RPCLifecycle, error)

// RPCServiceDeclaration selects standard private services for a runtime
// lifecycle. Provider IDs use the lifecycle itself as their HTTP handler;
// explicitly supplied handlers override that default.
type RPCServiceDeclaration struct {
	UI                              bool
	UIOptional                      bool
	HTTPBackendProviderIDs          []string
	HTTPBackendHandlers             map[string]http.Handler
	UnavailableHTTPBackendProviders map[string]string
}

// RPCEntrypointConfig declares the common process bootstrap shared by every
// rpc-service plugin. Run owns only plugin-specific runtime wiring; argument
// parsing and the canonical build-time handshake stay in the SDK.
type RPCEntrypointConfig struct {
	Declaration         RPCPluginDeclaration
	NewProbeLifecycle   RPCLifecycleFactory
	NewRuntimeLifecycle RPCRuntimeLifecycleFactory
	Services            RPCServiceDeclaration
	Run                 func(context.Context) error
}

// RunRPCEntrypoint runs the canonical build-time handshake probe or starts the
// plugin runtime when no arguments are supplied.
func RunRPCEntrypoint(ctx context.Context, args []string, output io.Writer, config RPCEntrypointConfig) error {
	if ctx == nil || output == nil {
		return errors.New("RPC entrypoint context and output are required")
	}
	if err := ValidateExecutionScopeEnvironment(); err != nil {
		return err
	}
	identity, probe, err := ResolveRPCHandshakeProbe(args, config.Declaration)
	if err != nil {
		return err
	}
	if probe {
		if config.NewProbeLifecycle == nil {
			return errors.New("RPC entrypoint probe lifecycle factory is required")
		}
		request := RPCHandshakeRequest{
			ABI:              RPCABIV1,
			PluginID:         identity.PluginID,
			PluginVersion:    identity.PluginVersion,
			PackageDigest:    rpcProbePackageDigest,
			ArtifactDigest:   rpcProbeArtifactDigest,
			GrantedScopes:    append([]string(nil), config.Declaration.RequiredCapabilities...),
			Generation:       rpcProbeGeneration,
			RequiredFeatures: append([]string(nil), config.Declaration.SupportedFeatures...),
		}
		lifecycle, err := config.NewProbeLifecycle(request)
		if err != nil {
			return err
		}
		response, err := lifecycle.Handshake(ctx, request)
		if err != nil {
			return err
		}
		if response.ABI != RPCABIV1 {
			return errors.New("canonical RPC handshake ABI mismatch")
		}
		if err := ValidateRPCFeatures(config.Declaration.SupportedFeatures, response.Features); err != nil {
			return fmt.Errorf("canonical RPC handshake features: %w", err)
		}
		_, err = fmt.Fprintln(output, response.ABI)
		return err
	}
	if len(args) != 0 {
		return fmt.Errorf("unexpected %s arguments: %v", config.Declaration.PluginID, args)
	}
	if config.NewRuntimeLifecycle != nil {
		lifecycle, err := config.NewRuntimeLifecycle()
		if err != nil {
			return err
		}
		services, err := pluginServices(lifecycle, config.Services)
		if err != nil {
			return err
		}
		return ServeRPCPluginServices(ctx, services)
	}
	if config.Run != nil {
		return config.Run(ctx)
	}
	return errors.New("RPC entrypoint runtime is required")
}

func pluginServices(lifecycle RPCLifecycle, declaration RPCServiceDeclaration) (RPCPluginServices, error) {
	services := RPCPluginServices{Lifecycle: lifecycle, UIOptional: declaration.UIOptional}
	handler, isHandler := lifecycle.(http.Handler)
	if declaration.UI {
		if !isHandler {
			return RPCPluginServices{}, errors.New("RPC plugin UI lifecycle must implement http.Handler")
		}
		services.UI = handler
	}
	if len(declaration.HTTPBackendProviderIDs) != 0 {
		if !isHandler {
			return RPCPluginServices{}, errors.New("RPC HTTP backend lifecycle must implement http.Handler")
		}
		services.HTTPBackendHandlers = make(map[string]http.Handler, len(declaration.HTTPBackendProviderIDs)+len(declaration.HTTPBackendHandlers))
		for _, providerID := range declaration.HTTPBackendProviderIDs {
			if strings.TrimSpace(providerID) == "" {
				return RPCPluginServices{}, errors.New("RPC HTTP backend provider id is required")
			}
			services.HTTPBackendHandlers[providerID] = handler
		}
	}
	if len(declaration.HTTPBackendHandlers) != 0 {
		if services.HTTPBackendHandlers == nil {
			services.HTTPBackendHandlers = make(map[string]http.Handler, len(declaration.HTTPBackendHandlers))
		}
		for providerID, providerHandler := range declaration.HTTPBackendHandlers {
			if strings.TrimSpace(providerID) == "" || providerHandler == nil {
				return RPCPluginServices{}, errors.New("RPC HTTP backend provider id and handler are required")
			}
			services.HTTPBackendHandlers[providerID] = providerHandler
		}
	}
	for providerID, message := range declaration.UnavailableHTTPBackendProviders {
		if strings.TrimSpace(providerID) == "" || strings.TrimSpace(message) == "" {
			return RPCPluginServices{}, errors.New("unavailable RPC HTTP backend provider id and message are required")
		}
		if services.HTTPBackendHandlers == nil {
			services.HTTPBackendHandlers = make(map[string]http.Handler, len(declaration.UnavailableHTTPBackendProviders))
		}
		unavailableMessage := message
		services.HTTPBackendHandlers[providerID] = http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			http.Error(writer, unavailableMessage, http.StatusServiceUnavailable)
		})
	}
	return services, nil
}

// RPCPluginServices declares the standard private servers owned by one RPC
// plugin process. UIOptional skips the UI server when the Host did not
// provision an endpoint, while all other declared services remain required.
type RPCPluginServices struct {
	Lifecycle           RPCLifecycle
	HTTPBackendHandlers map[string]http.Handler
	UI                  http.Handler
	UIOptional          bool
}

// ServeRPCPluginServices runs all declared private servers as one cancellation
// group. The first completed server cancels its siblings and every result is
// joined before returning.
func ServeRPCPluginServices(ctx context.Context, services RPCPluginServices) error {
	if ctx == nil {
		return errors.New("RPC plugin services context is required")
	}
	servers := make([]func(context.Context) error, 0, 3)
	if services.Lifecycle != nil {
		servers = append(servers, func(runCtx context.Context) error {
			return ServeRPCPlugin(runCtx, services.Lifecycle)
		})
	}
	if len(services.HTTPBackendHandlers) != 0 {
		servers = append(servers, func(runCtx context.Context) error {
			return ServeHTTPBackendProviders(runCtx, services.HTTPBackendHandlers)
		})
	}
	if services.UI != nil && (!services.UIOptional || strings.TrimSpace(os.Getenv(EnvPluginUIEndpoint)) != "") {
		servers = append(servers, func(runCtx context.Context) error {
			return ServePluginUI(runCtx, services.UI)
		})
	}
	return runRPCPluginServices(ctx, servers)
}

func runRPCPluginServices(ctx context.Context, servers []func(context.Context) error) error {
	if len(servers) == 0 {
		return errors.New("at least one RPC plugin service is required")
	}
	if len(servers) == 1 {
		return servers[0](ctx)
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan error, len(servers))
	for _, server := range servers {
		go func(server func(context.Context) error) {
			results <- server(runCtx)
		}(server)
	}
	errs := make([]error, 0, len(servers))
	errs = append(errs, <-results)
	cancel()
	for range len(servers) - 1 {
		errs = append(errs, <-results)
	}
	return errors.Join(errs...)
}
