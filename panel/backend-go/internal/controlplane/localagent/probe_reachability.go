package localagent

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const localProbeTimeout = 5 * time.Second

// probeLocalBackendReachability dials a literal HTTP target from this process.
// A relay listener id has no in-process trust material, so that hop stays
// untested and is never reported as connected.
func probeLocalBackendReachability(ctx context.Context, agentID string, payload map[string]any) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	target, listenerID, hasListener, reason := localProbePayload(payload)
	checkedAt := time.Now().UTC().Format(time.RFC3339Nano)
	if hasListener {
		result := localProbeResult(agentID, target, checkedAt, "untested", listenerID, true, 0, "relay trust material is unavailable")
		if reason != "" {
			result["error"] = reason
		}
		return result, nil
	}
	if reason != "" {
		return localProbeResult(agentID, target, checkedAt, "untested", 0, false, 0, reason), nil
	}
	parsed, err := url.Parse(target)
	if err != nil || parsed == nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return localProbeResult(agentID, target, checkedAt, "untested", 0, false, 0, "target_url is not a literal http url"), nil
	}
	status, code, detail, err := dialLocalLiteral(ctx, parsed)
	if err != nil {
		return nil, err
	}
	return localProbeResult(agentID, target, checkedAt, status, 0, false, code, detail), nil
}

func localProbePayload(payload map[string]any) (string, int, bool, string) {
	if payload == nil {
		return "", 0, false, "target_url is required"
	}
	target, _ := payload["target_url"].(string)
	target = strings.TrimSpace(target)
	value, ok := payload["relay_listener_id"]
	if !ok || value == nil {
		if target == "" {
			return "", 0, false, "target_url is required"
		}
		return target, 0, false, ""
	}
	listenerID, err := localProbeListenerID(value)
	if err != nil {
		if target == "" {
			target = strings.TrimSpace(target)
		}
		return target, 0, true, "invalid relay_listener_id"
	}
	return target, listenerID, true, ""
}

func localProbeListenerID(value any) (int, error) {
	switch typed := value.(type) {
	case int:
		if typed <= 0 {
			return 0, errors.New("must be a positive integer")
		}
		return typed, nil
	case int32:
		if typed <= 0 {
			return 0, errors.New("must be a positive integer")
		}
		return int(typed), nil
	case int64:
		if typed <= 0 {
			return 0, errors.New("must be a positive integer")
		}
		return int(typed), nil
	case float64:
		if typed <= 0 || typed != float64(int(typed)) {
			return 0, errors.New("must be a positive integer")
		}
		return int(typed), nil
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err != nil || parsed <= 0 {
			return 0, errors.New("must be a positive integer")
		}
		return parsed, nil
	default:
		return 0, errors.New("must be a positive integer")
	}
}

func dialLocalLiteral(ctx context.Context, target *url.URL) (string, int, string, error) {
	timeout := localProbeTimeout
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining > 0 && remaining < timeout {
			timeout = remaining
		}
	}
	dialer := &net.Dialer{Timeout: timeout}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, addr)
		},
		ForceAttemptHTTP2:     false,
		DisableKeepAlives:     true,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
	}
	client := &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return "untested", 0, "target_url is not a literal http url", nil
	}
	response, err := client.Do(request)
	if response != nil {
		defer response.Body.Close()
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		if response.StatusCode > 0 {
			return "http_status", response.StatusCode, "", nil
		}
	}
	if err != nil && errors.Is(err, context.Canceled) && ctx.Err() != nil {
		return "", 0, "", err
	}
	if err != nil {
		return "network_error", 0, err.Error(), nil
	}
	return "untested", 0, "dial evidence is incomplete", nil
}

func localProbeResult(agentID, target, checkedAt, status string, listenerID int, hasListener bool, httpCode int, detail string) map[string]any {
	result := map[string]any{
		"agent_id":   agentID,
		"target":     target,
		"checked_at": checkedAt,
		"status":     status,
	}
	if hasListener {
		result["relay_listener_id"] = listenerID
	}
	if status == "http_status" {
		result["http_status_code"] = httpCode
	}
	if detail != "" && status != "http_status" && status != "connected" {
		result["error"] = detail
	}
	return result
}
