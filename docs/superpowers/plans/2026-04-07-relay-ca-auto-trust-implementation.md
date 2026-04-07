# Relay CA Auto Trust Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a control-plane-managed global Relay CA, default auto-issued Relay listener certificates, and automatic CA+Pin trust derivation for HTTP/L4 relay chains without requiring users to hand-maintain Pin material.

**Architecture:** Keep the existing `managed_certificates`, `relay_listeners`, `relay_chain`, and Go relay TLS runtime shapes, but teach the control plane to bootstrap a singleton Relay CA, auto-issue `relay_tunnel` certificates for listeners, and derive listener trust material server-side. Frontend changes stay declarative and friendly, while agent/runtime compatibility is preserved by continuing to ship `certificate_id`, `tls_mode`, `pin_set`, and `trusted_ca_certificate_ids`.

**Tech Stack:** Vue 3 + Vite SPA, Node/CommonJS backend, JSON/SQLite/Prisma-backed storage, Go agent relay/certificate runtime, Node test runner, existing managed certificate bundle store.

---

## File Map

### Backend / Control Plane

- Modify: `panel/backend/server.js`
  - Bootstrap and persist the singleton global Relay CA on startup
  - Add helpers for auto-issued Relay listener certificates
  - Derive listener trust material (`pin_set`, `trusted_ca_certificate_ids`, `tls_mode`)
  - Block deletion of the system Relay CA and referenced Relay listeners
- Modify: `panel/backend/relay-listener-normalize.js`
  - Allow server-side auto population before final normalization
  - Keep runtime-facing validation strict on saved listeners
- Modify: `panel/backend/storage-prisma-core.js`
  - Persist any new Relay listener / managed certificate metadata fields if the implementation chooses explicit `auto/custom` markers
- Modify: `panel/backend/prisma/schema.prisma`
  - Add optional metadata columns only if the implementation needs explicit source tracking
- Optional modify: `panel/backend/prisma/migrations/*`
  - Add migration for any new metadata columns

### Backend Tests

- Modify: `panel/backend/tests/relay-version-api.test.js`
  - Cover bootstrap Relay CA visibility
  - Cover auto-issued Relay listener certificate creation
  - Cover automatic `pin_and_ca` derivation
  - Cover delete protection for referenced listeners / system Relay CA
- Modify: `panel/backend/tests/go-agent-heartbeat.test.js`
  - Verify sync payload ships the derived listener trust material and global Relay CA material correctly
- Modify: `panel/backend/tests/helpers.js`
  - Add helper PEM fixtures or helper constructors if needed

### Frontend

- Modify: `panel/frontend/src/utils/certificateTemplates.js`
  - Remove ordinary-user “Relay CA certificate” creation path
  - Make Relay listener certificates default to control-plane auto issuance
- Modify: `panel/frontend/src/components/CertificateForm.vue`
  - Present global Relay CA as system-managed
  - Keep manual upload for advanced paths
- Modify: `panel/frontend/src/pages/CertsPage.vue`
  - Surface the singleton Relay CA card and system-managed labels
- Modify: `panel/frontend/src/components/RelayListenerForm.vue`
  - Default to “自动签发（Relay CA）” and “自动（Relay CA + Pin）”
  - Hide raw Pin/CA in the default flow
  - Allow advanced override editing
- Modify: `panel/frontend/src/pages/RelayListenersPage.vue`
  - Warn that editing a shared listener affects every referencing rule
  - Show automatic trust summaries
- Modify: `panel/frontend/src/api/index.js`
  - Update mock certificates and listeners to match the new control-plane behavior

### Go Agent / Runtime Verification

- Modify: `go-agent/internal/relay/runtime_test.go`
  - Preserve coverage that `pin_and_ca` succeeds with both derived materials
- Modify: `go-agent/internal/app/local_runtime_test.go`
  - Verify HTTP/L4 relay snapshots using auto-derived listener fields still apply cleanly
