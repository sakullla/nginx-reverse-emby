"use strict";

const { describe, it } = require("node:test");
const assert = require("node:assert/strict");
const crypto = require("node:crypto");
const fsp = require("node:fs/promises");
const path = require("node:path");
const selfsigned = require("selfsigned");
const { TEST_SERVER_CERT_PEM, TEST_SERVER_KEY_PEM, withBackendServer } = require("./helpers");

async function jsonRequest(baseUrl, method, path, body) {
  const response = await fetch(`${baseUrl}${path}`, {
    method,
    headers: { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const payload = await response.json();
  return { status: response.status, payload };
}

function computeSpkiSha256(certPem) {
  const cert = new crypto.X509Certificate(certPem);
  const der = cert.publicKey.export({ type: "spki", format: "der" });
  return crypto.createHash("sha256").update(der).digest("base64");
}

async function generateRelaySignedIntermediateBundle(relayCAKeyPem, relayCACertPem) {
  const intermediate = await selfsigned.generate(
    [{ name: "commonName", value: "relay-intermediate.internal" }],
    {
      algorithm: "sha256",
      days: 825,
      ca: {
        key: relayCAKeyPem,
        cert: relayCACertPem,
      },
      extensions: [
        { name: "basicConstraints", cA: true },
        {
          name: "keyUsage",
          digitalSignature: true,
          keyCertSign: true,
          cRLSign: true,
        },
      ],
    },
  );
  const leaf = await selfsigned.generate(
    [{ name: "commonName", value: "relay-indirect.example.com" }],
    {
      algorithm: "sha256",
      days: 825,
      ca: {
        key: intermediate.private,
        cert: intermediate.cert,
      },
    },
  );
  return {
    certificatePem: `${leaf.cert}\n${intermediate.cert}`,
    privateKeyPem: leaf.private,
  };
}

async function generateUnrelatedIntermediateBundle() {
  const intermediate = await selfsigned.generate(
    [{ name: "commonName", value: "unrelated-intermediate.internal" }],
    {
      algorithm: "sha256",
      days: 825,
      extensions: [
        { name: "basicConstraints", cA: true },
        {
          name: "keyUsage",
          digitalSignature: true,
          keyCertSign: true,
          cRLSign: true,
        },
      ],
    },
  );
  const leaf = await selfsigned.generate(
    [{ name: "commonName", value: "relay-unrelated-chain.example.com" }],
    {
      algorithm: "sha256",
      days: 825,
      ca: {
        key: intermediate.private,
        cert: intermediate.cert,
      },
    },
  );
  return {
    certificatePem: `${leaf.cert}\n${intermediate.cert}`,
    privateKeyPem: leaf.private,
  };
}

describe("relay listeners and version policies API", () => {
  it("supports relay listener CRUD for registered agents", async () => {
    await withBackendServer(
      {
        agents: [
          {
            id: "edge-1",
            name: "Edge-1",
            agent_url: "http://edge-1:8080",
            agent_token: "token-edge-1",
            capabilities: ["http_rules", "l4"],
          },
        ],
        managedCertificates: [
          {
            id: 7,
            domain: "relay-cert.example.com",
            enabled: true,
            scope: "domain",
            issuer_mode: "local_http01",
            usage: "relay_tunnel",
            certificate_type: "uploaded",
            self_signed: true,
            target_agent_ids: ["edge-1"],
            status: "issued",
            revision: 1,
          },
        ],
      },
      async ({ baseUrl }) => {
        const created = await jsonRequest(baseUrl, "POST", "/api/agents/edge-1/relay-listeners", {
          name: "edge relay",
          listen_host: "0.0.0.0",
          listen_port: 10443,
          enabled: true,
          tls_mode: "pin_or_ca",
          certificate_id: 7,
          pin_set: [{ type: "spki_sha256", value: "abc" }],
          trusted_ca_certificate_ids: [],
          allow_self_signed: false,
          tags: ["relay"],
        });
        assert.equal(created.status, 201);
        assert.equal(typeof created.payload.listener.id, "number");
        assert.deepEqual(created.payload.listener.bind_hosts, ["0.0.0.0"]);
        assert.equal(created.payload.listener.public_host, "0.0.0.0");
        assert.equal(created.payload.listener.public_port, 10443);
        assert.equal(created.payload.listener.listen_host, "0.0.0.0");

        const list = await jsonRequest(baseUrl, "GET", "/api/agents/edge-1/relay-listeners");
        assert.equal(list.status, 200);
        assert.equal(list.payload.listeners.length, 1);
        assert.deepEqual(list.payload.listeners[0].bind_hosts, ["0.0.0.0"]);
        assert.equal(list.payload.listeners[0].public_host, "0.0.0.0");
        assert.equal(list.payload.listeners[0].public_port, 10443);
        assert.equal(list.payload.listeners[0].listen_host, "0.0.0.0");

        const listenerId = created.payload.listener.id;
        const updated = await jsonRequest(
          baseUrl,
          "PUT",
          `/api/agents/edge-1/relay-listeners/${listenerId}`,
          {
            name: "edge relay updated",
            pin_set: [{ type: "spki_sha256", value: "def" }],
            trusted_ca_certificate_ids: [2],
          },
        );
        assert.equal(updated.status, 200);
        assert.equal(updated.payload.listener.name, "edge relay updated");
        assert.deepEqual(updated.payload.listener.bind_hosts, ["0.0.0.0"]);
        assert.equal(updated.payload.listener.public_host, "0.0.0.0");
        assert.equal(updated.payload.listener.public_port, 10443);
        assert.equal(updated.payload.listener.listen_host, "0.0.0.0");

        const deleted = await jsonRequest(baseUrl, "DELETE", `/api/agents/edge-1/relay-listeners/${listenerId}`);
        assert.equal(deleted.status, 200);
      },
    );
  });

  it("bootstraps a usable singleton global relay ca on startup", async () => {
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
        assert.equal(relayCAs[0].scope, "domain");
        assert.equal(relayCAs[0].status, "active");
        assert.match(String(relayCAs[0].material_hash || ""), /^[0-9a-f]{64}$/i);
      },
    );
  });

  it("bootstraps a usable singleton global relay ca without a local master agent", async () => {
    await withBackendServer(
      {
        env: {
          PANEL_ROLE: "master",
          MASTER_LOCAL_AGENT_ENABLED: "0",
        },
      },
      async ({ baseUrl }) => {
        const response = await fetch(`${baseUrl}/api/certificates`);
        assert.equal(response.status, 200);
        const payload = await response.json();

        const relayCAs = payload.certificates.filter((cert) => cert.usage === "relay_ca");
        assert.equal(relayCAs.length, 1);
        assert.equal(relayCAs[0].certificate_type, "internal_ca");
        assert.equal(relayCAs[0].enabled, true);
        assert.equal(relayCAs[0].scope, "domain");
        assert.equal(relayCAs[0].status, "active");
        assert.match(String(relayCAs[0].material_hash || ""), /^[0-9a-f]{64}$/i);
        assert.deepEqual(relayCAs[0].target_agent_ids, []);
      },
    );
  });

  it("blocks ordinary certificate APIs from creating or mutating the system relay ca", async () => {
    await withBackendServer(
      {
        env: { PANEL_ROLE: "master" },
        agents: [
          {
            id: "edge-1",
            name: "Edge-1",
            agent_url: "http://edge-1:8080",
            agent_token: "token-edge-1",
            capabilities: ["cert_install", "http_rules", "l4"],
          },
        ],
        managedCertificates: [
          {
            id: 7,
            domain: "relay-cert.example.com",
            enabled: true,
            scope: "domain",
            issuer_mode: "local_http01",
            usage: "relay_tunnel",
            certificate_type: "uploaded",
            self_signed: true,
            target_agent_ids: ["edge-1"],
            status: "active",
            revision: 1,
          },
        ],
      },
      async ({ baseUrl }) => {
        const certificates = await jsonRequest(baseUrl, "GET", "/api/certificates");
        assert.equal(certificates.status, 200);
        const relayCA = certificates.payload.certificates.find((cert) => cert.usage === "relay_ca");
        assert.ok(relayCA, "expected system relay CA to exist");

        const duplicateReservedCreate = await jsonRequest(baseUrl, "POST", "/api/certificates", {
          domain: "__relay-ca.internal",
          enabled: true,
          scope: "domain",
          issuer_mode: "local_http01",
          usage: "relay_ca",
          certificate_type: "internal_ca",
          self_signed: true,
          target_agent_ids: ["edge-1"],
        });
        assert.equal(duplicateReservedCreate.status, 400);

        const duplicateReservedAgentCreate = await jsonRequest(
          baseUrl,
          "POST",
          "/api/agents/edge-1/certificates",
          {
            domain: "__relay-ca.internal",
            enabled: true,
            scope: "domain",
            issuer_mode: "local_http01",
            usage: "relay_ca",
            certificate_type: "internal_ca",
            self_signed: true,
          },
        );
        assert.equal(duplicateReservedAgentCreate.status, 400);

        const duplicateRoleCreate = await jsonRequest(baseUrl, "POST", "/api/certificates", {
          domain: "different-relay-ca.example.com",
          enabled: true,
          scope: "domain",
          issuer_mode: "local_http01",
          usage: "relay_ca",
          certificate_type: "internal_ca",
          self_signed: true,
          target_agent_ids: ["edge-1"],
          tags: [],
        });
        assert.equal(duplicateRoleCreate.status, 400);

        const duplicateReservedUpdate = await jsonRequest(
          baseUrl,
          "PUT",
          "/api/certificates/7",
          {
            domain: "__relay-ca.internal",
            usage: "relay_ca",
            certificate_type: "internal_ca",
            issuer_mode: "local_http01",
            self_signed: true,
            target_agent_ids: ["edge-1"],
          },
        );
        assert.equal(duplicateReservedUpdate.status, 400);

        const duplicateRoleUpdate = await jsonRequest(
          baseUrl,
          "PUT",
          "/api/certificates/7",
          {
            domain: "relay-ca-via-update.example.com",
            usage: "relay_ca",
            certificate_type: "internal_ca",
            issuer_mode: "local_http01",
            self_signed: true,
            target_agent_ids: ["edge-1"],
            tags: [],
          },
        );
        assert.equal(duplicateRoleUpdate.status, 400);

        const disableSystemRelayCA = await jsonRequest(
          baseUrl,
          "PUT",
          `/api/certificates/${relayCA.id}`,
          { enabled: false },
        );
        assert.equal(disableSystemRelayCA.status, 400);

        const mutateSystemRelayCA = await jsonRequest(
          baseUrl,
          "PUT",
          `/api/certificates/${relayCA.id}`,
          { domain: "relay-ca-mutated.example.com" },
        );
        assert.equal(mutateSystemRelayCA.status, 400);

        const finalCertificates = await jsonRequest(baseUrl, "GET", "/api/certificates");
        assert.equal(finalCertificates.status, 200);
        const relayCAs = finalCertificates.payload.certificates.filter((cert) => cert.usage === "relay_ca");
        assert.equal(relayCAs.length, 1);
        assert.equal(relayCAs[0].id, relayCA.id);
        assert.equal(relayCAs[0].enabled, true);
      },
    );
  });

  it("does not churn a healthy system relay ca on startup", async () => {
    const existingMaterialHash = crypto
      .createHash("sha256")
      .update(TEST_SERVER_CERT_PEM)
      .update("\n---\n")
      .update(TEST_SERVER_KEY_PEM)
      .digest("hex");

    await withBackendServer(
      {
        env: { PANEL_ROLE: "master" },
        managedCertificates: [
          {
            id: 41,
            domain: "__relay-ca.internal",
            enabled: true,
            scope: "domain",
            issuer_mode: "local_http01",
            usage: "relay_ca",
            certificate_type: "internal_ca",
            self_signed: true,
            target_agent_ids: ["local"],
            tags: ["system:relay-ca", "system"],
            status: "active",
            material_hash: existingMaterialHash,
            last_issue_at: "2026-04-08T00:00:00.000Z",
            revision: 77,
          },
        ],
        managedCertificateMaterial: {
          "__relay-ca.internal": {
            cert_pem: TEST_SERVER_CERT_PEM,
            key_pem: TEST_SERVER_KEY_PEM,
          },
        },
      },
      async ({ baseUrl }) => {
        const response = await jsonRequest(baseUrl, "GET", "/api/certificates");
        assert.equal(response.status, 200);
        const relayCA = response.payload.certificates.find((cert) => cert.usage === "relay_ca");
        assert.ok(relayCA, "expected relay CA to exist");
        assert.equal(relayCA.id, 41);
        assert.equal(relayCA.revision, 77);
        assert.equal(relayCA.status, "active");
        assert.equal(relayCA.last_issue_at, "2026-04-08T00:00:00.000Z");
        assert.equal(relayCA.material_hash, existingMaterialHash);
      },
    );
  });

  it("repairs drifted system relay ca invariants on startup", async () => {
    const existingMaterialHash = crypto
      .createHash("sha256")
      .update(TEST_SERVER_CERT_PEM)
      .update("\n---\n")
      .update(TEST_SERVER_KEY_PEM)
      .digest("hex");

    await withBackendServer(
      {
        env: {
          PANEL_ROLE: "master",
          MASTER_LOCAL_AGENT_ENABLED: "0",
        },
        managedCertificates: [
          {
            id: 49,
            domain: "__relay-ca.internal",
            enabled: false,
            scope: "domain",
            issuer_mode: "local_http01",
            usage: "relay_ca",
            certificate_type: "internal_ca",
            self_signed: true,
            target_agent_ids: ["local"],
            tags: ["system:relay-ca", "system"],
            status: "active",
            material_hash: existingMaterialHash,
            last_issue_at: "2026-04-08T00:00:00.000Z",
            revision: 88,
          },
        ],
        managedCertificateMaterial: {
          "__relay-ca.internal": {
            cert_pem: TEST_SERVER_CERT_PEM,
            key_pem: TEST_SERVER_KEY_PEM,
          },
        },
      },
      async ({ baseUrl }) => {
        const response = await jsonRequest(baseUrl, "GET", "/api/certificates");
        assert.equal(response.status, 200);
        const relayCA = response.payload.certificates.find((cert) => cert.usage === "relay_ca");
        assert.ok(relayCA, "expected relay CA to exist");
        assert.equal(relayCA.id, 49);
        assert.equal(relayCA.enabled, true);
        assert.deepEqual(relayCA.target_agent_ids, []);
        assert.equal(relayCA.status, "active");
        assert.equal(relayCA.material_hash, existingMaterialHash);
      },
    );
  });

  it("repairs a reserved relay ca candidate even when identity fields drifted", async () => {
    const existingMaterialHash = crypto
      .createHash("sha256")
      .update(TEST_SERVER_CERT_PEM)
      .update("\n---\n")
      .update(TEST_SERVER_KEY_PEM)
      .digest("hex");

    await withBackendServer(
      {
        env: { PANEL_ROLE: "master" },
        managedCertificates: [
          {
            id: 57,
            domain: "__relay-ca.internal",
            enabled: true,
            scope: "domain",
            issuer_mode: "local_http01",
            usage: "https",
            certificate_type: "uploaded",
            self_signed: false,
            target_agent_ids: ["local"],
            tags: ["system"],
            status: "active",
            material_hash: existingMaterialHash,
            last_issue_at: "2026-04-08T00:00:00.000Z",
            revision: 93,
          },
        ],
        managedCertificateMaterial: {
          "__relay-ca.internal": {
            cert_pem: TEST_SERVER_CERT_PEM,
            key_pem: TEST_SERVER_KEY_PEM,
          },
        },
      },
      async ({ baseUrl }) => {
        const response = await jsonRequest(baseUrl, "GET", "/api/certificates");
        assert.equal(response.status, 200);
        const certificates = response.payload.certificates;
        assert.equal(certificates.length, 1);
        const relayCA = certificates[0];
        assert.equal(relayCA.id, 57);
        assert.equal(relayCA.domain, "__relay-ca.internal");
        assert.equal(relayCA.usage, "relay_ca");
        assert.equal(relayCA.certificate_type, "internal_ca");
        assert.equal(relayCA.self_signed, true);
        assert.ok(Array.isArray(relayCA.tags) && relayCA.tags.includes("system:relay-ca"));
        assert.equal(relayCA.material_hash, existingMaterialHash);
      },
    );
  });

  it("repairs a reserved relay ca candidate with invalid persisted identity fields", async () => {
    const existingMaterialHash = crypto
      .createHash("sha256")
      .update(TEST_SERVER_CERT_PEM)
      .update("\n---\n")
      .update(TEST_SERVER_KEY_PEM)
      .digest("hex");

    await withBackendServer(
      {
        env: { PANEL_ROLE: "master" },
        managedCertificates: [
          {
            id: 58,
            domain: "__relay-ca.internal",
            enabled: true,
            scope: "domain",
            issuer_mode: "local_http01",
            usage: "bogus",
            certificate_type: "also-bogus",
            self_signed: false,
            target_agent_ids: ["local"],
            tags: ["system"],
            status: "active",
            material_hash: existingMaterialHash,
            last_issue_at: "2026-04-08T00:00:00.000Z",
            revision: 94,
          },
        ],
        managedCertificateMaterial: {
          "__relay-ca.internal": {
            cert_pem: TEST_SERVER_CERT_PEM,
            key_pem: TEST_SERVER_KEY_PEM,
          },
        },
      },
      async ({ baseUrl }) => {
        const response = await jsonRequest(baseUrl, "GET", "/api/certificates");
        assert.equal(response.status, 200);
        const certificates = response.payload.certificates;
        assert.equal(certificates.length, 1);
        const relayCA = certificates[0];
        assert.equal(relayCA.id, 58);
        assert.equal(relayCA.domain, "__relay-ca.internal");
        assert.equal(relayCA.usage, "relay_ca");
        assert.equal(relayCA.certificate_type, "internal_ca");
        assert.equal(relayCA.self_signed, true);
        assert.ok(Array.isArray(relayCA.tags) && relayCA.tags.includes("system:relay-ca"));
        assert.equal(relayCA.material_hash, existingMaterialHash);
      },
    );
  });

  it("repairs a reserved relay ca candidate with invalid persisted enabled", async () => {
    const existingMaterialHash = crypto
      .createHash("sha256")
      .update(TEST_SERVER_CERT_PEM)
      .update("\n---\n")
      .update(TEST_SERVER_KEY_PEM)
      .digest("hex");

    await withBackendServer(
      {
        env: { PANEL_ROLE: "master" },
        managedCertificates: [
          {
            id: 59,
            domain: "__relay-ca.internal",
            enabled: 0,
            scope: "domain",
            issuer_mode: "local_http01",
            usage: "relay_ca",
            certificate_type: "internal_ca",
            self_signed: true,
            target_agent_ids: ["local"],
            tags: ["system:relay-ca", "system"],
            status: "active",
            material_hash: existingMaterialHash,
            last_issue_at: "2026-04-08T00:00:00.000Z",
            revision: 95,
          },
        ],
        managedCertificateMaterial: {
          "__relay-ca.internal": {
            cert_pem: TEST_SERVER_CERT_PEM,
            key_pem: TEST_SERVER_KEY_PEM,
          },
        },
      },
      async ({ baseUrl }) => {
        const response = await jsonRequest(baseUrl, "GET", "/api/certificates");
        assert.equal(response.status, 200);
        const certificates = response.payload.certificates;
        assert.equal(certificates.length, 1);
        const relayCA = certificates[0];
        assert.equal(relayCA.id, 59);
        assert.equal(relayCA.enabled, true);
        assert.equal(relayCA.usage, "relay_ca");
        assert.equal(relayCA.certificate_type, "internal_ca");
        assert.equal(relayCA.material_hash, existingMaterialHash);
      },
    );
  });

  it("fails startup when multiple relay ca candidates are persisted", async () => {
    await assert.rejects(
      withBackendServer(
        {
          env: { PANEL_ROLE: "master" },
          managedCertificates: [
            {
              id: 61,
              domain: "__relay-ca.internal",
              enabled: true,
              scope: "domain",
              issuer_mode: "local_http01",
              usage: "relay_tunnel",
              certificate_type: "uploaded",
              self_signed: false,
              target_agent_ids: ["local"],
              tags: ["system"],
              status: "active",
              revision: 1,
            },
            {
              id: 62,
              domain: "relay-ca-duplicate.example.com",
              enabled: true,
              scope: "domain",
              issuer_mode: "local_http01",
              usage: "relay_ca",
              certificate_type: "internal_ca",
              self_signed: true,
              target_agent_ids: ["local"],
              tags: [],
              status: "pending",
              revision: 2,
            },
          ],
        },
        async () => {
          assert.fail("server should not have started");
        },
      ),
      /multiple relay ca candidates/i,
    );
  });

  it("repairs corrupt persisted relay ca material during bootstrap", async () => {
    const brokenMaterialHash = crypto
      .createHash("sha256")
      .update("BROKEN CERT")
      .update("\n---\n")
      .update("BROKEN KEY")
      .digest("hex");

    await withBackendServer(
      {
        env: { PANEL_ROLE: "master" },
        managedCertificates: [
          {
            id: 55,
            domain: "__relay-ca.internal",
            enabled: true,
            scope: "domain",
            issuer_mode: "local_http01",
            usage: "relay_ca",
            certificate_type: "internal_ca",
            self_signed: true,
            target_agent_ids: ["local"],
            tags: ["system:relay-ca", "system"],
            status: "active",
            material_hash: brokenMaterialHash,
            last_issue_at: "2026-04-08T00:00:00.000Z",
            revision: 91,
          },
        ],
        managedCertificateMaterial: {
          "__relay-ca.internal": {
            cert_pem: "BROKEN CERT",
            key_pem: "BROKEN KEY",
          },
        },
      },
      async ({ baseUrl, dataRoot }) => {
        const response = await jsonRequest(baseUrl, "GET", "/api/certificates");
        assert.equal(response.status, 200);
        const relayCA = response.payload.certificates.find((cert) => cert.usage === "relay_ca");
        assert.ok(relayCA, "expected relay CA to exist");
        assert.equal(relayCA.status, "active");
        assert.match(String(relayCA.material_hash || ""), /^[0-9a-f]{64}$/i);
        assert.notEqual(relayCA.material_hash, brokenMaterialHash);

        const certPath = path.join(dataRoot, "managed_certificates", "__relay-ca.internal", "cert");
        const keyPath = path.join(dataRoot, "managed_certificates", "__relay-ca.internal", "key");
        const [certPem, keyPem] = await Promise.all([
          fsp.readFile(certPath, "utf8"),
          fsp.readFile(keyPath, "utf8"),
        ]);
        assert.notEqual(certPem, "BROKEN CERT");
        assert.notEqual(keyPem, "BROKEN KEY");
        assert.match(certPem, /BEGIN CERTIFICATE/);
        assert.match(keyPem, /BEGIN [A-Z ]*PRIVATE KEY/);
      },
    );
  });

  it("validates relay listener TLS modes and certificate requirements", async () => {
    await withBackendServer(
      {
        agents: [
          {
            id: "edge-1",
            name: "Edge-1",
            agent_url: "http://edge-1:8080",
            agent_token: "token-edge-1",
            capabilities: ["http_rules", "l4"],
          },
        ],
        managedCertificates: [
          {
            id: 7,
            domain: "relay-cert.example.com",
            enabled: true,
            scope: "domain",
            issuer_mode: "local_http01",
            usage: "relay_tunnel",
            certificate_type: "uploaded",
            self_signed: true,
            target_agent_ids: ["edge-1"],
            status: "issued",
            revision: 1,
          },
          {
            id: 42,
            domain: "relay-ca.example.com",
            enabled: true,
            scope: "domain",
            issuer_mode: "local_http01",
            usage: "relay_ca",
            certificate_type: "internal_ca",
            self_signed: true,
            target_agent_ids: ["edge-1"],
            status: "issued",
            revision: 1,
          },
        ],
      },
      async ({ baseUrl }) => {
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
        assert.equal(created.payload.listener.tls_mode, "pin_and_ca");
        assert.equal(created.payload.listener.certificate_id, 7);
        assert.deepEqual(created.payload.listener.trusted_ca_certificate_ids, [42]);

        const invalid = await jsonRequest(baseUrl, "POST", "/api/agents/edge-1/relay-listeners", {
          name: "relay-missing-cert",
          listen_host: "0.0.0.0",
          listen_port: 20443,
          enabled: true,
          tls_mode: "pin_only",
          pin_set: [{ type: "spki_sha256", value: "abc123" }],
        });
        assert.equal(invalid.status, 400);
        assert.equal(invalid.payload.message, "certificate_id is required when relay listener is enabled");
      },
    );
  });

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
      async ({ baseUrl, dataRoot }) => {
        const response = await jsonRequest(baseUrl, "POST", "/api/agents/edge-1/relay-listeners", {
          name: "relay-a",
          listen_host: "relay-a.example.com",
          listen_port: 7443,
          enabled: true,
          certificate_source: "auto_relay_ca",
          trust_mode_source: "auto",
        });

        assert.equal(response.status, 201);
        assert.ok(Number.isInteger(response.payload.listener.certificate_id));
        assert.equal(response.payload.listener.tls_mode, "pin_and_ca");
        assert.equal(response.payload.listener.pin_set.length, 1);
        assert.ok(response.payload.listener.trusted_ca_certificate_ids.length >= 1);

        const certificates = await jsonRequest(baseUrl, "GET", "/api/certificates");
        assert.equal(certificates.status, 200);
        const relayCA = certificates.payload.certificates.find((cert) => cert.usage === "relay_ca");
        const listenerCert = certificates.payload.certificates.find(
          (cert) => cert.id === response.payload.listener.certificate_id,
        );
        assert.ok(relayCA);
        assert.ok(listenerCert);
        assert.notEqual(listenerCert.id, relayCA.id);
        assert.equal(listenerCert.usage, "relay_tunnel");
        assert.equal(listenerCert.certificate_type, "internal_ca");

        const relayCAPath = path.join(dataRoot, "managed_certificates", relayCA.domain, "cert");
        const relayCAKeyPath = path.join(dataRoot, "managed_certificates", relayCA.domain, "key");
        const listenerCertPath = path.join(dataRoot, "managed_certificates", listenerCert.domain, "cert");
        const listenerKeyPath = path.join(dataRoot, "managed_certificates", listenerCert.domain, "key");
        const [relayCAPem, relayCAKeyPem, listenerPem, listenerKeyPem] = await Promise.all([
          fsp.readFile(relayCAPath, "utf8"),
          fsp.readFile(relayCAKeyPath, "utf8"),
          fsp.readFile(listenerCertPath, "utf8"),
          fsp.readFile(listenerKeyPath, "utf8"),
        ]);

        assert.notEqual(listenerPem, relayCAPem);
        assert.notEqual(listenerKeyPem, relayCAKeyPem);

        const relayCACert = new crypto.X509Certificate(relayCAPem);
        const listenerLeafCert = new crypto.X509Certificate(listenerPem);
        assert.notEqual(listenerLeafCert.subject, relayCACert.subject);
        assert.equal(listenerLeafCert.verify(relayCACert.publicKey), true);

        const expectedLeafPin = computeSpkiSha256(listenerPem);
        const relayCAPin = computeSpkiSha256(relayCAPem);
        assert.equal(response.payload.listener.pin_set[0].value, expectedLeafPin);
        assert.notEqual(expectedLeafPin, relayCAPin);
      },
    );
  });

  it("prefers public_host when selecting auto-issued relay certificate identity", async () => {
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
        const created = await jsonRequest(baseUrl, "POST", "/api/agents/edge-1/relay-listeners", {
          name: "relay-identity",
          bind_hosts: ["10.0.0.10", "127.0.0.1"],
          listen_port: 7443,
          public_host: "relay-public.example.com",
          public_port: 443,
          enabled: true,
          certificate_source: "auto_relay_ca",
          trust_mode_source: "auto",
        });
        assert.equal(created.status, 201);

        const certificates = await jsonRequest(baseUrl, "GET", "/api/certificates");
        assert.equal(certificates.status, 200);
        const listenerCert = certificates.payload.certificates.find(
          (cert) => cert.id === created.payload.listener.certificate_id,
        );
        assert.ok(listenerCert);
        assert.match(listenerCert.domain, /relay-public-example-com/i);
        assert.doesNotMatch(listenerCert.domain, /10-0-0-10/i);
      },
    );
  });

  it("auto-issues distinct stable listener certificates for duplicate-name listeners on the same agent", async () => {
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
      async ({ baseUrl, dataRoot }) => {
        const first = await jsonRequest(baseUrl, "POST", "/api/agents/edge-1/relay-listeners", {
          name: "shared-name",
          listen_host: "relay-1.example.com",
          listen_port: 7443,
          enabled: true,
          certificate_source: "auto_relay_ca",
          trust_mode_source: "auto",
        });
        const second = await jsonRequest(baseUrl, "POST", "/api/agents/edge-1/relay-listeners", {
          name: "shared-name",
          listen_host: "relay-2.example.com",
          listen_port: 8443,
          enabled: true,
          certificate_source: "auto_relay_ca",
          trust_mode_source: "auto",
        });

        assert.equal(first.status, 201);
        assert.equal(second.status, 201);
        assert.notEqual(first.payload.listener.certificate_id, second.payload.listener.certificate_id);
        assert.notEqual(first.payload.listener.pin_set[0].value, second.payload.listener.pin_set[0].value);

        const certificates = await jsonRequest(baseUrl, "GET", "/api/certificates");
        const firstCert = certificates.payload.certificates.find(
          (cert) => cert.id === first.payload.listener.certificate_id,
        );
        const secondCert = certificates.payload.certificates.find(
          (cert) => cert.id === second.payload.listener.certificate_id,
        );
        assert.ok(firstCert);
        assert.ok(secondCert);
        assert.match(firstCert.domain, /^listener-1\.[a-z0-9-]+\.[a-z0-9-]+\.relay\.internal$/);
        assert.match(secondCert.domain, /^listener-2\.[a-z0-9-]+\.[a-z0-9-]+\.relay\.internal$/);
        assert.notEqual(firstCert.domain, secondCert.domain);

        const [firstPem, secondPem] = await Promise.all([
          fsp.readFile(path.join(dataRoot, "managed_certificates", firstCert.domain, "cert"), "utf8"),
          fsp.readFile(path.join(dataRoot, "managed_certificates", secondCert.domain, "cert"), "utf8"),
        ]);
        assert.notEqual(firstPem, secondPem);
      },
    );
  });

  it("cleans up auto-issued listener certificates when the listener is deleted", async () => {
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
      async ({ baseUrl, dataRoot }) => {
        const created = await jsonRequest(baseUrl, "POST", "/api/agents/edge-1/relay-listeners", {
          name: "relay-delete",
          listen_host: "relay-delete.example.com",
          listen_port: 7443,
          enabled: true,
          certificate_source: "auto_relay_ca",
          trust_mode_source: "auto",
        });
        assert.equal(created.status, 201);

        const certificates = await jsonRequest(baseUrl, "GET", "/api/certificates");
        const listenerCert = certificates.payload.certificates.find(
          (cert) => cert.id === created.payload.listener.certificate_id,
        );
        assert.ok(listenerCert);

        const deleted = await jsonRequest(
          baseUrl,
          "DELETE",
          `/api/agents/edge-1/relay-listeners/${created.payload.listener.id}`,
        );
        assert.equal(deleted.status, 200);

        const finalCertificates = await jsonRequest(baseUrl, "GET", "/api/certificates");
        assert.equal(
          finalCertificates.payload.certificates.some((cert) => cert.id === listenerCert.id),
          false,
        );
        await assert.rejects(
          fsp.access(path.join(dataRoot, "managed_certificates", listenerCert.domain, "cert")),
        );
      },
    );
  });

  it("cleans up old auto-issued listener certificates when switching to a manual certificate", async () => {
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
        managedCertificates: [
          {
            id: 7,
            domain: "relay-manual.example.com",
            enabled: true,
            scope: "domain",
            issuer_mode: "local_http01",
            usage: "relay_tunnel",
            certificate_type: "uploaded",
            self_signed: true,
            target_agent_ids: ["edge-1"],
            status: "active",
            revision: 1,
          },
        ],
        managedCertificateMaterial: {
          "relay-manual.example.com": {
            cert_pem: TEST_SERVER_CERT_PEM,
            key_pem: TEST_SERVER_KEY_PEM,
          },
        },
      },
      async ({ baseUrl, dataRoot }) => {
        const created = await jsonRequest(baseUrl, "POST", "/api/agents/edge-1/relay-listeners", {
          name: "relay-swap",
          listen_host: "relay-swap.example.com",
          listen_port: 7443,
          enabled: true,
          certificate_source: "auto_relay_ca",
          trust_mode_source: "auto",
        });
        assert.equal(created.status, 201);

        const initialCertificates = await jsonRequest(baseUrl, "GET", "/api/certificates");
        const autoCert = initialCertificates.payload.certificates.find(
          (cert) => cert.id === created.payload.listener.certificate_id,
        );
        assert.ok(autoCert);

        const updated = await jsonRequest(
          baseUrl,
          "PUT",
          `/api/agents/edge-1/relay-listeners/${created.payload.listener.id}`,
          {
            certificate_source: "existing_certificate",
            certificate_id: 7,
            trust_mode_source: "auto",
          },
        );
        assert.equal(updated.status, 200);
        assert.equal(updated.payload.listener.certificate_id, 7);

        const finalCertificates = await jsonRequest(baseUrl, "GET", "/api/certificates");
        assert.equal(finalCertificates.payload.certificates.some((cert) => cert.id === autoCert.id), false);
        await assert.rejects(
          fsp.access(path.join(dataRoot, "managed_certificates", autoCert.domain, "cert")),
        );
      },
    );
  });

  it("blocks deleting auto-issued listener certificates while listeners still reference them", async () => {
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
        const created = await jsonRequest(baseUrl, "POST", "/api/agents/edge-1/relay-listeners", {
          name: "relay-protected",
          listen_host: "relay-protected.example.com",
          listen_port: 7443,
          enabled: true,
          certificate_source: "auto_relay_ca",
          trust_mode_source: "auto",
        });
        assert.equal(created.status, 201);
        const certId = created.payload.listener.certificate_id;

        const agentDelete = await jsonRequest(
          baseUrl,
          "DELETE",
          `/api/agents/edge-1/certificates/${certId}`,
        );
        assert.equal(agentDelete.status, 400);

        const globalDelete = await jsonRequest(baseUrl, "DELETE", `/api/certificates/${certId}`);
        assert.equal(globalDelete.status, 400);
      },
    );
  });

  it("does not auto-trust relay ca when an unrelated uploaded leaf merely appends relay ca pem", async () => {
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
      async ({ baseUrl, dataRoot }) => {
        const certificates = await jsonRequest(baseUrl, "GET", "/api/certificates");
        assert.equal(certificates.status, 200);
        const relayCA = certificates.payload.certificates.find((cert) => cert.usage === "relay_ca");
        assert.ok(relayCA);
        const relayCAPem = await fsp.readFile(
          path.join(dataRoot, "managed_certificates", relayCA.domain, "cert"),
          "utf8",
        );

        const uploaded = await jsonRequest(baseUrl, "POST", "/api/agents/edge-1/certificates", {
          domain: "relay-uploaded.example.com",
          enabled: true,
          scope: "domain",
          issuer_mode: "local_http01",
          usage: "relay_tunnel",
          certificate_type: "uploaded",
          self_signed: true,
          certificate_pem: TEST_SERVER_CERT_PEM,
          private_key_pem: TEST_SERVER_KEY_PEM,
          ca_pem: relayCAPem,
        });
        assert.equal(uploaded.status, 201);

        const created = await jsonRequest(baseUrl, "POST", "/api/agents/edge-1/relay-listeners", {
          name: "relay-manual",
          listen_host: "relay-manual.example.com",
          listen_port: 7443,
          enabled: true,
          certificate_source: "existing_certificate",
          certificate_id: uploaded.payload.certificate.id,
          trust_mode_source: "auto",
        });

        assert.equal(created.status, 201);
        assert.equal(created.payload.listener.certificate_id, uploaded.payload.certificate.id);
        assert.equal(created.payload.listener.tls_mode, "pin_only");
        assert.deepEqual(created.payload.listener.trusted_ca_certificate_ids, []);
        assert.deepEqual(created.payload.listener.pin_set, [
          {
            type: "spki_sha256",
            value: computeSpkiSha256(TEST_SERVER_CERT_PEM),
          },
        ]);
      },
    );
  });

  it("does not auto-trust relay ca when an unrelated intermediate chain merely appends relay ca pem", async () => {
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
      async ({ baseUrl, dataRoot }) => {
        const certificates = await jsonRequest(baseUrl, "GET", "/api/certificates");
        assert.equal(certificates.status, 200);
        const relayCA = certificates.payload.certificates.find((cert) => cert.usage === "relay_ca");
        assert.ok(relayCA);
        const relayCAPem = await fsp.readFile(
          path.join(dataRoot, "managed_certificates", relayCA.domain, "cert"),
          "utf8",
        );
        const bundle = await generateUnrelatedIntermediateBundle();

        const uploaded = await jsonRequest(baseUrl, "POST", "/api/agents/edge-1/certificates", {
          domain: "relay-unrelated-chain.example.com",
          enabled: true,
          scope: "domain",
          issuer_mode: "local_http01",
          usage: "relay_tunnel",
          certificate_type: "uploaded",
          self_signed: false,
          certificate_pem: bundle.certificatePem,
          private_key_pem: bundle.privateKeyPem,
          ca_pem: relayCAPem,
        });
        assert.equal(uploaded.status, 201);

        const created = await jsonRequest(baseUrl, "POST", "/api/agents/edge-1/relay-listeners", {
          name: "relay-unrelated-chain",
          listen_host: "relay-unrelated-chain.example.com",
          listen_port: 7443,
          enabled: true,
          certificate_source: "existing_certificate",
          certificate_id: uploaded.payload.certificate.id,
          trust_mode_source: "auto",
        });

        assert.equal(created.status, 201);
        assert.equal(created.payload.listener.certificate_id, uploaded.payload.certificate.id);
        assert.equal(created.payload.listener.tls_mode, "pin_only");
        assert.deepEqual(created.payload.listener.trusted_ca_certificate_ids, []);
        assert.deepEqual(created.payload.listener.pin_set, [
          {
            type: "spki_sha256",
            value: computeSpkiSha256(bundle.certificatePem),
          },
        ]);
      },
    );
  });

  it("auto-trusts relay ca when an uploaded listener chain terminates at relay ca through an intermediate", async () => {
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
      async ({ baseUrl, dataRoot }) => {
        const certificates = await jsonRequest(baseUrl, "GET", "/api/certificates");
        assert.equal(certificates.status, 200);
        const relayCA = certificates.payload.certificates.find((cert) => cert.usage === "relay_ca");
        assert.ok(relayCA);

        const relayCACertPem = await fsp.readFile(
          path.join(dataRoot, "managed_certificates", relayCA.domain, "cert"),
          "utf8",
        );
        const relayCAKeyPem = await fsp.readFile(
          path.join(dataRoot, "managed_certificates", relayCA.domain, "key"),
          "utf8",
        );
        const bundle = await generateRelaySignedIntermediateBundle(relayCAKeyPem, relayCACertPem);

        const uploaded = await jsonRequest(baseUrl, "POST", "/api/agents/edge-1/certificates", {
          domain: "relay-indirect.example.com",
          enabled: true,
          scope: "domain",
          issuer_mode: "local_http01",
          usage: "relay_tunnel",
          certificate_type: "uploaded",
          self_signed: false,
          certificate_pem: bundle.certificatePem,
          private_key_pem: bundle.privateKeyPem,
          ca_pem: relayCACertPem,
        });
        assert.equal(uploaded.status, 201);

        const created = await jsonRequest(baseUrl, "POST", "/api/agents/edge-1/relay-listeners", {
          name: "relay-indirect",
          listen_host: "relay-indirect.example.com",
          listen_port: 7443,
          enabled: true,
          certificate_source: "existing_certificate",
          certificate_id: uploaded.payload.certificate.id,
          trust_mode_source: "auto",
        });

        assert.equal(created.status, 201);
        assert.equal(created.payload.listener.certificate_id, uploaded.payload.certificate.id);
        assert.equal(created.payload.listener.tls_mode, "pin_and_ca");
        assert.deepEqual(created.payload.listener.trusted_ca_certificate_ids, [relayCA.id]);
        assert.deepEqual(created.payload.listener.pin_set, [
          {
            type: "spki_sha256",
            value: computeSpkiSha256(bundle.certificatePem),
          },
        ]);
      },
    );
  });

  it("allows disabled auto relay listeners without forcing certificate issuance or trust derivation", async () => {
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
            capabilities: ["http_rules", "l4"],
            created_at: "2026-04-01T00:00:00.000Z",
            updated_at: "2026-04-01T00:00:00.000Z",
          },
        ],
      },
      async ({ baseUrl }) => {
        const created = await jsonRequest(baseUrl, "POST", "/api/agents/edge-1/relay-listeners", {
          name: "relay-disabled",
          listen_host: "relay-disabled.example.com",
          listen_port: 7443,
          enabled: false,
          certificate_source: "auto_relay_ca",
          trust_mode_source: "auto",
        });
        assert.equal(created.status, 201);
        assert.equal(created.payload.listener.certificate_id ?? null, null);

        const updated = await jsonRequest(
          baseUrl,
          "PUT",
          `/api/agents/edge-1/relay-listeners/${created.payload.listener.id}`,
          {
            enabled: false,
            name: "relay-disabled-updated",
            certificate_source: "auto_relay_ca",
            trust_mode_source: "auto",
          },
        );
        assert.equal(updated.status, 200);
        assert.equal(updated.payload.listener.certificate_id ?? null, null);
      },
    );
  });

  it("delete then recreate does not reuse the prior auto certificate identity", async () => {
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
        const first = await jsonRequest(baseUrl, "POST", "/api/agents/edge-1/relay-listeners", {
          name: "relay-recreate",
          listen_host: "relay-recreate.example.com",
          listen_port: 7443,
          enabled: true,
          certificate_source: "auto_relay_ca",
          trust_mode_source: "auto",
        });
        assert.equal(first.status, 201);

        const firstCertificates = await jsonRequest(baseUrl, "GET", "/api/certificates");
        const firstCert = firstCertificates.payload.certificates.find(
          (cert) => cert.id === first.payload.listener.certificate_id,
        );
        assert.ok(firstCert);

        const deleted = await jsonRequest(
          baseUrl,
          "DELETE",
          `/api/agents/edge-1/relay-listeners/${first.payload.listener.id}`,
        );
        assert.equal(deleted.status, 200);

        const second = await jsonRequest(baseUrl, "POST", "/api/agents/edge-1/relay-listeners", {
          name: "relay-recreate",
          listen_host: "relay-recreate.example.com",
          listen_port: 7443,
          enabled: true,
          certificate_source: "auto_relay_ca",
          trust_mode_source: "auto",
        });
        assert.equal(second.status, 201);

        const secondCertificates = await jsonRequest(baseUrl, "GET", "/api/certificates");
        const secondCert = secondCertificates.payload.certificates.find(
          (cert) => cert.id === second.payload.listener.certificate_id,
        );
        assert.ok(secondCert);
        assert.notEqual(secondCert.domain, firstCert.domain);
      },
    );
  });

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
        assert.equal(created.status, 201);

        const createdRule = await jsonRequest(baseUrl, "POST", "/api/agents/edge-1/rules", {
          frontend_url: "http://edge.example.com",
          backend_url: "http://127.0.0.1:8096",
          relay_chain: [created.payload.listener.id],
        });
        assert.equal(createdRule.status, 201);

        const deleted = await fetch(
          `${baseUrl}/api/agents/edge-1/relay-listeners/${created.payload.listener.id}`,
          {
            method: "DELETE",
          },
        );

        assert.equal(deleted.status, 400);
      },
    );
  });

  it("rejects invalid relay listener and relay-chain payloads", async () => {
    await withBackendServer(
      {
        agents: [
          {
            id: "edge-1",
            name: "Edge-1",
            agent_url: "http://edge-1:8080",
            agent_token: "token-edge-1",
            capabilities: ["http_rules", "l4"],
          },
        ],
      },
      async ({ baseUrl }) => {
        const invalidListener = await jsonRequest(baseUrl, "POST", "/api/agents/edge-1/relay-listeners", {
          name: "invalid",
          listen_host: "0.0.0.0",
          listen_port: 10443,
          pin_set: [],
          trusted_ca_certificate_ids: [],
        });
        assert.equal(invalidListener.status, 400);

        const ruleWithUnknownRelay = await jsonRequest(baseUrl, "POST", "/api/agents/edge-1/rules", {
          frontend_url: "https://a.example.com",
          backend_url: "http://127.0.0.1:8096",
          relay_chain: [999],
        });
        assert.equal(ruleWithUnknownRelay.status, 400);

        const l4UdpWithRelay = await jsonRequest(baseUrl, "POST", "/api/agents/edge-1/l4-rules", {
          protocol: "udp",
          listen_host: "0.0.0.0",
          listen_port: 5511,
          upstream_host: "127.0.0.1",
          upstream_port: 5511,
          relay_chain: [1],
        });
        assert.equal(l4UdpWithRelay.status, 400);
      },
    );
  });

  it("prevents deleting relay listeners referenced by other agents", async () => {
    await withBackendServer(
      {
        agents: [
          {
            id: "edge-1",
            name: "Edge-1",
            agent_url: "http://edge-1:8080",
            agent_token: "token-edge-1",
            capabilities: ["http_rules", "l4"],
          },
          {
            id: "edge-2",
            name: "Edge-2",
            agent_url: "http://edge-2:8080",
            agent_token: "token-edge-2",
            capabilities: ["http_rules", "l4"],
          },
        ],
        managedCertificates: [
          {
            id: 7,
            domain: "relay-cert.example.com",
            enabled: true,
            scope: "domain",
            issuer_mode: "local_http01",
            usage: "relay_tunnel",
            certificate_type: "uploaded",
            self_signed: true,
            target_agent_ids: ["edge-1"],
            status: "issued",
            revision: 1,
          },
        ],
      },
      async ({ baseUrl }) => {
        const created = await jsonRequest(baseUrl, "POST", "/api/agents/edge-1/relay-listeners", {
          name: "edge relay",
          listen_host: "0.0.0.0",
          listen_port: 10443,
          enabled: true,
          tls_mode: "pin_or_ca",
          certificate_id: 7,
          pin_set: [{ type: "spki_sha256", value: "abc" }],
          trusted_ca_certificate_ids: [],
          allow_self_signed: false,
          tags: ["relay"],
        });
        assert.equal(created.status, 201);
        const listenerId = created.payload.listener.id;

        const createdRule = await jsonRequest(baseUrl, "POST", "/api/agents/edge-2/rules", {
          frontend_url: "http://cross.example.com",
          backend_url: "http://127.0.0.1:8096",
          relay_chain: [listenerId],
        });
        assert.equal(createdRule.status, 201);

        const deleteReferenced = await jsonRequest(
          baseUrl,
          "DELETE",
          `/api/agents/edge-1/relay-listeners/${listenerId}`,
        );
        assert.equal(deleteReferenced.status, 400);
      },
    );
  });

  it("prevents disabling relay listeners when referenced by relay chains", async () => {
    await withBackendServer(
      {
        agents: [
          {
            id: "edge-1",
            name: "Edge-1",
            agent_url: "http://edge-1:8080",
            agent_token: "token-edge-1",
            capabilities: ["http_rules", "l4"],
          },
          {
            id: "edge-2",
            name: "Edge-2",
            agent_url: "http://edge-2:8080",
            agent_token: "token-edge-2",
            capabilities: ["http_rules", "l4"],
          },
        ],
        managedCertificates: [
          {
            id: 7,
            domain: "relay-cert.example.com",
            enabled: true,
            scope: "domain",
            issuer_mode: "local_http01",
            usage: "relay_tunnel",
            certificate_type: "uploaded",
            self_signed: true,
            target_agent_ids: ["edge-1"],
            status: "issued",
            revision: 1,
          },
        ],
      },
      async ({ baseUrl }) => {
        const created = await jsonRequest(baseUrl, "POST", "/api/agents/edge-1/relay-listeners", {
          name: "edge relay",
          listen_host: "0.0.0.0",
          listen_port: 10443,
          enabled: true,
          tls_mode: "pin_or_ca",
          certificate_id: 7,
          pin_set: [{ type: "spki_sha256", value: "abc" }],
          trusted_ca_certificate_ids: [],
          allow_self_signed: false,
          tags: ["relay"],
        });
        assert.equal(created.status, 201);
        const listenerId = created.payload.listener.id;

        const createdRule = await jsonRequest(baseUrl, "POST", "/api/agents/edge-2/rules", {
          frontend_url: "http://cross.example.com",
          backend_url: "http://127.0.0.1:8096",
          relay_chain: [listenerId],
        });
        assert.equal(createdRule.status, 201);

        const disableReferenced = await jsonRequest(
          baseUrl,
          "PUT",
          `/api/agents/edge-1/relay-listeners/${listenerId}`,
          { enabled: false },
        );
        assert.equal(disableReferenced.status, 400);
      },
    );
  });

  it("prevents deleting agent when its relay listener is referenced by other agents", async () => {
    await withBackendServer(
      {
        agents: [
          {
            id: "edge-1",
            name: "Edge-1",
            agent_url: "http://edge-1:8080",
            agent_token: "token-edge-1",
            capabilities: ["http_rules", "l4"],
          },
          {
            id: "edge-2",
            name: "Edge-2",
            agent_url: "http://edge-2:8080",
            agent_token: "token-edge-2",
            capabilities: ["http_rules", "l4"],
          },
        ],
        managedCertificates: [
          {
            id: 7,
            domain: "relay-cert.example.com",
            enabled: true,
            scope: "domain",
            issuer_mode: "local_http01",
            usage: "relay_tunnel",
            certificate_type: "uploaded",
            self_signed: true,
            target_agent_ids: ["edge-1"],
            status: "issued",
            revision: 1,
          },
        ],
      },
      async ({ baseUrl }) => {
        const created = await jsonRequest(baseUrl, "POST", "/api/agents/edge-1/relay-listeners", {
          name: "edge relay",
          listen_host: "0.0.0.0",
          listen_port: 10443,
          enabled: true,
          tls_mode: "pin_or_ca",
          certificate_id: 7,
          pin_set: [{ type: "spki_sha256", value: "abc" }],
          trusted_ca_certificate_ids: [],
          allow_self_signed: false,
          tags: ["relay"],
        });
        assert.equal(created.status, 201);
        const listenerId = created.payload.listener.id;

        const createdRule = await jsonRequest(baseUrl, "POST", "/api/agents/edge-2/rules", {
          frontend_url: "http://cross.example.com",
          backend_url: "http://127.0.0.1:8096",
          relay_chain: [listenerId],
        });
        assert.equal(createdRule.status, 201);

        const deleteAgent = await jsonRequest(baseUrl, "DELETE", "/api/agents/edge-1");
        assert.equal(deleteAgent.status, 400);
      },
    );
  });

  it("supports version policy CRUD with required desired_version", async () => {
    await withBackendServer({}, async ({ baseUrl }) => {
      const created = await jsonRequest(baseUrl, "POST", "/api/version-policies", {
        id: "stable",
        channel: "stable",
        desired_version: "1.2.3",
        packages: [{ platform: "linux-amd64", url: "https://example.com/pkg.tar.gz", sha256: "abc" }],
      });
      assert.equal(created.status, 201);

      const invalid = await jsonRequest(baseUrl, "POST", "/api/version-policies", {
        id: "broken",
        channel: "stable",
        desired_version: "",
        packages: [],
      });
      assert.equal(invalid.status, 400);

      const list = await jsonRequest(baseUrl, "GET", "/api/version-policies");
      assert.equal(list.status, 200);
      assert.ok(Array.isArray(list.payload.policies));

      const updated = await jsonRequest(baseUrl, "PUT", "/api/version-policies/stable", {
        desired_version: "1.2.4",
        packages: [{ platform: "linux-amd64", url: "https://example.com/pkg2.tar.gz", sha256: "def" }],
      });
      assert.equal(updated.status, 200);
      assert.equal(updated.payload.policy.desired_version, "1.2.4");

      const deleted = await jsonRequest(baseUrl, "DELETE", "/api/version-policies/stable");
      assert.equal(deleted.status, 200);
    });
  });
});
