// Package channel implements the host-managed reverse channel data plane. An
// exit agent with no inbound reachability dials out to an entry agent over a
// persistent, multiplexed, mutually-authenticated TLS channel; the entry agent
// bridges loopback traffic from host-managed L4 rule backends back over the
// channel so the exit agent can deliver it to the session backend.
package channel

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
)

const (
	// RoleEntry terminates the outbound tunnel from the exit agent and bridges
	// accepted loopback traffic back over the established channel.
	RoleEntry = "entry"
	// RoleExit dials out to the entry agent and delivers channel streams to the
	// session backend.
	RoleExit = "exit"

	ProtocolTCP = "tcp"
	ProtocolUDP = "udp"

	StateOnline  = "online"
	StateOffline = "offline"
)

const (
	defaultBackoffBase    = 500 * time.Millisecond
	defaultBackoffLimit   = 30 * time.Second
	defaultConnectTimeout = 10 * time.Second
	defaultUDPIdleTimeout = 60 * time.Second
	maxSessionIDLength    = 190
)

// Config configures a Manager.
type Config struct {
	// AgentID is the identity of the agent owning this manager.
	AgentID string
	// Credentials is the execution plane's tunnel PKI facade. Both channel
	// peers authenticate with their agent-identity tunnel credentials.
	Credentials relay.TunnelCredentialProvider
	// BackoffBase and BackoffLimit bound the exit reconnect backoff.
	BackoffBase  time.Duration
	BackoffLimit time.Duration
	// ConnectTimeout bounds one dial and handshake attempt.
	ConnectTimeout time.Duration
	// UDPIdleTimeout retires silent UDP associations.
	UDPIdleTimeout time.Duration
}

func (cfg Config) withDefaults() Config {
	if cfg.BackoffBase <= 0 {
		cfg.BackoffBase = defaultBackoffBase
	}
	if cfg.BackoffLimit <= 0 {
		cfg.BackoffLimit = defaultBackoffLimit
	}
	if cfg.BackoffLimit < cfg.BackoffBase {
		cfg.BackoffLimit = cfg.BackoffBase
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = defaultConnectTimeout
	}
	if cfg.UDPIdleTimeout <= 0 {
		cfg.UDPIdleTimeout = defaultUDPIdleTimeout
	}
	return cfg
}

// SessionSpec is the desired state of one reverse channel session on this
// agent. Ingress and bridge ports are outputs on the entry role: zero asks the
// agent to allocate a free port and report it through SessionStatus.
type SessionSpec struct {
	SessionID    string
	Role         string
	Protocol     string
	EntryAgentID string
	ExitAgentID  string

	// ListenHost/ListenPort are the entry role tunnel ingress binding.
	ListenHost string
	ListenPort int
	// BridgeHost/BridgePort are the entry role loopback bridge binding an L4
	// rule backend points at.
	BridgeHost string
	BridgePort int

	// DialAddress is the exit role target: the entry agent ingress host:port.
	DialAddress string
	// BackendAddress is the exit role local delivery target host:port.
	BackendAddress string
	// RelayChain optionally routes the exit dial through host relay listeners.
	RelayChain []relay.Hop
}

// SessionStatus reports the live state of one session.
type SessionStatus struct {
	SessionID string
	Role      string
	State     string
	// IngressAddress is the entry role bound tunnel ingress address.
	IngressAddress string
	// BridgeAddress is the entry role bound loopback bridge address.
	BridgeAddress string
	LastError     string
}

func (spec SessionSpec) validate(agentID string) error {
	if err := validateSessionID(spec.SessionID); err != nil {
		return err
	}
	switch spec.Role {
	case RoleEntry:
		if strings.TrimSpace(spec.EntryAgentID) == "" || spec.EntryAgentID != strings.TrimSpace(agentID) {
			return errors.New("channel entry session does not belong to this agent")
		}
		if strings.TrimSpace(spec.ExitAgentID) == "" {
			return errors.New("channel entry session exit agent id is required")
		}
	case RoleExit:
		if strings.TrimSpace(spec.ExitAgentID) == "" || spec.ExitAgentID != strings.TrimSpace(agentID) {
			return errors.New("channel exit session does not belong to this agent")
		}
		if strings.TrimSpace(spec.EntryAgentID) == "" {
			return errors.New("channel exit session entry agent id is required")
		}
		if _, _, err := splitHostPortChecked(spec.DialAddress); err != nil {
			return fmt.Errorf("channel exit session dial address: %w", err)
		}
		if _, _, err := splitHostPortChecked(spec.BackendAddress); err != nil {
			return fmt.Errorf("channel exit session backend address: %w", err)
		}
	default:
		return fmt.Errorf("channel session role %q is unsupported", spec.Role)
	}
	switch spec.Protocol {
	case ProtocolTCP, ProtocolUDP:
	default:
		return fmt.Errorf("channel session protocol %q is unsupported", spec.Protocol)
	}
	if spec.ListenPort < 0 || spec.ListenPort > 65535 || spec.BridgePort < 0 || spec.BridgePort > 65535 {
		return errors.New("channel session port is out of range")
	}
	for index, hop := range spec.RelayChain {
		if err := relay.ValidateListener(hop.Listener); err != nil {
			return fmt.Errorf("channel session relay hop %d: %w", index, err)
		}
		if strings.TrimSpace(hop.Address) == "" {
			return fmt.Errorf("channel session relay hop %d address is required", index)
		}
	}
	return nil
}

// comparable returns the spec fields that define session identity for
// idempotent ensure. Agent-allocated outputs are excluded.
func (spec SessionSpec) comparable() SessionSpec {
	out := spec
	out.ListenHost = normalizeListenHost(spec.ListenHost)
	out.BridgeHost = normalizeBridgeHost(spec.BridgeHost)
	out.ListenPort = 0
	out.BridgePort = 0
	out.RelayChain = append([]relay.Hop(nil), spec.RelayChain...)
	return out
}

func validateSessionID(value string) error {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxSessionIDLength || strings.ContainsAny(value, "\r\n\x00") {
		return errors.New("channel session id is invalid")
	}
	return nil
}

func normalizeListenHost(host string) string {
	return strings.TrimSpace(host)
}

func normalizeBridgeHost(host string) string {
	if strings.TrimSpace(host) == "" {
		return "127.0.0.1"
	}
	return strings.TrimSpace(host)
}

func splitHostPortChecked(address string) (string, int, error) {
	host, portText, err := net.SplitHostPort(strings.TrimSpace(address))
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("port %q is invalid", portText)
	}
	if strings.TrimSpace(host) == "" {
		return "", 0, errors.New("host is required")
	}
	return host, port, nil
}

func joinHostPort(host string, port int) string {
	return net.JoinHostPort(host, strconv.Itoa(port))
}