- Optional modify: `go-agent/internal/certs/manager_test.go`
  - Verify `relay_ca` material remains trust-only and is not selected as a server cert

### Verification

- Run: `cd panel/backend && node --test tests/relay-version-api.test.js`
- Run: `cd panel/backend && node --test tests/go-agent-heartbeat.test.js`
- Run: `cd panel/backend && npm test`
- Run: `node --check panel/backend/server.js`
- Run: `cd panel/frontend && npm run build`
- Run: `cd go-agent && go test ./internal/relay ./internal/app ./internal/certs`

---

### Task 1: Add failing backend regressions for Relay CA bootstrap and auto-issued Relay listeners

**Files:**
- Modify: `panel/backend/tests/relay-version-api.test.js`
- Modify: `panel/backend/tests/go-agent-heartbeat.test.js`
- Inspect: `panel/backend/tests/helpers.js`

- [ ] **Step 1: Add a failing control-plane bootstrap test for the singleton Relay CA**

Append a new case near the certificate API coverage in `panel/backend/tests/relay-version-api.test.js`:

```js
  it("bootstraps a singleton global relay ca on startup", async () => {
    await withBackendServer(
      {
        env: { PANEL_ROLE: "master" },
      },
      async ({ baseUrl }) => {
        const response = await fetch(`${baseUrl}/api/certificates`);
        assert.equal(response.status, 200);
        const payload = await response.json();

        const relayCAs = payload.certificates.filter((cert) => cert.usage === "relay_ca");
        assert.equal(relayCAs.length, 1);
        assert.equal(relayCAs[0].certificate_type, "internal_ca");
        assert.equal(relayCAs[0].enabled, true);
      },
    );
  });
```

- [ ] **Step 2: Run the focused Relay API test to verify it fails**

Run:

```bash
cd panel/backend && node --test tests/relay-version-api.test.js
```

Expected: FAIL because the server currently does not auto-create a `relay_ca` certificate record on startup.

- [ ] **Step 3: Add a failing Relay listener create test for auto-issued certificates and auto trust derivation**

In the same file, add:

```js
  it("creates relay listeners with an auto-issued relay certificate and derived trust material", async () => {
    await withBackendServer(
      {
        env: { PANEL_ROLE: "master" },
        agents: [
          {
            id: "edge-1",
            name: "edge-1",
            agent_token: "token-edge-1",
            desired_revision: 1,
            current_revision: 1,
            capabilities: ["cert_install", "http_rules", "l4"],
            created_at: "2026-04-01T00:00:00.000Z",
            updated_at: "2026-04-01T00:00:00.000Z",
          },
        ],
      },
      async ({ baseUrl }) => {
        const response = await jsonRequest(baseUrl, "POST", "/api/agents/edge-1/relay-listeners", {
          name: "relay-a",
          listen_host: "relay-a.example.com",
          listen_port: 7443,
          enabled: true,
          certificate_source: "auto_relay_ca",
          trust_mode_source: "auto",
        });

        assert.equal(response.status, 201);
        assert.ok(Number.isInteger(response.body.listener.certificate_id));
        assert.equal(response.body.listener.tls_mode, "pin_and_ca");
        assert.equal(response.body.listener.pin_set.length, 1);
        assert.ok(response.body.listener.trusted_ca_certificate_ids.length >= 1);
      },
    );
  });
```

- [ ] **Step 4: Add a failing sync test that proves the agent receives the auto-derived fields**

Extend `panel/backend/tests/go-agent-heartbeat.test.js` with:

