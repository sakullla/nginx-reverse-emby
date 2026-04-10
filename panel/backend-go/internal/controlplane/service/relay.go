package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

var ErrRelayListenerNotFound = errors.New("relay listener not found")

type RelayPin struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type RelayListener struct {
	ID                      int        `json:"id"`
	AgentID                 string     `json:"agent_id"`
	Name                    string     `json:"name"`
	BindHosts               []string   `json:"bind_hosts"`
	ListenHost              string     `json:"listen_host"`
	ListenPort              int        `json:"listen_port"`
	PublicHost              string     `json:"public_host"`
	PublicPort              int        `json:"public_port"`
	Enabled                 bool       `json:"enabled"`
	CertificateID           *int       `json:"certificate_id"`
	TLSMode                 string     `json:"tls_mode"`
	PinSet                  []RelayPin `json:"pin_set"`
	TrustedCACertificateIDs []int      `json:"trusted_ca_certificate_ids"`
	AllowSelfSigned         bool       `json:"allow_self_signed"`
	Tags                    []string   `json:"tags"`
	Revision                int        `json:"revision"`
}

type RelayListenerInput struct {
	ID                      *int        `json:"id,omitempty"`
	Name                    *string     `json:"name,omitempty"`
	BindHosts               *[]string   `json:"bind_hosts,omitempty"`
	ListenHost              *string     `json:"listen_host,omitempty"`
	ListenPort              *int        `json:"listen_port,omitempty"`
	PublicHost              *string     `json:"public_host,omitempty"`
	PublicPort              *int        `json:"public_port,omitempty"`
	Enabled                 *bool       `json:"enabled,omitempty"`
	CertificateID           *int        `json:"certificate_id,omitempty"`
	TLSMode                 *string     `json:"tls_mode,omitempty"`
	PinSet                  *[]RelayPin `json:"pin_set,omitempty"`
	TrustedCACertificateIDs *[]int      `json:"trusted_ca_certificate_ids,omitempty"`
	AllowSelfSigned         *bool       `json:"allow_self_signed,omitempty"`
	Tags                    *[]string   `json:"tags,omitempty"`
}

type relayService struct {
	cfg   config.Config
	store storage.Store
}

func NewRelayListenerService(cfg config.Config, store storage.Store) *relayService {
	return &relayService{cfg: cfg, store: store}
}

func (s *relayService) List(ctx context.Context, agentID string) ([]RelayListener, error) {
	resolvedID, err := s.ensureAgentExists(ctx, agentID)
	if err != nil {
		return nil, err
	}

	rows, err := s.store.ListRelayListeners(ctx, resolvedID)
	if err != nil {
		return nil, err
	}

	listeners := make([]RelayListener, 0, len(rows))
	for _, row := range rows {
		listeners = append(listeners, relayListenerFromRow(row))
	}
	return listeners, nil
}

func (s *relayService) Create(ctx context.Context, agentID string, input RelayListenerInput) (RelayListener, error) {
	resolvedID, err := s.ensureAgentExists(ctx, agentID)
	if err != nil {
		return RelayListener{}, err
	}

	allRows, err := s.store.ListRelayListeners(ctx, "")
	if err != nil {
		return RelayListener{}, err
	}
	rows, err := s.store.ListRelayListeners(ctx, resolvedID)
	if err != nil {
		return RelayListener{}, err
	}

	maxID := 0
	maxRevision := 0
	for _, row := range allRows {
		if row.ID > maxID {
			maxID = row.ID
		}
		if row.Revision > maxRevision {
			maxRevision = row.Revision
		}
	}

	listener, err := normalizeRelayListenerInput(input, RelayListener{}, maxID+1)
	if err != nil {
		return RelayListener{}, err
	}
	listener.AgentID = resolvedID
	listener.Revision = maxRevision + 1

	rows = append(rows, relayListenerToRow(listener))
	if err := s.store.SaveRelayListeners(ctx, resolvedID, rows); err != nil {
		return RelayListener{}, err
	}
	return listener, nil
}

