package task

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

type ClientConfig struct {
	MasterURL     string
	AgentToken    string
	AgentID       string
	AgentName     string
	Version       string
	Capabilities  []string
	ReconnectWait time.Duration
	HTTPClient    *http.Client
}

type Client struct {
	cfg        ClientConfig
	sessionSeq uint64
}

func NewClient(cfg ClientConfig) *Client {
	if cfg.ReconnectWait <= 0 {
		cfg.ReconnectWait = time.Second
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = http.DefaultClient
	}
	cfg.MasterURL = strings.TrimRight(cfg.MasterURL, "/")
	return &Client{cfg: cfg}
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
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("task session failed: %s", resp.Status)
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return nil
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