```js
  it("syncs auto-derived relay trust material for relay-chain listeners", async () => {
    await withBackendServer(
      {
        env: { PANEL_ROLE: "master" },
        agents: [
          {
            id: "remote-agent-a",
            name: "remote-agent-a",
            agent_token: "token-remote-agent-a",
            desired_revision: 3,
            current_revision: 1,
            capabilities: ["http_rules", "cert_install", "l4"],
            created_at: "2026-04-01T00:00:00.000Z",
            updated_at: "2026-04-01T00:00:00.000Z",
          },
        ],
      },
      async ({ baseUrl }) => {
        await fetch(`${baseUrl}/api/agents/remote-agent-a/relay-listeners`, {
          method: "POST",
          headers: { "content-type": "application/json" },
          body: JSON.stringify({
            name: "relay-a",
            listen_host: "relay-a.example.com",
            listen_port: 7443,
            enabled: true,
            certificate_source: "auto_relay_ca",
            trust_mode_source: "auto",
          }),
        });

        const heartbeat = await fetch(`${baseUrl}/api/agents/heartbeat`, {
          method: "POST",
          headers: {
            "content-type": "application/json",
            "x-agent-token": "token-remote-agent-a",
          },
          body: JSON.stringify({ name: "remote-agent-a", current_revision: 1 }),
        });

        assert.equal(heartbeat.status, 200);
        const payload = await heartbeat.json();
        assert.equal(payload.sync.relay_listeners[0].tls_mode, "pin_and_ca");
        assert.equal(payload.sync.relay_listeners[0].pin_set.length, 1);
        assert.ok(payload.sync.relay_listeners[0].trusted_ca_certificate_ids.length >= 1);
      },
    );
  });
```

- [ ] **Step 5: Commit the red backend tests**

```bash
git add panel/backend/tests/relay-version-api.test.js panel/backend/tests/go-agent-heartbeat.test.js
git commit -m "test(backend): cover relay ca auto trust flow"
```

---

### Task 2: Bootstrap the global Relay CA in the control plane

**Files:**
- Modify: `panel/backend/server.js`
- Optional modify: `panel/backend/storage-prisma-core.js`
- Optional modify: `panel/backend/prisma/schema.prisma`

- [ ] **Step 1: Add a focused helper layer for locating or creating the Relay CA**

Insert helper code near the managed certificate helpers in `panel/backend/server.js`:

```js
const RELAY_CA_DOMAIN = "__relay-ca.internal";
const RELAY_CA_TAG = "system:relay-ca";

function findGlobalRelayCA() {
  return storage
    .loadManagedCertificates()
    .find((cert) =>
      cert.usage === "relay_ca" &&
      cert.certificate_type === "internal_ca" &&
      Array.isArray(cert.tags) &&
      cert.tags.includes(RELAY_CA_TAG)
    ) || null;
}

function buildGlobalRelayCAPayload(nextId) {
  return normalizeManagedCertificatePayload({
    id: nextId,
    domain: RELAY_CA_DOMAIN,
    enabled: true,
    scope: "domain",
    issuer_mode: "local_http01",
    usage: "relay_ca",
    certificate_type: "internal_ca",
    self_signed: true,
    target_agent_ids: [LOCAL_AGENT_ID],
    tags: [RELAY_CA_TAG, "system"],
  }, {}, nextId);
}
```

- [ ] **Step 2: Add startup bootstrap logic**

Call a new helper during server initialization after `ensureDataDir()` and before request handling starts:

```js
async function ensureGlobalRelayCA() {
  const existing = findGlobalRelayCA();
  if (existing) {
    return existing;
  }

  const certs = storage.loadManagedCertificates();
  const nextId = certs.reduce((max, cert) => Math.max(max, Number(cert.id) || 0), 0) + 1;
  const prepared = prepareManagedCertificateForSave(null, buildGlobalRelayCAPayload(nextId));
  prepared.revision = storage.getNextGlobalRevision();
  certs.push(prepared);
  storage.saveManagedCertificates(certs);
  return syncStaticLocalCertificateById(prepared.id, { bumpRevision: false });
}
```

And in the bootstrap path:

```js
ensureDataDir();
await ensureGlobalRelayCA();
```

- [ ] **Step 3: Protect the system Relay CA from ordinary deletion**

Add a guard in both certificate delete handlers in `panel/backend/server.js`:

```js
function assertCertificateIsNotSystemRelayCA(cert) {
  if (cert && cert.usage === "relay_ca" && Array.isArray(cert.tags) && cert.tags.includes(RELAY_CA_TAG)) {
    throw new Error("system relay ca cannot be deleted");
  }
}
```

Use it before `certs.splice(...)` in both `/api/agents/:agentId/certificates/:id` and `/api/certificates/:id` delete paths.

- [ ] **Step 4: Run the focused Relay API test and verify the bootstrap case passes**

Run:

```bash
cd panel/backend && node --test tests/relay-version-api.test.js
```

Expected: the new “bootstraps a singleton global relay ca on startup” test passes, while the auto-issued listener test still fails.

- [ ] **Step 5: Commit the Relay CA bootstrap**

```bash
git add panel/backend/server.js panel/backend/storage-prisma-core.js panel/backend/prisma/schema.prisma panel/backend/tests/relay-version-api.test.js
git commit -m "feat(backend): bootstrap global relay ca"
```

If no schema/storage changes were needed, omit those files from `git add`.

---

### Task 3: Auto-issue Relay listener certificates and derive CA+Pin trust material

**Files:**
- Modify: `panel/backend/server.js`
- Modify: `panel/backend/relay-listener-normalize.js`
- Modify: `panel/backend/tests/relay-version-api.test.js`
- Modify: `panel/backend/tests/go-agent-heartbeat.test.js`

- [ ] **Step 1: Add a failing implementation test for delete protection on referenced listeners**

Append this to `panel/backend/tests/relay-version-api.test.js`:

```js
  it("blocks deleting relay listeners that are still referenced by a rule", async () => {
    await withBackendServer(
      {
        env: { PANEL_ROLE: "master" },
        agents: [
          {
            id: "edge-1",
            name: "edge-1",
            agent_token: "token-edge-1",
            desired_revision: 1,
            current_revision: 1,
            capabilities: ["http_rules", "l4", "cert_install"],
            created_at: "2026-04-01T00:00:00.000Z",
            updated_at: "2026-04-01T00:00:00.000Z",
          },
        ],
      },
      async ({ baseUrl }) => {
        const created = await jsonRequest(baseUrl, "POST", "/api/agents/edge-1/relay-listeners", {
          name: "relay-shared",
          listen_host: "relay-shared.example.com",
          listen_port: 9443,
          enabled: true,
          certificate_source: "auto_relay_ca",
          trust_mode_source: "auto",
        });

        await jsonRequest(baseUrl, "POST", "/api/rules", {
          frontend_url: "https://edge.example.com",
          backend_url: "http://127.0.0.1:8096",
          relay_chain: [created.body.listener.id],
        });

        const deleted = await fetch(`${baseUrl}/api/agents/edge-1/relay-listeners/${created.body.listener.id}`, {
          method: "DELETE",
        });

        assert.equal(deleted.status, 400);
      },
    );
  });
```

- [ ] **Step 2: Teach the relay listener create/update path to support auto-issued mode**

Add helper functions in `panel/backend/server.js`:

```js
function relayListenerAutoCertificateDomain(listener, agentId) {
  return `${listener.name}.${agentId}.relay.internal`;
}

async function ensureRelayListenerCertificate(agentId, listener, previousListener = null) {
  if (listener.certificate_id != null) {
    return listener.certificate_id;
  }

  const certs = storage.loadManagedCertificates();
  const nextId = certs.reduce((max, cert) => Math.max(max, Number(cert.id) || 0), 0) + 1;
  const payload = normalizeManagedCertificatePayload({
    id: nextId,
    domain: relayListenerAutoCertificateDomain(listener, agentId),
    enabled: true,
    scope: "domain",
    issuer_mode: "local_http01",
    usage: "relay_tunnel",
    certificate_type: "internal_ca",
    self_signed: false,
    target_agent_ids: [agentId],
    tags: ["relay", `listener:${listener.id}`],
  }, {}, nextId);
  const prepared = prepareManagedCertificateForSave(null, payload);
  prepared.revision = storage.getNextGlobalRevision();
  certs.push(prepared);
  storage.saveManagedCertificates(certs);
  const issued = await syncStaticLocalCertificateById(prepared.id, { bumpRevision: false, agentId });
  return issued.id;
}
```

