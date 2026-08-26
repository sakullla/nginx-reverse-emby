package dockerproxy

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	pluginprocess "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/process"
)

const (
	RequiredGrant            = "service.revocable-resource-handle"
	RequiredResourceKind     = "docker-compose"
	RequiredResourceID       = "managed"
	EndpointEnv              = "NRE_PLUGIN_DOCKER_PROXY_ENDPOINT"
	CLIEnv                   = "NRE_PLUGIN_DOCKER_CLI"
	CookieFileEnv            = "NRE_PLUGIN_COOKIE_FILE"
	WorkDirEnv               = "NRE_APP_WORKDIR"
	guestCLIPath             = "/run/nre-plugin/docker"
	guestEndpointPath        = "/run/nre-plugin/docker-proxy.sock"
	proxyPath                = "/v1/exec"
	credentialHeader         = "X-NRE-Docker-Proxy-Credential"
	maxPayloadBytes          = 3 << 20
	maxOutputBytes           = 1 << 20
	workspaceTombstonePrefix = "-workspace-gc-"
)

type Request struct {
	Args    []string `json:"args"`
	AppID   string   `json:"app_id,omitempty"`
	Compose string   `json:"compose,omitempty"`
	Env     string   `json:"env,omitempty"`
}

type Response struct {
	Output   []byte `json:"output,omitempty"`
	ExitCode int    `json:"exit_code,omitempty"`
	Error    string `json:"error,omitempty"`
}

type Runner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "/usr/bin/docker", args...)
	command.Dir = dir
	return command.CombinedOutput()
}

type Config struct {
	EndpointDirectory string
	EndpointRoot      string
	Cookie            string
	SandboxUID        int
	WorkspaceRoot     string
	HelperExecutable  string
	Runner            Runner
}

func Eligible(grants []model.PluginGrantProjection) bool {
	for _, grant := range grants {
		if strings.TrimSpace(grant.Name) == RequiredGrant && strings.TrimSpace(grant.ResourceKind) == RequiredResourceKind && strings.TrimSpace(grant.ResourceID) == RequiredResourceID {
			return true
		}
	}
	return false
}

func WorkspaceDirectoryBinding(workspaceRoot string) pluginprocess.DirectoryBinding {
	path := filepath.Clean(strings.TrimSpace(workspaceRoot))
	return pluginprocess.DirectoryBinding{HostPath: path, GuestPath: path, ReadOnly: false}
}

func guestEnvironment(workspaceRoot string) []string {
	return []string{CLIEnv + "=" + guestCLIPath, EndpointEnv + "=unix:" + guestEndpointPath, WorkDirEnv + "=" + workspaceRoot}
}

