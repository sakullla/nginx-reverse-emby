# WireGuard Relay And L4 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add reusable WireGuard profiles and use them for Relay transport plus L4 WireGuard inbound and outbound paths.

**Architecture:** The control plane owns agent-scoped WireGuard profiles and redacts secrets on reads. The agent applies profiles through a focused `internal/wireguard` runtime based on `golang.zx2c4.com/wireguard/tun/netstack`, then Relay and L4 consume the runtime through narrow dial/listen interfaces.

**Tech Stack:** Go 1.26, GORM/SQLite storage, Vue 3, Vite, `golang.zx2c4.com/wireguard`, existing Relay/L4 packages.

---

## File Structure

- Create `go-agent/internal/model/wireguard.go`: agent snapshot model types.
- Create `go-agent/internal/wireguard/config.go`: profile validation and config fingerprinting.
- Create `go-agent/internal/wireguard/runtime.go`: runtime manager with dial/listen interfaces.
- Modify `go-agent/internal/model/relay.go`: add `WireGuardProfileID`.
- Modify `go-agent/internal/model/l4.go`: add `WireGuardProfileID` and `WireGuardListenHost`.
- Modify `go-agent/internal/relay/*`: add `wireguard` transport mode and dial/listen hooks.
- Modify `go-agent/internal/l4/*`: add WireGuard inbound/egress modes.
- Modify `go-agent/internal/app/*`: pass profiles to relay and l4 managers.
- Modify `panel/backend-go/internal/controlplane/storage/*`: persist wireguard profiles.
- Create `panel/backend-go/internal/controlplane/service/wireguard.go`: CRUD, validation, redaction.
- Create `panel/backend-go/internal/controlplane/http/handlers_wireguard.go`: API handlers.
- Modify `panel/backend-go/internal/controlplane/service/relay.go`: profile reference validation.
- Modify `panel/backend-go/internal/controlplane/service/l4.go`: profile reference validation.
- Modify `panel/frontend/src/api/index.js`: wireguard profile client functions.
- Create `panel/frontend/src/hooks/useWireGuardProfiles.js`.
- Create `panel/frontend/src/pages/WireGuardProfilesPage.vue`.
- Modify `panel/frontend/src/components/RelayListenerForm.vue`: transport option/profile selector.
- Modify `panel/frontend/src/components/L4RuleForm.vue`: inbound/egress option/profile selector.

## Task 1: Backend WireGuard Profile Service

**Files:**
- Modify: `panel/backend-go/internal/controlplane/storage/sqlite_models.go`
- Modify: `panel/backend-go/internal/controlplane/storage/gorm_store.go`
- Modify: `panel/backend-go/internal/controlplane/storage/sqlite_store.go`
- Create: `panel/backend-go/internal/controlplane/service/wireguard.go`
- Test: `panel/backend-go/internal/controlplane/service/wireguard_test.go`

- [ ] **Step 1: Write failing profile normalization/redaction tests**

Add tests asserting:

```go
func TestWireGuardProfileCreateRedactsSecretsOnRead(t *testing.T) {
    // Create a profile with private_key and peer preshared_key.
    // Assert Create returns private_key "xxxxx" and peer preshared_key "xxxxx".
    // Assert List returns the same redacted values.
}

func TestWireGuardProfileRejectsInvalidCIDR(t *testing.T) {
    // Create a profile with addresses []string{"10.0.0.1"}.
    // Assert ErrInvalidArgument and message containing "addresses must be CIDR".
}

func TestWireGuardProfileUpdateKeepsRedactedSecrets(t *testing.T) {
    // Create profile with real secrets.
    // Update name while sending private_key "xxxxx" and peer preshared_key "xxxxx".
    // Load raw storage row and assert original secrets remain.
}
```

- [ ] **Step 2: Run test to verify RED**

Run: `cd panel/backend-go && go test ./internal/controlplane/service -run WireGuard -count=1`

Expected: fail because `NewWireGuardProfileService` and types do not exist.

- [ ] **Step 3: Add storage model and service types**

Add `WireGuardProfileRow` with JSON fields for addresses, peers, DNS, tags and `TableName() string { return "wireguard_profiles" }`. Add service types:

```go
type WireGuardPeer struct {
    Name string `json:"name"`
    PublicKey string `json:"public_key"`
    PresharedKey string `json:"preshared_key,omitempty"`
    Endpoint string `json:"endpoint"`
    AllowedIPs []string `json:"allowed_ips"`
    PersistentKeepaliveSeconds int `json:"persistent_keepalive_seconds,omitempty"`
}

type WireGuardProfile struct {
    ID int `json:"id"`
    AgentID string `json:"agent_id"`
    Name string `json:"name"`
    Mode string `json:"mode"`
    PrivateKey string `json:"private_key,omitempty"`
    ListenPort int `json:"listen_port"`
    Addresses []string `json:"addresses"`
    Peers []WireGuardPeer `json:"peers"`
    DNS []string `json:"dns"`
    MTU int `json:"mtu"`
    Enabled bool `json:"enabled"`
    Tags []string `json:"tags"`
    Revision int `json:"revision"`
}
```

- [ ] **Step 4: Implement validation and redaction**

Use `net/netip.ParsePrefix` for addresses and allowed IPs, `net.SplitHostPort` for endpoints when present, and base64 length checks for WireGuard keys. Use existing `redactedProxyPassword` value for secret placeholder compatibility.

- [ ] **Step 5: Run service tests to GREEN**

Run: `cd panel/backend-go && go test ./internal/controlplane/service -run WireGuard -count=1`

Expected: pass.

## Task 2: Backend API And Snapshot Contract

**Files:**
- Create: `panel/backend-go/internal/controlplane/http/handlers_wireguard.go`
- Modify: `panel/backend-go/internal/controlplane/http/router.go`
- Modify: `panel/backend-go/internal/controlplane/http/router_config_store_test.go`
- Modify: `panel/backend-go/internal/controlplane/storage/gorm_store.go`
- Modify: `go-agent/internal/model/wireguard.go`
- Modify: snapshot assembly files found by `rg "RelayListeners|L4Rules|Rules" panel/backend-go/internal/controlplane`
- Test: package tests under `panel/backend-go/internal/controlplane/http` and snapshot service tests.

- [ ] **Step 1: Write failing API tests**

Add tests asserting POST/GET profile endpoints redact secrets and invalid profile returns HTTP 400.

- [ ] **Step 2: Run API tests to verify RED**

Run: `cd panel/backend-go && go test ./internal/controlplane/http -run WireGuard -count=1`

Expected: fail because routes do not exist.

- [ ] **Step 3: Add dependencies, routes, handlers**

Wire handlers like existing L4/Relay handlers. JSON shape:

```json
{
  "ok": true,
  "profiles": []
}
```

and:

```json
{
  "ok": true,
  "profile": {}
}
```

- [ ] **Step 4: Include profiles in desired agent snapshot**

Add `WireGuardProfiles []model.WireGuardProfile` to the agent snapshot model and populate it for each agent. Do not include disabled profiles unless current snapshot conventions include disabled rules; follow the existing rules/listeners convention.

- [ ] **Step 5: Run backend tests to GREEN**

Run: `cd panel/backend-go && go test ./internal/controlplane/http ./internal/controlplane/service ./internal/controlplane/storage -run "WireGuard|Snapshot|Router" -count=1`

Expected: pass.

## Task 3: Relay And L4 Service Validation

**Files:**
- Modify: `panel/backend-go/internal/controlplane/service/relay.go`
- Modify: `panel/backend-go/internal/controlplane/service/l4.go`
- Test: `panel/backend-go/internal/controlplane/service/relay_test.go`
- Test: `panel/backend-go/internal/controlplane/service/l4_test.go`

- [ ] **Step 1: Write failing validation tests**

Add tests asserting:

```go
func TestRelayListenerWireGuardRequiresProfile(t *testing.T) {
    // transport_mode wireguard without wireguard_profile_id fails.
}

func TestL4WireGuardListenModeRequiresProfile(t *testing.T) {
    // listen_mode wireguard without wireguard_profile_id fails.
}

func TestL4WireGuardProxyEgressRequiresProfile(t *testing.T) {
    // listen_mode proxy + proxy_egress_mode wireguard without wireguard_profile_id fails.
}
```

- [ ] **Step 2: Run tests to verify RED**