- [ ] **Step 3: Derive `pin_set`, `trusted_ca_certificate_ids`, and `tls_mode` from the listener certificate**

Add helpers in `panel/backend/server.js`:

```js
function deriveRelayPinSetFromCertificate(certPEM) {
  const leaf = new crypto.X509Certificate(certPEM);
  const spkiDer = leaf.publicKey.export({ type: "spki", format: "der" });
  return [{
    type: "spki_sha256",
    value: crypto.createHash("sha256").update(spkiDer).digest("base64"),
  }];
}

function deriveRelayTrustMaterial(listener, certificate) {
  const certDir = getManagedCertificateDir(certificate.domain);
  const certPEM = fs.readFileSync(path.join(certDir, "cert"), "utf8");
  return {
    tls_mode: "pin_and_ca",
    pin_set: deriveRelayPinSetFromCertificate(certPEM),
    trusted_ca_certificate_ids: findGlobalRelayCA() ? [findGlobalRelayCA().id] : [],
    allow_self_signed: true,
  };
}
```

Then in the Relay listener POST/PUT handlers, replace direct normalization with a two-phase prepare/save flow:

```js
const draftListener = normalizeRelayListenerPayload({
  ...body,
  id: getNextRelayListenerId(),
  agent_id: agentId,
  certificate_id: body.certificate_id ?? null,
  pin_set: body.pin_set ?? [{ type: "spki_sha256", value: "placeholder" }],
  trusted_ca_certificate_ids: body.trusted_ca_certificate_ids ?? [findGlobalRelayCA().id],
});

draftListener.certificate_id = await ensureRelayListenerCertificate(agentId, draftListener);
Object.assign(draftListener, deriveRelayTrustMaterial(draftListener, getManagedCertificateById(draftListener.certificate_id)));

const nextListener = normalizeRelayListenerPayload(draftListener);
```

- [ ] **Step 4: Preserve advanced overrides while still supporting strict saved-state validation**

Relax only the pre-save draft path, not the final saved listener validation. In `panel/backend/relay-listener-normalize.js`, add a helper:

```js
function normalizeRelayListenerDraft(payload) {
  const normalized = normalizeRelayListenerPayload({
    ...payload,
    pin_set: Array.isArray(payload.pin_set) ? payload.pin_set : [{ type: "spki_sha256", value: "__draft__" }],
    trusted_ca_certificate_ids: Array.isArray(payload.trusted_ca_certificate_ids) ? payload.trusted_ca_certificate_ids : [1],
  });
  if (payload.pin_set == null) normalized.pin_set = [];
  if (payload.trusted_ca_certificate_ids == null) normalized.trusted_ca_certificate_ids = [];
  return normalized;
}
```

Use `normalizeRelayListenerDraft` only during server-side preparation before auto-derivation, then persist with the original strict `normalizeRelayListenerPayload`.

- [ ] **Step 5: Re-run the focused backend tests and verify green**

Run:

```bash
cd panel/backend && node --test tests/relay-version-api.test.js
cd panel/backend && node --test tests/go-agent-heartbeat.test.js
```

Expected: PASS for Relay CA bootstrap, auto-issued listener certs, derived `pin_and_ca`, heartbeat sync, and delete protection.

- [ ] **Step 6: Commit the auto-issuance and trust derivation**

```bash
git add panel/backend/server.js panel/backend/relay-listener-normalize.js panel/backend/tests/relay-version-api.test.js panel/backend/tests/go-agent-heartbeat.test.js
git commit -m "feat(backend): auto-issue relay listener trust material"
```

---

### Task 4: Update certificate management UI for the system Relay CA and auto-issued listener certs