func (s *relayService) Update(ctx context.Context, agentID string, id int, input RelayListenerInput) (RelayListener, error) {
	resolvedID, err := s.ensureAgentExists(ctx, agentID)
	if err != nil {
		return RelayListener{}, err
	}

	rows, err := s.store.ListRelayListeners(ctx, resolvedID)
	if err != nil {
		return RelayListener{}, err
	}

	maxRevision := 0
	targetIndex := -1
	var current RelayListener
	for i, row := range rows {
		if row.Revision > maxRevision {
			maxRevision = row.Revision
		}
		if row.ID == id {
			targetIndex = i
			current = relayListenerFromRow(row)
		}
	}
	if targetIndex < 0 {
		return RelayListener{}, ErrRelayListenerNotFound
	}

	listener, err := normalizeRelayListenerInput(input, current, id)
	if err != nil {
		return RelayListener{}, err
	}
	listener.AgentID = resolvedID
	listener.Revision = maxRevision + 1

	rows[targetIndex] = relayListenerToRow(listener)
	if err := s.store.SaveRelayListeners(ctx, resolvedID, rows); err != nil {
		return RelayListener{}, err
	}
	return listener, nil
}

func (s *relayService) Delete(ctx context.Context, agentID string, id int) (RelayListener, error) {
	resolvedID, err := s.ensureAgentExists(ctx, agentID)
	if err != nil {
		return RelayListener{}, err
	}

	rows, err := s.store.ListRelayListeners(ctx, resolvedID)
	if err != nil {
		return RelayListener{}, err
	}

	targetIndex := -1
	var deleted RelayListener
	for i, row := range rows {
		if row.ID == id {
			targetIndex = i
			deleted = relayListenerFromRow(row)
			break
		}
	}
	if targetIndex < 0 {
		return RelayListener{}, ErrRelayListenerNotFound
	}

	next := append([]storage.RelayListenerRow(nil), rows[:targetIndex]...)
	next = append(next, rows[targetIndex+1:]...)
	if err := s.store.SaveRelayListeners(ctx, resolvedID, next); err != nil {
		return RelayListener{}, err
	}
	return deleted, nil
}

func (s *relayService) ensureAgentExists(ctx context.Context, agentID string) (string, error) {
	resolvedID := strings.TrimSpace(agentID)
	if resolvedID == "" {
		resolvedID = s.cfg.LocalAgentID
	}
	if s.cfg.EnableLocalAgent && resolvedID == s.cfg.LocalAgentID {
		return resolvedID, nil
	}

	rows, err := s.store.ListAgents(ctx)
	if err != nil {
		return "", err
	}
	for _, row := range rows {
		if row.ID == resolvedID {
			return resolvedID, nil
		}
	}
	return "", ErrAgentNotFound
}