func Start(config Config) ([]string, func() error, error) {
	if runtime.GOOS != "linux" {
		return nil, nil, errors.New("Docker plugin command proxy is available only on Linux")
	}
	endpointDirectory := filepath.Clean(strings.TrimSpace(config.EndpointDirectory))
	endpointRoot := filepath.Clean(strings.TrimSpace(config.EndpointRoot))
	workspaceRoot := filepath.Clean(strings.TrimSpace(config.WorkspaceRoot))
	if !filepath.IsAbs(endpointDirectory) || !filepath.IsAbs(endpointRoot) || !filepath.IsAbs(workspaceRoot) || endpointDirectory == string(filepath.Separator) || workspaceRoot == string(filepath.Separator) || strings.TrimSpace(config.Cookie) == "" {
		return nil, nil, errors.New("Docker plugin command proxy configuration is invalid")
	}
	if err := os.MkdirAll(workspaceRoot, 0o700); err != nil {
		return nil, nil, err
	}
	workspaceInfo, err := os.Lstat(workspaceRoot)
	if err != nil || !workspaceInfo.IsDir() || workspaceInfo.Mode()&os.ModeSymlink != 0 {
		return nil, nil, errors.New("Docker plugin workspace root must be a real directory")
	}
	if err := cleanupWorkspaceTombstones(workspaceRoot); err != nil {
		return nil, nil, err
	}
	if config.SandboxUID != 0 {
		if err := os.Chown(workspaceRoot, config.SandboxUID, config.SandboxUID); err != nil {
			return nil, nil, err
		}
	}
	helper := strings.TrimSpace(config.HelperExecutable)
	if helper == "" {
		var err error
		helper, err = os.Executable()
		if err != nil {
			return nil, nil, err
		}
	}
	helperTarget := filepath.Join(endpointDirectory, "docker")
	if err := copyExecutable(helper, helperTarget); err != nil {
		return nil, nil, err
	}

	listener, err := net.Listen("unix", filepath.Join(endpointRoot, "docker-proxy.sock"))
	if err != nil {
		_ = os.Remove(helperTarget)
		return nil, nil, err
	}
	socketPath := filepath.Join(endpointDirectory, "docker-proxy.sock")
	if err := os.Chmod(socketPath, 0o600); err != nil {
		_ = listener.Close()
		_ = os.Remove(helperTarget)
		return nil, nil, err
	}
	if config.SandboxUID != 0 {
		if err := os.Chown(socketPath, config.SandboxUID, config.SandboxUID); err != nil {
			_ = listener.Close()
			_ = os.Remove(helperTarget)
			return nil, nil, err
		}
	}
	runner := config.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	handler := &handler{cookie: config.Cookie, workspaceRoot: workspaceRoot, runner: runner}
	serverCtx, cancelServer := context.WithCancel(context.Background())
	server := &http.Server{ReadHeaderTimeout: 2 * time.Second, Handler: handler, BaseContext: func(net.Listener) context.Context { return serverCtx }}
	go func() { _ = server.Serve(listener) }()
	var once sync.Once
	var closeErr error
	closeProxy := func() error {
		once.Do(func() {
			cancelServer()
			serverErr := server.Close()
			listenerErr := listener.Close()
			helperErr := os.Remove(helperTarget)
			socketErr := os.Remove(socketPath)
			if errors.Is(serverErr, http.ErrServerClosed) || errors.Is(serverErr, net.ErrClosed) {
				serverErr = nil
			}
			if errors.Is(listenerErr, net.ErrClosed) {
				listenerErr = nil
			}
			if errors.Is(helperErr, os.ErrNotExist) {
				helperErr = nil
			}
			if errors.Is(socketErr, os.ErrNotExist) {
				socketErr = nil
			}
			closeErr = errors.Join(serverErr, listenerErr, helperErr, socketErr)
		})
		return closeErr
	}
	return guestEnvironment(workspaceRoot), closeProxy, nil
}

type handler struct {
	cookie        string
	workspaceRoot string
	runner        Runner
}

func (handler *handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Content-Type", "application/json")
	if request.Method != http.MethodPost || request.URL.Path != proxyPath {
		writeResponse(writer, http.StatusNotFound, Response{Error: "Docker command proxy route was not found"})
		return
	}
	if subtle.ConstantTimeCompare([]byte(request.Header.Get(credentialHeader)), []byte(handler.cookie)) != 1 {
		writeResponse(writer, http.StatusForbidden, Response{Error: "Docker command proxy credential was rejected"})
		return
	}
	payload, err := io.ReadAll(io.LimitReader(request.Body, maxPayloadBytes+1))
	if err != nil || len(payload) > maxPayloadBytes {
		writeResponse(writer, http.StatusRequestEntityTooLarge, Response{Error: "Docker command proxy request exceeds the limit"})
		return
	}
	var input Request
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || ensureJSONEOF(decoder) != nil {
		writeResponse(writer, http.StatusBadRequest, Response{Error: "Docker command proxy request is invalid"})
		return
	}
	if workspaceRemoveRequest(input.Args) {
		if err := handler.removeWorkspace(input); err != nil {
			writeResponse(writer, http.StatusBadRequest, Response{Error: err.Error()})
			return
		}
		writeResponse(writer, http.StatusOK, Response{})
		return
	}
	dir, err := handler.prepare(input)
	if err != nil {
		writeResponse(writer, http.StatusBadRequest, Response{Error: err.Error()})
		return
	}
	output, runErr := handler.runner.Run(request.Context(), dir, input.Args...)
	if len(output) > maxOutputBytes {
		output = output[:maxOutputBytes]
		if runErr == nil {
			runErr = errors.New("Docker command output exceeds the limit")
		}
	}
	result := Response{Output: output}
	if runErr != nil {
		result.ExitCode = 1
		result.Error = boundedError(runErr)
	}
	writeResponse(writer, http.StatusOK, result)
}

