package sync

import "github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"

type Snapshot = model.Snapshot

type Client struct{}

func NewClient() *Client {
	return &Client{}
}

func (c *Client) Sync(_ Snapshot) error {
	return nil
}