**Files:**
- Modify: `panel/frontend/src/utils/certificateTemplates.js`
- Modify: `panel/frontend/src/components/CertificateForm.vue`
- Modify: `panel/frontend/src/pages/CertsPage.vue`
- Modify: `panel/frontend/src/api/index.js`

- [ ] **Step 1: Make the mock certificate data reflect a system-managed Relay CA**

Update `panel/frontend/src/api/index.js` so mock certificates include a singleton Relay CA:

```js
    {
      id: 2,
      domain: '__relay-ca.internal',
      enabled: true,
      scope: 'domain',
      issuer_mode: 'local_http01',
      usage: 'relay_ca',
      certificate_type: 'internal_ca',
      self_signed: true,
      status: 'active',
      last_issue_at: new Date().toISOString(),
      last_error: '',
      tags: ['system', 'system:relay-ca']
    }
```

- [ ] **Step 2: Run the frontend build to verify the baseline still passes before UI edits**

Run:

```bash
cd panel/frontend && npm run build
```

Expected: PASS.

- [ ] **Step 3: Remove ordinary-user Relay CA creation from templates and default Relay listener certificates to auto issuance**

Update `panel/frontend/src/utils/certificateTemplates.js`:

```js
export const CERTIFICATE_TEMPLATES = [
  {
    id: 'https',
    label: '网站 HTTPS',
    description: '为站点入口生成或绑定 HTTPS 证书',
    defaults: {
      issuer_mode: 'master_cf_dns',
      usage: 'https',
      certificate_type: 'acme',
      self_signed: false,
      scope: 'domain'
    }
  },
  {
    id: 'relay_tunnel',
    label: 'Relay 监听证书',
    description: '默认由系统 Relay CA 自动签发并分发',
    defaults: {
      issuer_mode: 'local_http01',
      usage: 'relay_tunnel',
      certificate_type: 'internal_ca',
      self_signed: false,
      scope: 'domain'
    }
  },
  {
    id: 'uploaded',
    label: '手动上传证书',
    description: '直接粘贴 PEM 证书与私钥',
    defaults: {
      issuer_mode: 'local_http01',
      usage: 'relay_tunnel',
      certificate_type: 'uploaded',
      self_signed: true,
      scope: 'domain'
    }
  }
]
```

- [ ] **Step 4: Surface system-managed Relay CA state in the certificate page and form**

In `panel/frontend/src/pages/CertsPage.vue`, add labels:

```vue
<span v-if="cert.tags?.includes('system:relay-ca')" class="tag tag--info">系统 Relay CA</span>
<span v-if="cert.certificate_type === 'internal_ca' && cert.usage === 'relay_tunnel'" class="cert-card__issuer">系统自动签发</span>
```

In `panel/frontend/src/components/CertificateForm.vue`, add a system banner:

```vue
<div v-if="form.usage === 'relay_tunnel' && form.certificate_type === 'internal_ca'" class="cert-banner cert-banner--info">
  Relay 监听证书默认由控制面使用全局 Relay CA 自动签发。
</div>
```

- [ ] **Step 5: Re-run the frontend build and verify it passes**

Run:

```bash
cd panel/frontend && npm run build
```

Expected: PASS with the new template semantics and Relay CA card labels.

- [ ] **Step 6: Commit the certificate UI changes**

```bash
git add panel/frontend/src/utils/certificateTemplates.js panel/frontend/src/components/CertificateForm.vue panel/frontend/src/pages/CertsPage.vue panel/frontend/src/api/index.js
git commit -m "feat(panel): present relay ca system certificate flow"
```

---

### Task 5: Simplify the Relay listener UI around auto-issued certs and shared-listener warnings

**Files:**
- Modify: `panel/frontend/src/components/RelayListenerForm.vue`
- Modify: `panel/frontend/src/pages/RelayListenersPage.vue`
- Modify: `panel/frontend/src/api/index.js`

- [ ] **Step 1: Add mock Relay listener payloads that look like auto-derived listeners**

Update `panel/frontend/src/api/index.js`:

```js
    {
      id: 1,
      name: 'relay-a',
      listen_host: 'relay-a.example.com',
      listen_port: 7443,
      enabled: true,
      certificate_id: 2,
      tls_mode: 'pin_and_ca',
      pin_set: [{ type: 'spki_sha256', value: 'derived-pin-a' }],
      trusted_ca_certificate_ids: [2],
      allow_self_signed: true,
      tags: ['relay', 'shared']
    }
```

- [ ] **Step 2: Run the frontend build to capture the red/incomplete state**

Run:

```bash
cd panel/frontend && npm run build
```

Expected: PASS before the UI rewrite, giving a clean baseline.

- [ ] **Step 3: Rework the default Relay listener form around “auto issued / auto trust”**

In `panel/frontend/src/components/RelayListenerForm.vue`, set the default state:

```js
function createDefaultForm() {
  return {
    name: '',
    listen_host: '0.0.0.0',
    listen_port: 0,
    enabled: true,
    certificate_id: null,
    certificate_source: 'auto_relay_ca',
    trust_mode_source: 'auto',
    tls_mode: 'pin_and_ca',
    pin_set: [],
    trusted_ca_certificate_ids: [],
    allow_self_signed: true,
    tags: []
  }
}
```

And change the default template:

```vue
<div class='form-group'>
  <label class='form-label'>监听证书来源</label>
  <select v-model='form.certificate_source' class='input'>
    <option value='auto_relay_ca'>自动签发（Relay CA）</option>
    <option value='existing_certificate'>绑定已有证书</option>
  </select>
</div>

<div class='form-group'>
  <label class='form-label'>信任策略</label>
  <select v-model='form.trust_mode_source' class='input'>
    <option value='auto'>自动（Relay CA + Pin）</option>
    <option value='custom'>高级自定义</option>
  </select>
</div>
```

- [ ] **Step 4: Keep advanced override editing available and explicit**

Use the advanced panel only when `form.trust_mode_source === 'custom' || showAdvanced`:

```vue
<section v-if="form.trust_mode_source === 'custom' || showAdvanced" class="advanced-panel">
  <label class='form-label'>TLS 模式</label>
  <select v-model='form.tls_mode' class='input'>
    <option value='pin_and_ca'>Pin + CA</option>
    <option value='pin_only'>仅证书 Pin</option>
    <option value='ca_only'>仅 CA 信任链</option>
    <option value='pin_or_ca'>证书 Pin 或 CA</option>
  </select>
</section>
```

Submit the new intent fields:

```js
const payload = {
  ...,
  certificate_source: form.value.certificate_source,
  trust_mode_source: form.value.trust_mode_source,
  pin_set: parsePinSetRows(),
  trusted_ca_certificate_ids: [...trustedCaSet.value].map((id) => Number(id))
}
```

- [ ] **Step 5: Add shared-listener warnings on the Relay listeners page**

In `panel/frontend/src/pages/RelayListenersPage.vue`, add:

```vue
<p class='relay-page__subtitle'>{{ listeners.length }} 个监听器 · 默认自动签发证书 · 自动 Relay CA + Pin 信任</p>
```

And in the delete modal:

```vue
<p class='relay-page__warning'>若该监听器已被规则引用，删除会被阻止。</p>
```

- [ ] **Step 6: Re-run the frontend build and verify green**

Run:

```bash
cd panel/frontend && npm run build
```

Expected: PASS with the simplified auto-issued listener flow and advanced override path intact.

- [ ] **Step 7: Commit the Relay listener UI changes**

```bash
git add panel/frontend/src/components/RelayListenerForm.vue panel/frontend/src/pages/RelayListenersPage.vue panel/frontend/src/api/index.js
git commit -m "feat(panel): default relay listeners to auto trust"
```

---

### Task 6: Add runtime compatibility regressions and run full verification

**Files:**
- Modify: `go-agent/internal/relay/runtime_test.go`
- Modify: `go-agent/internal/app/local_runtime_test.go`
- Optional modify: `go-agent/internal/certs/manager_test.go`