Run: `cd panel/backend-go && go test ./internal/controlplane/service -run "WireGuard.*RequiresProfile|RelayListenerWireGuard|L4WireGuard" -count=1`

Expected: fail because fields/modes are unsupported.

- [ ] **Step 3: Add fields and validation**

Add `WireGuardProfileID *int` to relay listener input/output and row conversion. Add `WireGuardProfileID *int` and `WireGuardListenHost string` to L4 input/output and row conversion. Accept `transport_mode=wireguard`, `listen_mode=wireguard`, and `proxy_egress_mode=wireguard`.

- [ ] **Step 4: Validate profile references**

List profiles for the target agent and require the referenced profile to exist and be enabled.

- [ ] **Step 5: Run tests to GREEN**

Run: `cd panel/backend-go && go test ./internal/controlplane/service -run "WireGuard|Relay|L4" -count=1`

Expected: pass.

## Task 4: Agent WireGuard Runtime Foundation

**Files:**
- Modify: `go-agent/go.mod`
- Create: `go-agent/internal/model/wireguard.go`
- Create: `go-agent/internal/wireguard/config.go`
- Create: `go-agent/internal/wireguard/runtime.go`
- Test: `go-agent/internal/wireguard/config_test.go`
- Test: `go-agent/internal/wireguard/runtime_test.go`

- [ ] **Step 1: Write failing config tests**

Add tests for key validation, CIDR parsing, peer endpoint parsing, and fingerprint changes when peer endpoint changes.

- [ ] **Step 2: Run tests to verify RED**

Run: `cd go-agent && go test ./internal/wireguard -count=1`

Expected: fail because package does not exist.

- [ ] **Step 3: Add dependency and model**

Run: `cd go-agent && go get golang.zx2c4.com/wireguard@latest`

Add `model.WireGuardProfile` matching backend JSON.

- [ ] **Step 4: Implement config validation/fingerprint**

Normalize addresses and peers using `net/netip`. Fingerprint stable JSON without redacted secrets.

- [ ] **Step 5: Implement runtime manager interface**

Expose:

```go
type Runtime interface {
    DialContext(ctx context.Context, network string, address string) (net.Conn, error)
    ListenTCP(ctx context.Context, address string) (net.Listener, error)
    ListenUDP(ctx context.Context, address string) (PacketConn, error)
    Close() error
}

type Manager struct { /* cache by profile ID and fingerprint */ }
```

- [ ] **Step 6: Run tests to GREEN**

Run: `cd go-agent && go test ./internal/wireguard -count=1`

Expected: pass.

## Task 5: Relay WireGuard Transport

**Files:**
- Modify: `go-agent/internal/model/relay.go`
- Modify: `go-agent/internal/relay/protocol.go`
- Modify: `go-agent/internal/relay/validation.go`
- Modify: `go-agent/internal/relay/runtime.go`
- Modify: `go-agent/internal/relay/dial_runtime.go`
- Modify: `go-agent/internal/app/relay_runtime.go`
- Test: `go-agent/internal/relay/runtime_test.go`

- [ ] **Step 1: Write failing relay tests**

Add tests asserting `ValidateListener` accepts `transport_mode=wireguard` only with `wireguard_profile_id`, and rejects obfs/fallback for WireGuard.

- [ ] **Step 2: Run tests to verify RED**

Run: `cd go-agent && go test ./internal/relay -run WireGuard -count=1`

Expected: fail because transport mode unsupported.

- [ ] **Step 3: Add transport constant and validation**

Add `ListenerTransportModeWireGuard = "wireguard"` and normalize it.

- [ ] **Step 4: Add runtime hooks**

Relay server receives a WireGuard runtime provider from app. For `wireguard`, call `runtime.ListenTCP(ctx, listen_host:listen_port)` and reuse `acceptLoop`.

- [ ] **Step 5: Add dial hook**

For first hop `wireguard`, dial first hop address through the referenced profile runtime and then run existing TLS/mux handshake over that connection only if TLS is still required. For first version, preserve Relay TLS over WG to avoid weakening auth.

- [ ] **Step 6: Run relay tests to GREEN**

Run: `cd go-agent && go test ./internal/relay -run WireGuard -count=1`

Expected: pass.

## Task 6: L4 WireGuard Inbound And Egress

