# Unified Certificate Management & Relay TLS UX Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver “统一证书管理” and the simplified Relay TLS flow: add manual PEM upload, certificate purpose templates, and a default-auto Relay trust UI without breaking existing certificate/Relay semantics.

**Architecture:** Keep the current managed-certificate and Relay data models, but layer a friendlier frontend on top and teach the backend to ingest uploaded PEM material into the existing filesystem-backed certificate bundle store. Use frontend-side mapping for “自动信任策略”, while backend/API behavior remains compatible with `tls_mode`, `pin_set`, `trusted_ca_certificate_ids`, and the existing certificate sync pipeline.

**Tech Stack:** Vue 3 + Vite SPA, Node/CommonJS backend, existing JSON/SQLite/Prisma storage adapters, Go agent compatibility via current managed certificate bundle/policy files.

---

## File Map

### Frontend

- Modify: `panel/frontend/src/pages/CertsPage.vue`
  - Rename page copy to “统一证书管理”
  - Add template-first creation entry copy
  - Surface source/usage/self-signed metadata on cards
- Modify: `panel/frontend/src/components/CertificateForm.vue`
  - Add purpose template selector
  - Add manual PEM upload fields
  - Drive template defaults and validation
- Modify: `panel/frontend/src/pages/RelayListenersPage.vue`
  - Update list copy to match simplified trust semantics
- Modify: `panel/frontend/src/components/RelayListenerForm.vue`
  - Default-simple mode: listener cert + trust strategy
  - Advanced mode: expose raw TLS fields
  - Auto-map trust strategy to existing payload
- Modify: `panel/frontend/src/api/index.js`
  - Extend mock certificate payloads with uploaded PEM fields and new metadata labels
  - Keep Relay mock payloads compatible with `pin_and_ca`
- Modify: `panel/frontend/src/router/index.js`
  - Change route title to “统一证书管理”
- Optional create: `panel/frontend/src/utils/certificateTemplates.js`
  - Centralize template defaults and label helpers if `CertificateForm.vue` becomes too large

### Backend

- Modify: `panel/backend/server.js`
  - Normalize uploaded PEM payloads
  - Validate uploaded material
  - Persist uploaded certificate material into the existing `managed_certificates/<domain>/{cert,key}` store
  - Auto-sync uploaded local certificates after create/update when material is present
- Modify: `panel/backend/tests/go-agent-heartbeat.test.js`
  - Add API coverage for uploaded PEM create/update/sync behavior
- Modify: `panel/backend/tests/relay-version-api.test.js`
  - Add Relay listener API validation coverage for `pin_and_ca` and enabled listener certificate requirements if backend validation is tightened here
- Optional modify: `panel/backend/relay-listener-normalize.js`
  - If needed, teach backend payload normalization to validate the full TLS mode matrix consistently with the Go agent

### Verification

- Run: `cd panel/backend && npm test`
- Run: `node --check panel/backend/server.js`
- Run: `cd panel/frontend && npm run build`

---

### Task 1: Add backend regression tests for uploaded PEM certificates

**Files:**
- Modify: `panel/backend/tests/go-agent-heartbeat.test.js`
- Modify: `panel/backend/tests/relay-version-api.test.js`
- Inspect: `panel/backend/tests/helpers.js`

- [ ] **Step 1: Write the failing backend test for uploaded PEM creation**

Add a new API test next to the existing certificate API coverage in `panel/backend/tests/go-agent-heartbeat.test.js`:

