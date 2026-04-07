"use strict";

const { describe, it } = require("node:test");
const assert = require("node:assert/strict");
const crypto = require("node:crypto");
const fsp = require("node:fs/promises");
const path = require("node:path");
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

        const list = await jsonRequest(baseUrl, "GET", "/api/agents/edge-1/relay-listeners");
        assert.equal(list.status, 200);
        assert.equal(list.payload.listeners.length, 1);

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
        assert.equal(invalid.payload.message, "target agent does not support certificate install: Edge-1");
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
        assert.ok(Number.isInteger(response.payload.listener.certificate_id));
        assert.equal(response.payload.listener.tls_mode, "pin_and_ca");
        assert.equal(response.payload.listener.pin_set.length, 1);
        assert.ok(response.payload.listener.trusted_ca_certificate_ids.length >= 1);
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