**Files:**
- Modify: `go-agent/internal/model/l4.go`
- Modify: `go-agent/internal/l4/server.go`
- Modify: `go-agent/internal/l4/tcp.go`
- Modify: `go-agent/internal/l4/udp.go`
- Modify: `go-agent/internal/l4/relay_paths.go`
- Modify: `go-agent/internal/app/l4_runtime.go`
- Test: `go-agent/internal/l4/server_test.go`

- [ ] **Step 1: Write failing L4 tests**

Add tests asserting:

```go
func TestValidateRuleAllowsWireGuardListenModeWithProfile(t *testing.T) {}
func TestValidateRuleRejectsWireGuardListenModeWithoutProfile(t *testing.T) {}
func TestProxyEntryDialUsesWireGuardEgress(t *testing.T) {}
```

- [ ] **Step 2: Run tests to verify RED**

Run: `cd go-agent && go test ./internal/l4 -run WireGuard -count=1`

Expected: fail because fields/modes unsupported.

- [ ] **Step 3: Add model fields and validation**

Allow `listen_mode=wireguard` for TCP and UDP. Allow `proxy_egress_mode=wireguard` for TCP proxy entry. Require `WireGuardProfileID`.

- [ ] **Step 4: Implement inbound listener selection**

In server startup, route `listen_mode=wireguard` to WireGuard runtime `ListenTCP` or `ListenUDP` instead of host `net.Listen`.

- [ ] **Step 5: Implement egress dial**

In `dialProxyEntryUpstream`, add `case "wireguard": return s.wireGuardDialer.DialContext(s.ctx, "tcp", target)`.

- [ ] **Step 6: Run L4 tests to GREEN**

Run: `cd go-agent && go test ./internal/l4 -run WireGuard -count=1`

Expected: pass.

## Task 7: Frontend WireGuard Profiles And Forms

**Files:**
- Modify: `panel/frontend/src/api/index.js`
- Create: `panel/frontend/src/hooks/useWireGuardProfiles.js`
- Create: `panel/frontend/src/pages/WireGuardProfilesPage.vue`
- Modify: `panel/frontend/src/router/index.js`
- Modify: `panel/frontend/src/components/layout/Sidebar.vue`
- Modify: `panel/frontend/src/components/RelayListenerForm.vue`
- Modify: `panel/frontend/src/components/L4RuleForm.vue`
- Test: `panel/frontend/src/api/runtimeCanonicalPayloads.test.mjs`

- [ ] **Step 1: Write failing payload tests**

Add tests that Relay listener payload sends `transport_mode: "wireguard"` and `wireguard_profile_id`, and L4 payload sends `listen_mode: "wireguard"` or `proxy_egress_mode: "wireguard"` with `wireguard_profile_id`.

- [ ] **Step 2: Run tests to verify RED**

Run: `cd panel/frontend && npm test -- --run runtimeCanonicalPayloads`

Expected: fail because forms/API do not emit fields.

- [ ] **Step 3: Add API hooks**

Mirror existing `useRelayListeners` hook style for list/create/update/delete profiles.

- [ ] **Step 4: Add profile page**

Build a compact management page with fields for name, private key, listen port, addresses, peers, DNS, MTU, enabled, and tags. Preserve redacted secrets by sending unchanged redacted values only when unchanged.

- [ ] **Step 5: Update Relay and L4 forms**

Add selects and profile selector. Disable incompatible obfs/fallback controls for WireGuard.

- [ ] **Step 6: Run frontend tests/build**

Run: `cd panel/frontend && npm run build`

Expected: build passes.

## Task 8: Documentation And Full Verification

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Document WireGuard usage**

Add sections describing:

- creating a generic WireGuard profile
- L4 clients connecting through WireGuard endpoint then service virtual IP/port
- Cloudflare WARP caveat: use standard exported profile or external WARP client routing
- Relay transport mode `wireguard`

- [ ] **Step 2: Run focused verification**

Run:

```powershell
cd panel/backend-go; go test ./...
cd ../../go-agent; go test ./...
cd ../panel/frontend; npm run build
```

Expected: all pass.

- [ ] **Step 3: Run image verification if dependency/build files changed**

Run from repo root:

```powershell
docker build -t nginx-reverse-emby .
```

Expected: image builds successfully.