- [ ] **Step 1: Add a focused relay runtime regression for derived `pin_and_ca`**

In `go-agent/internal/relay/runtime_test.go`, add:

```go
func TestPinAndCAVerificationWorksWithDerivedRelayMaterial(t *testing.T) {
	backendAddr, stopBackend := startTCPEchoServer(t)
	defer stopBackend()

	provider := newFakeTLSMaterialProvider()
	listener, hop := newRelayEndpoint(t, provider, 1, "relay-auto", "pin_and_ca", true, true)

	server, err := Start(context.Background(), []Listener{listener}, provider)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	defer server.Close()

	conn, err := Dial(context.Background(), "tcp", backendAddr, []Hop{hop}, provider)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer conn.Close()

	assertRoundTrip(t, conn, []byte("auto-derived"))
}
```

- [ ] **Step 2: Add an app-level regression proving relay snapshots still apply cleanly**

In `go-agent/internal/app/local_runtime_test.go`, add:

```go
func TestApplyRelayListenersAcceptsAutoDerivedPinAndCA(t *testing.T) {
	ctx := context.Background()
	manager, provider := newTestRelayRuntimeManager(t)

	certificateID := 41
	listener := runtimeTestRelayListener(pickFreeTCPPort(t), certificateID)
	listener.TLSMode = "pin_and_ca"
	listener.PinSet = []model.RelayPin{{Type: "spki_sha256", Value: "derived"}}
	listener.TrustedCACertificateIDs = []int{7}
	listener.AllowSelfSigned = true

	if err := manager.Apply(ctx, []model.RelayListener{listener}); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}

	if _, err := provider.ServerCertificate(ctx, certificateID); err != nil {
		t.Fatalf("expected certificate to remain resolvable: %v", err)
	}
}
```

- [ ] **Step 3: Run the focused Go tests to verify the runtime contract still holds**

Run:

```bash
cd go-agent && go test ./internal/relay -run TestPinAndCAVerificationWorksWithDerivedRelayMaterial -v
cd go-agent && go test ./internal/app -run TestApplyRelayListenersAcceptsAutoDerivedPinAndCA -v
```

Expected: PASS once the control-plane-generated listener fields remain compatible with the current Go runtime.

- [ ] **Step 4: Run the full cross-layer verification**

Run:

```bash
cd panel/backend && npm test
node --check panel/backend/server.js
cd ../frontend && npm run build
cd ..\\go-agent && go test ./internal/relay ./internal/app ./internal/certs
```

Expected:

```text
# backend tests pass
# frontend build succeeds
# go test exits 0
```

- [ ] **Step 5: Commit the runtime verification updates**

```bash
git add go-agent/internal/relay/runtime_test.go go-agent/internal/app/local_runtime_test.go go-agent/internal/certs/manager_test.go
git commit -m "test(go-agent): verify relay auto trust compatibility"
```

If `manager_test.go` was not modified, omit it from `git add`.

---

## Self-Review Checklist

- Spec coverage:
  - Global Relay CA bootstrap: Task 1 + Task 2
  - Default auto-issued Relay listener certificates: Task 1 + Task 3 + Task 5
  - Automatic CA + Pin trust derivation: Task 1 + Task 3 + Task 6
  - No agent-side signing: Task 2 + Task 6
  - Shared listener behavior and delete protection: Task 3 + Task 5
  - Unified certificate UI updates: Task 4
  - Advanced override preservation: Task 3 + Task 5
- Placeholder scan:
  - No `TODO`, `TBD`, or “implement later”
  - Each task lists exact files, commands, and code snippets
  - Commands specify expected outcomes
- Type consistency:
  - Relay listener runtime fields stay `certificate_id`, `tls_mode`, `pin_set`, `trusted_ca_certificate_ids`, `allow_self_signed`
  - New intent fields are consistently named `certificate_source` and `trust_mode_source`
  - Relay CA sentinel markers are consistently `usage === "relay_ca"` and `tags.includes("system:relay-ca")`