func (handler *handler) removeWorkspace(input Request) error {
	if err := validateArgs(input.Args); err != nil {
		return err
	}
	if !workspaceRemoveRequest(input.Args) || input.Compose != "" || input.Env != "" || !validIdentity(input.AppID) {
		return errors.New("Docker workspace removal request is invalid")
	}
	root, err := filepath.Abs(handler.workspaceRoot)
	if err != nil || root == string(filepath.Separator) {
		return errors.New("Docker workspace root is invalid")
	}
	rootInfo, err := os.Lstat(root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("Docker workspace root is not a managed directory")
	}
	target := filepath.Join(root, input.AppID)
	if filepath.Dir(target) != root {
		return errors.New("Docker compose workspace escapes the managed root")
	}
	targetInfo, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !targetInfo.IsDir() || targetInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("Docker compose workspace is not a managed directory")
	}
	tombstone, err := os.MkdirTemp(root, workspaceTombstonePrefix)
	if err != nil {
		return errors.New("Docker compose workspace cleanup could not be prepared")
	}
	if err := os.Remove(tombstone); err != nil {
		return errors.New("Docker compose workspace cleanup could not be prepared")
	}
	if err := os.Rename(target, tombstone); err != nil {
		return errors.New("Docker compose workspace cleanup could not take ownership")
	}
	if err := os.RemoveAll(tombstone); err != nil {
		restoreErr := os.Rename(tombstone, target)
		return errors.Join(errors.New("Docker compose workspace cleanup failed"), restoreErr)
	}
	return nil
}

func workspaceRemoveRequest(args []string) bool {
	return len(args) == 2 && args[0] == "workspace" && args[1] == "remove"
}

func cleanupWorkspaceTombstones(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), workspaceTombstonePrefix) {
			continue
		}
		path := filepath.Join(root, entry.Name())
		if filepath.Dir(path) != root {
			return errors.New("Docker workspace tombstone escapes the managed root")
		}
		if err := os.RemoveAll(path); err != nil {
			return errors.New("Docker workspace tombstone cleanup failed")
		}
	}
	return nil
}

func (handler *handler) prepare(input Request) (string, error) {
	if err := validateArgs(input.Args); err != nil {
		return "", err
	}
	if input.Args[0] != "compose" {
		if input.AppID != "" || input.Compose != "" || input.Env != "" {
			return "", errors.New("Docker non-compose command included workspace data")
		}
		return "", nil
	}
	if !validIdentity(input.AppID) {
		return "", errors.New("Docker compose app identity is invalid")
	}
	dir := filepath.Join(handler.workspaceRoot, input.AppID)
	if filepath.Dir(dir) != handler.workspaceRoot {
		return "", errors.New("Docker compose workspace escapes the managed root")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if input.Compose != "" {
		if len(input.Compose) > 1<<20 {
			return "", errors.New("Docker compose document exceeds the limit")
		}
		if err := writeComposeFile(dir, input.Compose); err != nil {
			return "", err
		}
	}
	if input.Env != "" {
		if len(input.Env) > 1<<20 || strings.ContainsRune(input.Env, '\x00') {
			return "", errors.New("Docker compose environment exceeds the limit")
		}
		if err := writeEnvironmentFile(dir, input.Env); err != nil {
			return "", err
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "compose.yaml")); err != nil {
		return "", errors.New("Docker compose workspace is not initialized")
	}
	return dir, nil
}

func validateArgs(args []string) error {
	if len(args) == 0 || len(args) > 8 {
		return errors.New("Docker command arguments are invalid")
	}
	for _, arg := range args {
		if len(arg) > 1024 || strings.ContainsAny(arg, "\x00\r\n") {
			return errors.New("Docker command argument is invalid")
		}
	}
	switch args[0] {
	case "version":
		if len(args) == 3 && args[1] == "--format" && args[2] == "{{.Server.Version}}" {
			return nil
		}
	case "compose":
		if len(args) == 2 {
			switch args[1] {
			case "start", "stop", "restart", "down", "pull", "ps":
				return nil
			}
		}
		if len(args) == 3 && (args[1] == "up" && args[2] == "-d" || args[1] == "rm" && args[2] == "-f") {
			return nil
		}
		if len(args) == 3 && args[1] == "logs" && args[2] == "--no-color" {
			return nil
		}
		if len(args) == 4 && args[1] == "logs" && args[2] == "--no-color" && validIdentity(args[3]) {
			return nil
		}
	case "workspace":
		if workspaceRemoveRequest(args) {
			return nil
		}
	case "image":
		if len(args) == 5 && args[1] == "inspect" && args[2] == "--format" && args[3] == "{{if .RepoDigests}}{{index .RepoDigests 0}}{{else}}{{.Id}}{{end}}" && validImage(args[4]) {
			return nil
		}
	case "buildx":
		if len(args) == 6 && args[1] == "imagetools" && args[2] == "inspect" && args[3] == "--format" && args[4] == "{{.Manifest.Digest}}" && validImage(args[5]) {
			return nil
		}
	case "manifest":
		if len(args) == 4 && args[1] == "inspect" && args[2] == "--verbose" && validImage(args[3]) {
			return nil
		}
	}
	return errors.New("Docker command is not allowed")
}

func validIdentity(value string) bool {
	if value == "" || len(value) > 128 || value != strings.TrimSpace(value) || value[0] == '-' {
		return false
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("._-", char) {
			continue
		}
		return false
	}
	return true
}

func validImage(value string) bool {
	if value == "" || len(value) > 512 || value != strings.TrimSpace(value) || value[0] == '-' || strings.ContainsAny(value, "\x00\r\n\t ") {
		return false
	}
	return true
}

func writeComposeFile(dir, compose string) error {
	temporary, err := os.CreateTemp(dir, ".compose-*.yaml")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := io.WriteString(temporary, compose); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, filepath.Join(dir, "compose.yaml"))
}