```js
  it("creates uploaded relay certificates from PEM input and immediately syncs material", async () => {
    await withBackendServer(
      {
        env: { PANEL_ROLE: "master" },
        agents: [
          {
            id: "remote-agent-relay",
            name: "remote-agent-relay",
            agent_token: "token-remote-agent-relay",
            desired_revision: 1,
            current_revision: 1,
            capabilities: ["cert_install"],
            created_at: "2026-04-01T00:00:00.000Z",
            updated_at: "2026-04-01T00:00:00.000Z",
          },
        ],
      },
      async ({ baseUrl, dataRoot }) => {
        const response = await fetch(`${baseUrl}/api/agents/remote-agent-relay/certificates`, {
          method: "POST",
          headers: { "content-type": "application/json" },
          body: JSON.stringify({
            domain: "relay-uploaded.example.com",
            enabled: true,
            scope: "domain",
            issuer_mode: "local_http01",
            usage: "relay_tunnel",
            certificate_type: "uploaded",
            self_signed: true,
            certificate_pem: TEST_SERVER_CERT_PEM,
            private_key_pem: TEST_SERVER_KEY_PEM,
            ca_pem: TEST_CA_CHAIN_PEM,
          }),
        });

        assert.equal(response.status, 201);
        const payload = await response.json();
        assert.equal(payload.certificate.status, "active");
        assert.match(String(payload.certificate.material_hash || ""), /^[0-9a-f]{64}$/i);

        const certPath = path.join(dataRoot, "managed_certificates", "relay-uploaded.example.com", "cert");
        const keyPath = path.join(dataRoot, "managed_certificates", "relay-uploaded.example.com", "key");
        assert.match(await fs.promises.readFile(certPath, "utf8"), /BEGIN CERTIFICATE/);
        assert.match(await fs.promises.readFile(keyPath, "utf8"), /BEGIN .*PRIVATE KEY/);
      },
    );
  });
```

- [ ] **Step 2: Run the focused backend test to verify it fails**

Run:

```bash
cd panel/backend && node --test tests/go-agent-heartbeat.test.js
```

Expected: FAIL because `certificate_pem` / `private_key_pem` / `ca_pem` are ignored and the returned certificate remains `pending` or lacks stored material.

- [ ] **Step 3: Add a failing Relay listener API regression for the raw TLS mode matrix**

Append a focused API case in `panel/backend/tests/relay-version-api.test.js`:

```js
      const created = await jsonRequest(baseUrl, "POST", "/api/agents/edge-1/relay-listeners", {
        name: "relay-both",
        listen_host: "0.0.0.0",
        listen_port: 19443,
        enabled: true,
        certificate_id: 7,
        tls_mode: "pin_and_ca",
        pin_set: [{ type: "spki_sha256", value: "abc123" }],
        trusted_ca_certificate_ids: [42],
      });

      assert.equal(created.status, 201);
      assert.equal(created.body.listener.tls_mode, "pin_and_ca");
```

Also add the negative case:

```js
      const invalid = await jsonRequest(baseUrl, "POST", "/api/agents/edge-1/relay-listeners", {
        name: "relay-missing-cert",
        listen_host: "0.0.0.0",
        listen_port: 20443,
        enabled: true,
        tls_mode: "pin_only",
        pin_set: [{ type: "spki_sha256", value: "abc123" }],
      });

      assert.equal(invalid.status, 400);
      assert.match(invalid.body.message, /certificate_id is required|certificate/i);
```

- [ ] **Step 4: Run the focused Relay API test to verify it fails for the expected reason**

Run:

```bash
cd panel/backend && node --test tests/relay-version-api.test.js
```

Expected: FAIL because the backend currently does not guarantee the desired certificate-required behavior at API normalization time, and the new `pin_and_ca` coverage is not protected by the current frontend-only UX.

- [ ] **Step 5: Commit the red tests**

```bash
git add panel/backend/tests/go-agent-heartbeat.test.js panel/backend/tests/relay-version-api.test.js
git commit -m "test(backend): cover uploaded pem certificate flow"
```

---

### Task 2: Implement backend PEM ingestion and uploaded-certificate sync

**Files:**
- Modify: `panel/backend/server.js`
- Optional modify: `panel/backend/relay-listener-normalize.js`

- [ ] **Step 1: Add uploaded PEM normalization helpers in `server.js`**

Insert helper code near the managed certificate normalization helpers:

```js
function normalizeUploadedPEMField(value) {
  return value === undefined || value === null ? "" : String(value).trim();
}

function joinCertificatePEM(certificatePEM, caPEM) {
  const parts = [normalizeUploadedPEMField(certificatePEM), normalizeUploadedPEMField(caPEM)].filter(Boolean);
  return parts.join("\n");
}

function validateUploadedMaterial({ certificate_pem, private_key_pem, ca_pem, usage, certificate_type }) {
  if (certificate_type !== "uploaded") {
    return { certPEM: "", keyPEM: "" };
  }

  const certPEM = joinCertificatePEM(certificate_pem, ca_pem);
  const keyPEM = normalizeUploadedPEMField(private_key_pem);
  if (!certPEM) {
    throw new Error("certificate_pem is required for uploaded certificates");
  }
  if (!keyPEM) {
    throw new Error("private_key_pem is required for uploaded certificates");
  }

  tls.createSecureContext({ cert: certPEM, key: keyPEM });
  return { certPEM, keyPEM };
}
```

