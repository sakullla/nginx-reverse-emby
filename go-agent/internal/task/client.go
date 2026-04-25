package task

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/config"
)

type HTTPTransportConfig = config.HTTPTransportConfig

type ClientConfig struct {
	MasterURL     string
	AgentToken    string
	AgentID       string
	AgentName     string
	Version       string
	Capabilities  []string
	ReconnectWait time.Duration
	HTTPTransport HTTPTransportConfig
	HTTPClient    *http.Client
	Handler       TaskHandler
}

type TaskHandler interface {
	HandleTask(context.Context, TaskMessage) (map[string]any, error)
}

type TaskHandlerFunc func(context.Context, TaskMessage) (map[string]any, error)

func (f TaskHandlerFunc) HandleTask(ctx context.Context, task TaskMessage) (map[string]any, error) {
	return f(ctx, task)
}

type Client struct {
	cfg        ClientConfig
	transport  *http.Transport
	sessionSeq uint64
}

func NewClient(cfg ClientConfig) *Client {
	if cfg.ReconnectWait <= 0 {
		cfg.ReconnectWait = time.Second
	}
	cfg.MasterURL = strings.TrimRight(cfg.MasterURL, "/")
	cfg.MasterURL = normalizeMasterBaseURL(cfg.MasterURL)
	if cfg.HTTPClient != nil {
		return &Client{cfg: cfg}
	}
	transportCfg := config.Default().HTTPTransport
	if cfg.HTTPTransport.DialTimeout > 0 {
		transportCfg.DialTimeout = cfg.HTTPTransport.DialTimeout
	}
	if cfg.HTTPTransport.TLSHandshakeTimeout > 0 {
		transportCfg.TLSHandshakeTimeout = cfg.HTTPTransport.TLSHandshakeTimeout
	}
	if cfg.HTTPTransport.ResponseHeaderTimeout > 0 {
		transportCfg.ResponseHeaderTimeout = cfg.HTTPTransport.ResponseHeaderTimeout
	}
	if cfg.HTTPTransport.IdleConnTimeout > 0 {
		transportCfg.IdleConnTimeout = cfg.HTTPTransport.IdleConnTimeout
	}
	if cfg.HTTPTransport.KeepAlive > 0 {
		transportCfg.KeepAlive = cfg.HTTPTransport.KeepAlive
	}
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   transportCfg.DialTimeout,
			KeepAlive: transportCfg.KeepAlive,
		}).DialContext,
		TLSHandshakeTimeout:   transportCfg.TLSHandshakeTimeout,
		ResponseHeaderTimeout: transportCfg.ResponseHeaderTimeout,
		IdleConnTimeout:       transportCfg.IdleConnTimeout,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	cfg.HTTPClient = &http.Client{Transport: transport}
	return &Client{cfg: cfg, transport: transport}
}

func (c *Client) Run(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		if err := c.runSession(ctx); err != nil && ctx.Err() == nil {
			timer := time.NewTimer(c.cfg.ReconnectWait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil
			case <-timer.C:
			}
			continue
		}
	}
}

func (c *Client) runSession(ctx context.Context) error {
	sessionID := c.nextSessionID()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.sessionURL(sessionID), nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Agent-Token", c.cfg.AgentToken)

	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		c.discardConnections()
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.discardConnections()
		return fmt.Errorf("task session failed: %s", resp.Status)
	}

	scanner := bufio.NewScanner(resp.Body)
	eventName := ""
	dataLines := make([]string, 0, 1)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return nil
		}
		line := scanner.Text()
		if line == "" {
			if err := c.handleSSEEvent(ctx, eventName, strings.Join(dataLines, "\n")); err != nil {
				return err
			}
			eventName = ""
			dataLines = dataLines[:0]
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

func (c *Client) sessionURL(sessionID string) string {
	return fmt.Sprintf(
		"%s/api/agents/task-session?agent_id=%s&session_id=%s",
		c.cfg.MasterURL,
		c.cfg.AgentID,
		sessionID,
	)
}

func (c *Client) nextSessionID() string {
	seq := atomic.AddUint64(&c.sessionSeq, 1)
	return fmt.Sprintf("%s-%d-%d", c.cfg.AgentID, time.Now().UTC().UnixNano(), seq)
}

func (c *Client) helloMessage(sessionID string) Message {
	return Message{
		Type: "hello",
		Hello: &HelloMessage{
			AgentID:      c.cfg.AgentID,
			SessionID:    sessionID,
			Version:      c.cfg.Version,
			Capabilities: append([]string(nil), c.cfg.Capabilities...),
		},
	}
}

func encodeMessage(msg Message) ([]byte, error) {
	return json.Marshal(msg)
}

func (c *Client) handleSSEEvent(ctx context.Context, eventName string, data string) error {
	if strings.TrimSpace(eventName) != "task" || strings.TrimSpace(data) == "" {
		return nil
	}

	var task TaskMessage
	if err := json.Unmarshal([]byte(data), &task); err != nil {
		return err
	}
	if strings.TrimSpace(task.TaskID) == "" || strings.TrimSpace(task.TaskType) == "" {
		return nil
	}

	if err := c.postUpdate(ctx, task.TaskID, map[string]any{"state": "running"}); err != nil {
		return err
	}
	if c.cfg.Handler == nil {
		return c.postUpdate(ctx, task.TaskID, map[string]any{
			"state": "failed",
			"error": "no task handler configured",
		})
	}

	taskCtx, cancel := contextWithTaskDeadline(ctx, task.Deadline)
	defer cancel()

	result, err := c.cfg.Handler.HandleTask(taskCtx, task)
	if err != nil {
		return c.postUpdate(ctx, task.TaskID, map[string]any{
			"state": "failed",
			"error": err.Error(),
		})
	}
	return c.postUpdate(ctx, task.TaskID, map[string]any{
		"state":  "completed",
		"result": result,
	})
}

func contextWithTaskDeadline(parent context.Context, rawDeadline string) (context.Context, context.CancelFunc) {
	deadline, err := time.Parse(time.RFC3339, strings.TrimSpace(rawDeadline))
	if err != nil || deadline.IsZero() {
		return context.WithCancel(parent)
	}
	return context.WithDeadline(parent, deadline)
}

func (c *Client) postUpdate(ctx context.Context, taskID string, payload map[string]any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.updateURL(taskID), bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Token", c.cfg.AgentToken)

	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		c.discardConnections()
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.discardConnections()
		return fmt.Errorf("task update failed: %s", resp.Status)
	}
	return nil
}

func (c *Client) discardConnections() {
	if c.transport != nil {
		c.transport.CloseIdleConnections()
	}
}

func (c *Client) updateURL(taskID string) string {
	return fmt.Sprintf("%s/api/agent-tasks/%s/updates", c.cfg.MasterURL, taskID)
}

func normalizeMasterBaseURL(raw string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	switch {
	case strings.HasSuffix(trimmed, "/panel-api"):
		return strings.TrimSuffix(trimmed, "/panel-api")
	case strings.HasSuffix(trimmed, "/api"):
		return strings.TrimSuffix(trimmed, "/api")
	default:
		return trimmed
	}
}
