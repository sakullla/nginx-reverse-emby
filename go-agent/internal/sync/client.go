package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type Snapshot = model.Snapshot

type ClientConfig struct {
	MasterURL      string
	AgentToken     string
	AgentID        string
	AgentName      string
	CurrentVersion string
	Platform       string
}

type Client struct {
	cfg    ClientConfig
	client *http.Client
}

func NewClient(cfg ClientConfig, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	cfg.MasterURL = strings.TrimRight(cfg.MasterURL, "/")
	return &Client{cfg: cfg, client: httpClient}
}

func (c *Client) Sync(ctx context.Context, _ Snapshot) (Snapshot, error) {
	payload := struct {
		Name            string `json:"name"`
		AgentID         string `json:"agent_id"`
		CurrentRevision int    `json:"current_revision"`
		Version         string `json:"version"`
		Platform        string `json:"platform"`
	}{
		Name:            c.cfg.AgentName,
		AgentID:         c.cfg.AgentID,
		CurrentRevision: 0,
		Version:         c.cfg.CurrentVersion,
		Platform:        c.cfg.Platform,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return Snapshot{}, err
	}

	endpoint := c.cfg.MasterURL + "/api/agents/heartbeat"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return Snapshot{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-agent-token", c.cfg.AgentToken)

	resp, err := c.client.Do(req)
	if err != nil {
		return Snapshot{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Snapshot{}, fmt.Errorf("heartbeat failed: %s", resp.Status)
	}

	var reply struct {
		Sync struct {
			DesiredVersion string `json:"desired_version"`
		} `json:"sync"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		return Snapshot{}, err
	}

	return Snapshot{DesiredVersion: reply.Sync.DesiredVersion}, nil
}