func normalizeRelayListenerInput(input RelayListenerInput, fallback RelayListener, suggestedID int) (RelayListener, error) {
	id := fallback.ID
	if input.ID != nil && *input.ID > 0 {
		id = *input.ID
	}
	if id <= 0 {
		id = suggestedID
	}

	name := strings.TrimSpace(pointerString(input.Name))
	if name == "" {
		name = strings.TrimSpace(fallback.Name)
	}
	if name == "" {
		return RelayListener{}, fmt.Errorf("%w: name is required", ErrInvalidArgument)
	}

	listenPort := fallback.ListenPort
	if input.ListenPort != nil {
		listenPort = *input.ListenPort
	}
	if listenPort < 1 || listenPort > 65535 {
		return RelayListener{}, fmt.Errorf("%w: listen_port must be an integer between 1 and 65535", ErrInvalidArgument)
	}

	bindHosts := append([]string(nil), fallback.BindHosts...)
	if input.BindHosts != nil {
		bindHosts = normalizeTags(*input.BindHosts)
	}
	listenHost := strings.TrimSpace(pointerString(input.ListenHost))
	if listenHost == "" {
		listenHost = strings.TrimSpace(fallback.ListenHost)
	}
	if len(bindHosts) == 0 {
		if listenHost == "" {
			listenHost = "0.0.0.0"
		}
		bindHosts = []string{listenHost}
	}
	listenHost = bindHosts[0]

	publicHost := strings.TrimSpace(pointerString(input.PublicHost))
	if publicHost == "" {
		publicHost = strings.TrimSpace(fallback.PublicHost)
	}
	if publicHost == "" {
		publicHost = listenHost
	}

	publicPort := fallback.PublicPort
	if input.PublicPort != nil {
		publicPort = *input.PublicPort
	}
	if publicPort <= 0 {
		publicPort = listenPort
	}
	if publicPort < 1 || publicPort > 65535 {
		return RelayListener{}, fmt.Errorf("%w: public_port must be an integer between 1 and 65535", ErrInvalidArgument)
	}

	enabled := true
	if fallback.ID > 0 {
		enabled = fallback.Enabled
	}
	if input.Enabled != nil {
		enabled = *input.Enabled
	}

	var certID *int
	if fallback.CertificateID != nil {
		value := *fallback.CertificateID
		certID = &value
	}
	if input.CertificateID != nil {
		if *input.CertificateID > 0 {
			value := *input.CertificateID
			certID = &value
		} else {
			certID = nil
		}
	}

	tlsMode := strings.TrimSpace(pointerString(input.TLSMode))
	if tlsMode == "" {
		tlsMode = fallback.TLSMode
	}
	if tlsMode == "" {
		tlsMode = "pin_or_ca"
	}
	switch tlsMode {
	case "pin_only", "ca_only", "pin_or_ca", "pin_and_ca":
	default:
		return RelayListener{}, fmt.Errorf("%w: tls_mode must be pin_only, ca_only, pin_or_ca, or pin_and_ca", ErrInvalidArgument)
	}

	pinSet := append([]RelayPin(nil), fallback.PinSet...)
	if input.PinSet != nil {
		pinSet = normalizeRelayPins(*input.PinSet)
	}

	trustedCAIDs := append([]int(nil), fallback.TrustedCACertificateIDs...)
	if input.TrustedCACertificateIDs != nil {
		trustedCAIDs = normalizeRelayCAIDs(*input.TrustedCACertificateIDs)
	}

	allowSelfSigned := fallback.AllowSelfSigned
	if input.AllowSelfSigned != nil {
		allowSelfSigned = *input.AllowSelfSigned
	}

	tags := append([]string(nil), fallback.Tags...)
	if input.Tags != nil {
		tags = normalizeTags(*input.Tags)
	}

	if enabled {
		if certID == nil {
			return RelayListener{}, fmt.Errorf("%w: certificate_id is required when relay listener is enabled", ErrInvalidArgument)
		}
		switch tlsMode {
		case "pin_and_ca":
			if len(pinSet) == 0 || len(trustedCAIDs) == 0 {
				return RelayListener{}, fmt.Errorf("%w: pin_and_ca requires both pin_set and trusted_ca_certificate_ids", ErrInvalidArgument)
			}
		case "pin_only":
			if len(pinSet) == 0 {
				return RelayListener{}, fmt.Errorf("%w: pin_only requires pin_set", ErrInvalidArgument)
			}
		case "ca_only":
			if len(trustedCAIDs) == 0 {
				return RelayListener{}, fmt.Errorf("%w: ca_only requires trusted_ca_certificate_ids", ErrInvalidArgument)
			}
		default:
			if len(pinSet) == 0 && len(trustedCAIDs) == 0 {
				return RelayListener{}, fmt.Errorf("%w: pin_set and trusted_ca_certificate_ids cannot both be empty", ErrInvalidArgument)
			}
		}
	}

	return RelayListener{
		ID:                      id,
		AgentID:                 fallback.AgentID,
		Name:                    name,
		BindHosts:               bindHosts,
		ListenHost:              listenHost,
		ListenPort:              listenPort,
		PublicHost:              publicHost,
		PublicPort:              publicPort,
		Enabled:                 enabled,
		CertificateID:           certID,
		TLSMode:                 tlsMode,
		PinSet:                  pinSet,
		TrustedCACertificateIDs: trustedCAIDs,
		AllowSelfSigned:         allowSelfSigned,
		Tags:                    tags,
		Revision:                fallback.Revision,
	}, nil
}