func writeEnvironmentFile(dir, value string) error {
	temporary, err := os.CreateTemp(dir, ".env-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := io.WriteString(temporary, value); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, filepath.Join(dir, ".env"))
}

func copyExecutable(source, target string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o555)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		_ = os.Remove(target)
		return err
	}
	if err := output.Sync(); err != nil {
		_ = output.Close()
		_ = os.Remove(target)
		return err
	}
	return output.Close()
}

func RunCLI(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	endpoint := strings.TrimSpace(os.Getenv(EndpointEnv))
	cookieFile := strings.TrimSpace(os.Getenv(CookieFileEnv))
	if !strings.HasPrefix(endpoint, "unix:") || cookieFile == "" {
		_, _ = io.WriteString(stderr, "docker proxy is unavailable\n")
		return 1
	}
	request := Request{Args: append([]string(nil), args...)}
	if len(args) > 0 && (args[0] == "compose" || args[0] == "workspace") {
		cwd, err := os.Getwd()
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		request.AppID = filepath.Base(cwd)
		if args[0] == "compose" {
			for _, name := range []string{"compose.yaml", "docker-compose.yml", "docker-compose.yaml"} {
				payload, readErr := os.ReadFile(filepath.Join(cwd, name))
				if readErr == nil {
					request.Compose = string(payload)
					break
				}
				if !errors.Is(readErr, os.ErrNotExist) {
					_, _ = fmt.Fprintln(stderr, readErr)
					return 1
				}
			}
			if environment, readErr := os.ReadFile(filepath.Join(cwd, ".env")); readErr == nil {
				request.Env = string(environment)
			} else if !errors.Is(readErr, os.ErrNotExist) {
				_, _ = fmt.Fprintln(stderr, readErr)
				return 1
			}
		}
	}
	payload, err := json.Marshal(request)
	if err != nil || len(payload) > maxPayloadBytes {
		_, _ = io.WriteString(stderr, "docker proxy request is invalid\n")
		return 1
	}
	cookie, err := os.ReadFile(cookieFile)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	transport := &http.Transport{DialContext: func(dialCtx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(dialCtx, "unix", strings.TrimPrefix(endpoint, "unix:"))
	}}
	client := &http.Client{Transport: transport}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://docker-proxy"+proxyPath, bytes.NewReader(payload))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set(credentialHeader, string(cookie))
	response, err := client.Do(httpRequest)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer response.Body.Close()
	var result Response
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxPayloadBytes))
	if err := decoder.Decode(&result); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if len(result.Output) > 0 {
		_, _ = stdout.Write(result.Output)
	}
	if result.Error != "" && len(result.Output) == 0 {
		_, _ = io.WriteString(stderr, result.Error+"\n")
	}
	if response.StatusCode != http.StatusOK || result.ExitCode != 0 || result.Error != "" {
		return 1
	}
	return 0
}

func writeResponse(writer http.ResponseWriter, status int, response Response) {
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(response)
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON data")
	}
	return nil
}

func boundedError(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	if len(text) > 512 {
		text = text[:512]
	}
	return text
}
