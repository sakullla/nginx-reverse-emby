package pluginsdk

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// DualStackListenBinding is a Host-owned dedicated TCP+UDP listen on one port.
// BindHost may be a wildcard; share host is selected from NodeAddresses, never
// from the wildcard itself.
type DualStackListenBinding struct {
	Port     int    `json:"port"`
	BindHost string `json:"bind_host,omitempty"`
	TCP      bool   `json:"tcp"`
	UDP      bool   `json:"udp"`
}

func (binding DualStackListenBinding) Validate() error {
	if binding.Port < 1 || binding.Port > 65535 {
		return fmt.Errorf("dual-stack listen port %d is out of range", binding.Port)
	}
	if !binding.TCP || !binding.UDP {
		return errors.New("dual-stack listen requires both TCP and UDP")
	}
	return nil
}

// DualStackListener is a Host-owned handle. Binding reports the actual listen
// after the Host has bound the sockets for listenerRef. Plugins do not open
// public sockets themselves.
type DualStackListener interface {
	Binding(context.Context, string) (DualStackListenBinding, error)
}

// JoinShareHostPort formats a selected share host and listen port for SIP002
// and for existing L4 backend_host:backend_port fill-in.
func JoinShareHostPort(host string, port int) (string, error) {
	host, ok := ShareableHost(host)
	if !ok || port < 1 || port > 65535 {
		return "", errors.New("share host or port is not publishable")
	}
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}

// ValidL4BackendHost is the same bound used by reverse-l4 backend_host.
func ValidL4BackendHost(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 253 && !strings.Contains(value, "://") && !strings.ContainsAny(value, "/\\ \t\r\n\x00")
}