- [ ] **Step 2: Wire uploaded PEM fields through create/update payload handling**

Extend the create/update certificate handlers so they capture PEM material before saving:

```js
      const uploadMaterial = validateUploadedMaterial({
        certificate_pem: body.certificate_pem,
        private_key_pem: body.private_key_pem,
        ca_pem: body.ca_pem,
        usage: nextCert.usage,
        certificate_type: nextCert.certificate_type,
      });
```

Persist the metadata model exactly as today, then write filesystem material when `certificate_type === "uploaded"`:

```js
function writeManagedCertificateMaterial(domain, material) {
  const certDir = getCertStoreDir(domain);
  fs.mkdirSync(certDir, { recursive: true });
  fs.writeFileSync(path.join(certDir, "cert"), material.cert_pem, "utf8");
  fs.writeFileSync(path.join(certDir, "key"), material.key_pem, "utf8");
}
```

- [ ] **Step 3: Auto-sync uploaded local certificates after create/update**

After saving the cert, branch for uploaded local certificates:

```js
      if (preparedCert.enabled && preparedCert.issuer_mode === "local_http01" && preparedCert.certificate_type === "uploaded") {
        savedCert = await syncStaticLocalCertificateById(preparedCert.id, { agentId });
      }
```

On update, do the same after writing new material and saving the new revision.

- [ ] **Step 4: Tighten Relay listener normalization only as far as the existing runtime contract**

Update `panel/backend/relay-listener-normalize.js` if needed so API saves stay aligned with the Go runtime:

```js
const allowedTlsModes = new Set(["pin_only", "ca_only", "pin_or_ca", "pin_and_ca"]);
if (!allowedTlsModes.has(normalized.tls_mode)) {
  throw new TypeError("tls_mode must be pin_only, ca_only, pin_or_ca, or pin_and_ca");
}
if (normalized.enabled && normalized.certificate_id == null) {
  throw new TypeError("certificate_id is required when relay listener is enabled");
}
```

- [ ] **Step 5: Re-run the focused backend tests and verify green**

Run:

```bash
cd panel/backend && node --test tests/go-agent-heartbeat.test.js
cd panel/backend && node --test tests/relay-version-api.test.js
```

Expected: PASS for the new uploaded PEM flow and the Relay TLS mode coverage.

- [ ] **Step 6: Run the full backend suite**

Run:

```bash
cd panel/backend && npm test
node --check panel/backend/server.js
```

Expected:

```text
✔ go-agent-heartbeat.test.js
✔ relay-version-api.test.js
...
# exit code 0
```

- [ ] **Step 7: Commit backend implementation**

```bash
git add panel/backend/server.js panel/backend/relay-listener-normalize.js panel/backend/tests/go-agent-heartbeat.test.js panel/backend/tests/relay-version-api.test.js
git commit -m "feat(backend): support uploaded pem certificate management"
```

---

### Task 3: Build the “统一证书管理” frontend flow

**Files:**
- Modify: `panel/frontend/src/pages/CertsPage.vue`
- Modify: `panel/frontend/src/components/CertificateForm.vue`
- Modify: `panel/frontend/src/api/index.js`
- Modify: `panel/frontend/src/router/index.js`
- Optional create: `panel/frontend/src/utils/certificateTemplates.js`

- [ ] **Step 1: Write the frontend build-preserving red state by changing mock/API payload shape first**

Extend the mock cert shape in `panel/frontend/src/api/index.js`:

```js
    {
      id: 2,
      domain: 'relay-uploaded.example.com',
      enabled: true,
      scope: 'domain',
      issuer_mode: 'local_http01',
      usage: 'relay_tunnel',
      certificate_type: 'uploaded',
      self_signed: true,
      status: 'active',
      last_issue_at: new Date().toISOString(),
      last_error: '',
      tags: ['relay', 'uploaded']
    }
```

Add passthrough fields for create/update:

```js
export async function createCertificate(agentId, payload) {
  ...
  const item = { ...payload, id: ++mockCertIdCounter, status: payload.certificate_type === 'uploaded' ? 'active' : 'pending', last_issue_at: '', last_error: '' }
```

- [ ] **Step 2: Run the frontend build to capture the pre-implementation baseline**

Run:

```bash
cd panel/frontend && npm run build
```

Expected: PASS before structural UI edits.

- [ ] **Step 3: Replace the old certificate form with a template-first UI**

In `panel/frontend/src/components/CertificateForm.vue`, restructure the form state like this:

```js
const TEMPLATE_DEFAULTS = {
  https: { usage: 'https', certificate_type: 'acme', self_signed: false, scope: 'domain', issuer_mode: 'master_cf_dns' },
  relay_tunnel: { usage: 'relay_tunnel', certificate_type: 'uploaded', self_signed: true, scope: 'domain', issuer_mode: 'local_http01' },
  relay_ca: { usage: 'relay_ca', certificate_type: 'internal_ca', self_signed: true, scope: 'domain', issuer_mode: 'local_http01' },
  uploaded: { usage: 'relay_tunnel', certificate_type: 'uploaded', self_signed: true, scope: 'domain', issuer_mode: 'local_http01' },
}
```

Add new refs:

```js
const selectedTemplate = ref(props.initialData ? inferTemplate(props.initialData) : 'https')
const uploadedMaterial = ref({
  certificate_pem: props.initialData?.certificate_pem || '',
  private_key_pem: props.initialData?.private_key_pem || '',
  ca_pem: props.initialData?.ca_pem || ''
})
```

And submit:

```js
const payload = {
  ...form.value,
  domain: form.value.domain.trim(),
  certificate_pem: uploadedMaterial.value.certificate_pem.trim(),
  private_key_pem: uploadedMaterial.value.private_key_pem.trim(),
  ca_pem: uploadedMaterial.value.ca_pem.trim(),
}
```

- [ ] **Step 4: Rename and enrich the certificate page**

In `panel/frontend/src/pages/CertsPage.vue` and `panel/frontend/src/router/index.js`, change copy to “统一证书管理”:

```js
meta: { title: '统一证书管理' }
```

```vue
<h1 class="certs-page__title">统一证书管理</h1>
```

Add card metadata:

```vue
<span class="cert-card__scope">{{ usageLabel(cert.usage) }}</span>
<span class="cert-card__issuer">{{ sourceLabel(cert.certificate_type) }}</span>
<span v-if="cert.self_signed" class="tag tag--warn">自签</span>
```

- [ ] **Step 5: Re-run the frontend build and fix the new UI until green**

Run:

```bash
cd panel/frontend && npm run build
```

Expected: PASS with the new template picker, uploaded PEM fields, and renamed page title.

- [ ] **Step 6: Commit the certificate management UI**

```bash
git add panel/frontend/src/pages/CertsPage.vue panel/frontend/src/components/CertificateForm.vue panel/frontend/src/api/index.js panel/frontend/src/router/index.js panel/frontend/src/utils/certificateTemplates.js
git commit -m "feat(panel): add unified certificate management flow"
```

If no `certificateTemplates.js` file was created, omit it from `git add`.

---

### Task 4: Implement the simplified Relay trust strategy UI

**Files:**
- Modify: `panel/frontend/src/components/RelayListenerForm.vue`
- Modify: `panel/frontend/src/pages/RelayListenersPage.vue`
- Modify: `panel/frontend/src/api/index.js`

- [ ] **Step 1: Add a failing local build change by introducing trust-strategy state**

In `panel/frontend/src/components/RelayListenerForm.vue`, add the new UI state:

```js
const trustStrategy = ref(inferTrustStrategy(props.initialData))
const showAdvanced = ref(Boolean(props.initialData && isAdvancedTLSMode(props.initialData)))
```

Add mapping helpers:

```js
function inferTrustStrategy(listener) {
  if (!listener) return 'auto'
  if (listener.tls_mode === 'pin_only') return 'pin_only'
  if (listener.tls_mode === 'ca_only') return 'ca_only'
  return 'auto'
}

function resolveTLSPayload({ trustStrategy, pinSet, trustedCaIds, explicitTLSMode }) {
  if (showAdvanced.value) {
    return explicitTLSMode
  }
  if (trustStrategy === 'pin_only') return 'pin_only'
  if (trustStrategy === 'ca_only') return 'ca_only'
  if (pinSet.length && trustedCaIds.length) return 'pin_and_ca'
  if (pinSet.length) return 'pin_only'
  if (trustedCaIds.length) return 'ca_only'
  throw new Error('自动信任策略至少需要 Pin 或 CA 之一')
}
```

- [ ] **Step 2: Run the frontend build to verify the incomplete state fails**

Run:

```bash
cd panel/frontend && npm run build
```

Expected: FAIL until the template/render logic and submit wiring are fully updated.

- [ ] **Step 3: Finish the simplified Relay form UI**

Update the template so the default view shows:

```vue
<label class='form-label'>本监听器对外证书</label>
<label class='form-label'>信任策略</label>
<select v-model='trustStrategy' class='input'>
  <option value='auto'>自动（推荐）</option>
  <option value='pin_only'>仅信任指定证书</option>
  <option value='ca_only'>仅信任指定 CA</option>
</select>
<button type='button' class='btn btn-secondary' @click='showAdvanced = !showAdvanced'>高级 TLS 设置</button>
```

Move the raw fields into the advanced section, but keep them editable:

```vue
<section v-if='showAdvanced' class='advanced-panel'>
  <label class='form-label'>TLS 模式</label>
  <select v-model='form.tls_mode' class='input'>
    <option value='pin_or_ca'>证书 Pin 或 CA</option>
    <option value='pin_only'>仅证书 Pin</option>
    <option value='ca_only'>仅 CA 信任链</option>
    <option value='pin_and_ca'>Pin + CA</option>
  </select>
</section>
```

Submit using the mapper:

```js
const payload = {
  ...,
  tls_mode: resolveTLSPayload({
    trustStrategy: trustStrategy.value,
    pinSet: parsePinSetRows(),
    trustedCaIds: [...trustedCaSet.value],
    explicitTLSMode: form.value.tls_mode,
  }),
}
```

- [ ] **Step 4: Update Relay list copy to match the new semantics**

In `panel/frontend/src/pages/RelayListenersPage.vue`, replace the raw badge text:

```vue
<span class='badge'>{{ listener.certificate_id ? `证书 #${listener.certificate_id}` : '未绑定证书' }}</span>
<span class='badge'>{{ trustSummary(listener) }}</span>
```

with a helper:

```js
function trustSummary(listener) {
  if (listener.tls_mode === 'pin_and_ca') return '自动（Pin + CA）'
  if (listener.tls_mode === 'pin_only') return '仅 Pin'
  if (listener.tls_mode === 'ca_only') return '仅 CA'
  return '兼容模式'
}
```

- [ ] **Step 5: Run the frontend build again and keep fixing until green**

Run:

```bash
cd panel/frontend && npm run build
```

Expected: PASS with the simplified Relay form, advanced raw TLS section, and updated list badges.

- [ ] **Step 6: Run the full cross-layer verification**

Run:

```bash
cd panel/backend && npm test
node --check panel/backend/server.js
cd ../frontend && npm run build
```

Expected: all commands exit `0`.

- [ ] **Step 7: Commit the Relay UI implementation**

```bash
git add panel/frontend/src/components/RelayListenerForm.vue panel/frontend/src/pages/RelayListenersPage.vue panel/frontend/src/api/index.js
git commit -m "feat(panel): simplify relay tls configuration"
```

---

## Self-Review Checklist

- Spec coverage:
  - Manual PEM upload: Task 1 + Task 2 + Task 3
  - “统一证书管理” rename + purpose templates: Task 3
  - Simplified Relay trust flow: Task 4
  - Advanced TLS preservation: Task 4
  - Compatibility with existing backend/agent semantics: Task 2 + Task 4
- Placeholder scan:
  - No `TODO` / `TBD`
  - Each task has explicit files, commands, and concrete code snippets
- Type consistency:
  - Frontend payload fields use `certificate_pem`, `private_key_pem`, `ca_pem`
  - Relay payload still uses `tls_mode`, `pin_set`, `trusted_ca_certificate_ids`, `allow_self_signed`

