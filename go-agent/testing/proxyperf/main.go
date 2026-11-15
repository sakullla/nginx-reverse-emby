//go:build integration

// proxyperf runs the production forwarding modules in an isolated Docker
// fixture. It intentionally has no Agent control-plane or PKI enrollment API.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/certs"
	httpmodule "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/http"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/l4"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/traffic"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	agentID := os.Getenv("NRE_AGENT_ID")
	traffic.SetEnabled(os.Getenv("NRE_TRAFFIC_STATS_ENABLED") != "false")
	defer relay.ConfigureTimeouts(relay.TimeoutConfig{DialTimeout: envDuration("NRE_RELAY_DIAL_TIMEOUT"), HandshakeTimeout: envDuration("NRE_RELAY_HANDSHAKE_TIMEOUT"), FrameTimeout: envDuration("NRE_RELAY_FRAME_TIMEOUT"), IdleTimeout: envDuration("NRE_RELAY_IDLE_TIMEOUT")})()
	request, err := json.Marshal(map[string]string{"agent_id": agentID})
	if err != nil {
		log.Fatal(err)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(os.Getenv("NRE_MASTER_URL")+"/api/agents/heartbeat", "application/json", bytes.NewReader(request))
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Fatalf("fixture snapshot status: %d", resp.StatusCode)
	}
	var reply struct {
		Sync model.Snapshot `json:"sync"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		log.Fatal(err)
	}
	certModule, err := certs.NewManagedModule(os.Getenv("NRE_DATA_DIR"))
	if err != nil {
		log.Fatal(err)
	}
	registry := module.NewRegistry()
	maxConns, _ := strconv.Atoi(os.Getenv("NRE_HTTP_MAX_CONNS_PER_HOST"))
	httpConfig := httpmodule.Config{AgentID: agentID, Transport: httpmodule.TransportOptions{DialTimeout: envDuration("NRE_HTTP_DIAL_TIMEOUT"), TLSHandshakeTimeout: envDuration("NRE_HTTP_TLS_HANDSHAKE_TIMEOUT"), ResponseHeaderTimeout: envDuration("NRE_HTTP_RESPONSE_HEADER_TIMEOUT"), IdleConnTimeout: envDuration("NRE_HTTP_IDLE_CONN_TIMEOUT"), KeepAlive: envDuration("NRE_HTTP_KEEP_ALIVE"), MaxConnsPerHost: maxConns}}
	for _, mod := range []module.Module{certModule, relay.NewModule(relay.Config{AgentID: agentID}), httpmodule.NewModule(httpConfig), l4.NewModule(l4.Config{AgentID: agentID})} {
		if err := registry.Register(mod); err != nil {
			log.Fatal(err)
		}
	}
	if err := registry.Apply(ctx, model.Snapshot{}, reply.Sync); err != nil {
		log.Fatal(err)
	}
	log.Printf("forwarding fixture %s ready (production modules, ephemeral pinned TLS)", agentID)
	ready, err := client.Post(os.Getenv("NRE_MASTER_URL")+"/node-ready?id="+agentID, "text/plain", nil)
	if err != nil {
		log.Fatal(err)
	}
	_ = ready.Body.Close()
	if ready.StatusCode != 200 {
		log.Fatalf("fixture readiness status: %d", ready.StatusCode)
	}
	<-ctx.Done()
	stopCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if err := registry.StopAll(stopCtx); err != nil {
		log.Print(err)
	}
}

func envDuration(name string) time.Duration {
	value := os.Getenv(name)
	if value == "" {
		return 0
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		log.Fatalf("%s: %v", name, err)
	}
	return duration
}