func normalizeRelayPins(pins []RelayPin) []RelayPin {
	normalized := make([]RelayPin, 0, len(pins))
	for _, pin := range pins {
		if strings.TrimSpace(pin.Type) == "" || strings.TrimSpace(pin.Value) == "" {
			continue
		}
		normalized = append(normalized, RelayPin{
			Type:  strings.TrimSpace(pin.Type),
			Value: strings.TrimSpace(pin.Value),
		})
	}
	return normalized
}

func normalizeRelayCAIDs(values []int) []int {
	seen := map[int]struct{}{}
	normalized := make([]int, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	return normalized
}

func relayListenerFromRow(row storage.RelayListenerRow) RelayListener {
	listener := RelayListener{
		ID:              row.ID,
		AgentID:         row.AgentID,
		Name:            row.Name,
		ListenHost:      defaultString(row.ListenHost, "0.0.0.0"),
		ListenPort:      row.ListenPort,
		PublicHost:      defaultString(row.PublicHost, row.ListenHost),
		PublicPort:      row.PublicPort,
		Enabled:         row.Enabled,
		CertificateID:   row.CertificateID,
		TLSMode:         defaultString(row.TLSMode, "pin_or_ca"),
		AllowSelfSigned: row.AllowSelfSigned,
		Tags:            parseStringArray(row.TagsJSON),
		Revision:        row.Revision,
	}
	if err := json.Unmarshal([]byte(defaultString(row.BindHostsJSON, "[]")), &listener.BindHosts); err != nil {
		listener.BindHosts = []string{listener.ListenHost}
	}
	if len(listener.BindHosts) == 0 {
		listener.BindHosts = []string{listener.ListenHost}
	}
	if err := json.Unmarshal([]byte(defaultString(row.PinSetJSON, "[]")), &listener.PinSet); err != nil {
		listener.PinSet = []RelayPin{}
	}
	listener.PinSet = normalizeRelayPins(listener.PinSet)
	listener.TrustedCACertificateIDs = parseIntArray(row.TrustedCACertificateIDs)
	if listener.PublicPort <= 0 {
		listener.PublicPort = listener.ListenPort
	}
	return listener
}

func relayListenerToRow(listener RelayListener) storage.RelayListenerRow {
	return storage.RelayListenerRow{
		ID:                      listener.ID,
		AgentID:                 listener.AgentID,
		Name:                    listener.Name,
		BindHostsJSON:           marshalJSON(listener.BindHosts, "[]"),
		ListenHost:              listener.ListenHost,
		ListenPort:              listener.ListenPort,
		PublicHost:              listener.PublicHost,
		PublicPort:              listener.PublicPort,
		Enabled:                 listener.Enabled,
		CertificateID:           listener.CertificateID,
		TLSMode:                 listener.TLSMode,
		PinSetJSON:              marshalJSON(listener.PinSet, "[]"),
		TrustedCACertificateIDs: marshalJSON(listener.TrustedCACertificateIDs, "[]"),
		AllowSelfSigned:         listener.AllowSelfSigned,
		TagsJSON:                marshalJSON(listener.Tags, "[]"),
		Revision:                listener.Revision,
	}
}
