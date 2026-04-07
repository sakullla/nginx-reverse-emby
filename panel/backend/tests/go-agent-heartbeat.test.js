"use strict";

const fs = require("node:fs");
const path = require("node:path");
const { describe, it } = require("node:test");
const assert = require("node:assert/strict");
const {
  withBackendServer,
  TEST_SERVER_CERT_PEM,
  TEST_SERVER_KEY_PEM,
  TEST_CA_CHAIN_PEM,
  TEST_SERVER_CHAIN_PEM,
} = require("./helpers");

describe("Go agent heartbeat API", () => {
  it("returns relay listeners and platform-matched version sync payload fields", async () => {
    await withBackendServer(
      {
        env: {
          PANEL_ROLE: "master",
        },
        agents: [
          {
            id: "remote-agent-1",
            name: "remote-agent-1",
            agent_token: "token-remote-agent-1",
            desired_revision: 8,
            current_revision: 1,
            desired_version: "1.2.3",
            created_at: "2026-04-01T00:00:00.000Z",
            updated_at: "2026-04-01T00:00:00.000Z",
          },
        ],
        relayListenersByAgentId: {
          "remote-agent-1": [
            {
              id: 5,
              agent_id: "remote-agent-1",
              name: "emby-relay",
              listen_host: "0.0.0.0",
              listen_port: 7000,
              enabled: true,
              certificate_id: 15,
              tls_mode: "pin_or_ca",
              pin_set: [
                {
                  type: "sha256",
                  value: "abc123",
                },
              ],
            },
          ],
        },
        versionPolicies: [
          {
            id: "stable-a",
            channel: "stable",
            desired_version: "1.2.3",
            packages: [
              {
                platform: "windows-amd64",
                url: "https://example.com/agent-windows-a.zip",
                sha256: "sha-windows-a",
              },
            ],
          },
          {
            id: "stable-z",
            channel: "stable",
            desired_version: "1.2.3",
            packages: [
              {
                platform: "windows-amd64",
                url: "https://example.com/agent-windows-z.zip",
                sha256: "sha-windows-z",
              },
              {
                platform: "linux-amd64",
                url: "https://example.com/agent-linux.tar.gz",
                sha256: "sha-linux",
              },
            ],
          },
        ],
      },
      async ({ baseUrl }) => {
        const response = await fetch(`${baseUrl}/api/agents/heartbeat`, {
          method: "POST",
          headers: {
            "content-type": "application/json",
            "x-agent-token": "token-remote-agent-1",
          },
          body: JSON.stringify({
            name: "remote-agent-1",
            current_revision: 1,
            version: "1.0.0",
            platform: "windows-amd64",
          }),
        });

        assert.equal(response.status, 200);
        const payload = await response.json();

        assert.equal(payload.agent.version, "1.0.0");
        assert.equal(payload.agent.platform, "windows-amd64");
        assert.equal(payload.agent.desired_version, "1.2.3");
        assert.deepEqual(payload.sync.relay_listeners, [
          {
            id: 5,
            agent_id: "remote-agent-1",
            name: "emby-relay",
            listen_host: "0.0.0.0",
            listen_port: 7000,
            enabled: true,
            certificate_id: 15,
            tls_mode: "pin_or_ca",
            pin_set: [
              {
                type: "sha256",
                value: "abc123",
              },
            ],
            trusted_ca_certificate_ids: [],
            allow_self_signed: false,
            tags: [],
            revision: 0,
          },
        ]);
        assert.equal(payload.sync.desired_version, "1.2.3");
        assert.equal(payload.sync.version_package, "https://example.com/agent-windows-a.zip");
        assert.deepEqual(payload.sync.version_package_meta, {
          platform: "windows-amd64",
          url: "https://example.com/agent-windows-a.zip",
          sha256: "sha-windows-a",
        });
        assert.equal(payload.sync.version_sha256, "sha-windows-a");
      },
    );
  });

  it("includes referenced remote relay listeners for HTTP relay chains", async () => {
    await withBackendServer(
      {
        env: {
          PANEL_ROLE: "master",
        },
        agents: [
          {
            id: "remote-agent-a",
            name: "remote-agent-a",
            agent_token: "token-remote-agent-a",
            desired_revision: 5,
            current_revision: 1,
            created_at: "2026-04-01T00:00:00.000Z",
            updated_at: "2026-04-01T00:00:00.000Z",
          },
          {
            id: "remote-agent-b",
            name: "remote-agent-b",
            agent_token: "token-remote-agent-b",
            desired_revision: 3,
            current_revision: 1,
            created_at: "2026-04-01T00:00:00.000Z",
            updated_at: "2026-04-01T00:00:00.000Z",
          },
        ],
        agentRulesByAgentId: {
          "remote-agent-a": [
            {
              id: 9,
              frontend_url: "http://edge-a.example.com",
              backend_url: "http://127.0.0.1:8096",
              relay_chain: [11, 22],
              revision: 6,
            },
          ],
        },
        relayListenersByAgentId: {
          "remote-agent-a": [
            {
              id: 11,
              agent_id: "remote-agent-a",
              name: "relay-a",
              listen_host: "relay-a.example.com",
              listen_port: 7443,
              enabled: true,
              certificate_id: 31,
              tls_mode: "pin_only",
              pin_set: [
                {
                  type: "sha256",
                  value: "pin-a",
                },
              ],
              revision: 4,
            },
          ],
          "remote-agent-b": [
            {
              id: 22,
              agent_id: "remote-agent-b",
              name: "relay-b",
              listen_host: "relay-b.example.com",
              listen_port: 8443,
              enabled: true,
              certificate_id: 32,
              tls_mode: "pin_only",
              pin_set: [
                {
                  type: "sha256",
                  value: "pin-b",
                },
              ],
              revision: 7,
            },
          ],
        },
      },
      async ({ baseUrl }) => {
        const response = await fetch(`${baseUrl}/api/agents/heartbeat`, {
          method: "POST",
          headers: {
            "content-type": "application/json",
            "x-agent-token": "token-remote-agent-a",
          },
          body: JSON.stringify({
            name: "remote-agent-a",
            current_revision: 1,
            version: "1.0.0",
            platform: "linux-amd64",
          }),
        });

        assert.equal(response.status, 200);
        const payload = await response.json();

        assert.deepEqual(payload.sync.rules, [
          {
            id: 9,
            frontend_url: "http://edge-a.example.com",
            backend_url: "http://127.0.0.1:8096",
            enabled: true,
            tags: [],
            proxy_redirect: true,
            pass_proxy_headers: true,
            user_agent: "",
            custom_headers: [],
            relay_chain: [11, 22],
            revision: 6,
          },
        ]);
        assert.deepEqual(
          payload.sync.relay_listeners.map((listener) => listener.id),
          [11, 22],
        );
        assert.equal(payload.sync.relay_listeners[1].agent_id, "remote-agent-b");
      },
    );
  });

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

  it("persists heartbeat platform/version fields and exposes them on agent APIs", async () => {
    await withBackendServer(
      {
        env: {
          PANEL_ROLE: "master",
        },
        agents: [
          {
            id: "remote-agent-2",
            name: "remote-agent-2",
            agent_token: "token-remote-agent-2",
            desired_revision: 3,
            current_revision: 3,
            desired_version: "2.0.0",
            created_at: "2026-04-01T00:00:00.000Z",
            updated_at: "2026-04-01T00:00:00.000Z",
          },
        ],
      },
      async ({ baseUrl }) => {
        const heartbeatResponse = await fetch(`${baseUrl}/api/agents/heartbeat`, {
          method: "POST",
          headers: {
            "content-type": "application/json",
            "x-agent-token": "token-remote-agent-2",
          },
          body: JSON.stringify({
            name: "remote-agent-2",
            current_revision: 3,
            version: "1.9.9",
            platform: "linux-amd64",
          }),
        });

        assert.equal(heartbeatResponse.status, 200);

        const listResponse = await fetch(`${baseUrl}/api/agents`);
        assert.equal(listResponse.status, 200);
        const listPayload = await listResponse.json();
        const listedAgent = listPayload.agents.find((agent) => agent.id === "remote-agent-2");
        assert.ok(listedAgent);
        assert.equal(listedAgent.version, "1.9.9");
        assert.equal(listedAgent.platform, "linux-amd64");
        assert.equal(listedAgent.desired_version, "2.0.0");

        const detailResponse = await fetch(`${baseUrl}/api/agents/remote-agent-2`);
        assert.equal(detailResponse.status, 200);
        const detailPayload = await detailResponse.json();
        assert.equal(detailPayload.agent.version, "1.9.9");
        assert.equal(detailPayload.agent.platform, "linux-amd64");
        assert.equal(detailPayload.agent.desired_version, "2.0.0");
      },
    );
  });

  it("resolves version packages across all matching desired-version policies", async () => {
    await withBackendServer(
      {
        env: {
          PANEL_ROLE: "master",
          ACME_DNS_PROVIDER: "cf",
          CF_Token: "test-token",
        },
        agents: [
          {
            id: "remote-agent-3",
            name: "remote-agent-3",
            agent_token: "token-remote-agent-3",
            desired_revision: 4,
            current_revision: 1,
            desired_version: "3.0.0",
            created_at: "2026-04-01T00:00:00.000Z",
            updated_at: "2026-04-01T00:00:00.000Z",
          },
        ],
        versionPolicies: [
          {
            id: "policy-a",
            channel: "stable",
            desired_version: "3.0.0",
            packages: [
              {
                platform: "linux-amd64",
                url: "https://example.com/linux-only.tar.gz",
                sha256: "sha-linux-only",
              },
            ],
          },
          {
            id: "policy-b",
            channel: "stable",
            desired_version: "3.0.0",
            packages: [
              {
                platform: "windows-amd64",
                url: "https://example.com/windows-match.zip",
                sha256: "sha-windows-match",
              },
            ],
          },
        ],
      },
      async ({ baseUrl }) => {
        const response = await fetch(`${baseUrl}/api/agents/heartbeat`, {
          method: "POST",
          headers: {
            "content-type": "application/json",
            "x-agent-token": "token-remote-agent-3",
          },
          body: JSON.stringify({
            name: "remote-agent-3",
            current_revision: 1,
            version: "2.9.0",
            platform: "windows-amd64",
          }),
        });

        assert.equal(response.status, 200);
        const payload = await response.json();
        assert.equal(payload.sync.desired_version, "3.0.0");
        assert.equal(payload.sync.version_package, "https://example.com/windows-match.zip");
        assert.deepEqual(payload.sync.version_package_meta, {
          platform: "windows-amd64",
          url: "https://example.com/windows-match.zip",
          sha256: "sha-windows-match",
        });
        assert.equal(payload.sync.version_sha256, "sha-windows-match");
      },
    );
  });

  it("returns full sync payloads when a config update is pending", async () => {
    await withBackendServer(
      {
        env: {
          PANEL_ROLE: "master",
        },
        agents: [
          {
            id: "remote-agent-5",
            name: "remote-agent-5",
            agent_token: "token-remote-agent-5",
            desired_revision: 6,
            current_revision: 2,
            desired_version: "4.0.0",
            created_at: "2026-04-01T00:00:00.000Z",
            updated_at: "2026-04-01T00:00:00.000Z",
          },
        ],
        agentRulesByAgentId: {
          "remote-agent-5": [
            {
              id: 1,
              frontend_url: "https://frontend.example.com",
              backend_url: "https://backend.example.com",
              enabled: true,
              tags: ["blue"],
              proxy_redirect: true,
              revision: 5,
            },
          ],
        },
        relayListenersByAgentId: {
          "remote-agent-5": [
            {
              id: 12,
              agent_id: "remote-agent-5",
              name: "relay-primary",
              listen_host: "0.0.0.0",
              listen_port: 7001,
              enabled: true,
              tls_mode: "pin_or_ca",
              pin_set: [
                {
                  type: "sha256",
                  value: "relayhash",
                },
              ],
              revision: 5,
            },
          ],
        },
        l4RulesByAgentId: {
          "remote-agent-5": [
            {
              id: 2,
              agent_id: "remote-agent-5",
              name: "tcp-service",
              protocol: "tcp",
              listen_host: "0.0.0.0",
              listen_port: 9000,
              upstream_host: "127.0.0.1",
              upstream_port: 9001,
              enabled: true,
              tags: ["sync"],
              revision: 4,
            },
          ],
        },
        managedCertificates: [
          {
            id: 21,
            domain: "sync.example.com",
            enabled: true,
            scope: "domain",
            issuer_mode: "local_http01",
            usage: "relay_ca",
            certificate_type: "internal_ca",
            self_signed: true,
            target_agent_ids: ["remote-agent-5"],
            status: "issued",
            revision: 3,
          },
          {
            id: 22,
            domain: "192.0.2.44",
            enabled: true,
            scope: "ip",
            issuer_mode: "local_http01",
            usage: "https",
            certificate_type: "uploaded",
            self_signed: false,
            target_agent_ids: ["remote-agent-5"],
            status: "issued",
            revision: 4,
          },
        ],
        managedCertificateMaterial: {
          "sync.example.com": {
            cert_pem: "CERTIFICATE",
            key_pem: "PRIVATEKEY",
          },
          "192.0.2.44": {
            cert_pem: "IP_CERTIFICATE",
            key_pem: "IP_PRIVATEKEY",
          },
        },
        versionPolicies: [
          {
            id: "stable-sync",
            channel: "stable",
            desired_version: "4.0.0",
            packages: [
              {
                platform: "linux-amd64",
                url: "https://example.com/agent-linux.tar.gz",
                sha256: "sha-linux-sync",
              },
            ],
          },
        ],
      },
      async ({ baseUrl }) => {
        const response = await fetch(`${baseUrl}/api/agents/heartbeat`, {
          method: "POST",
          headers: {
            "content-type": "application/json",
            "x-agent-token": "token-remote-agent-5",
          },
          body: JSON.stringify({
            name: "remote-agent-5",
            current_revision: 2,
            version: "3.9.0",
            platform: "linux-amd64",
          }),
        });

        assert.equal(response.status, 200);
        const payload = await response.json();
        assert.equal(payload.sync.has_update, true);
        assert.equal(payload.sync.desired_revision, 6);
        assert.equal(typeof payload.sync.desired_revision, "number");
        assert.ok(Array.isArray(payload.sync.rules));
        assert.equal(payload.sync.rules[0].frontend_url, "https://frontend.example.com");
        assert.ok(Array.isArray(payload.sync.l4_rules));
        assert.equal(payload.sync.l4_rules[0].listen_port, 9000);
        assert.ok(Array.isArray(payload.sync.relay_listeners));
        assert.ok(Array.isArray(payload.sync.certificates));
        assert.equal(payload.sync.certificates[0].domain, "sync.example.com");
        assert.equal(payload.sync.certificates[0].cert_pem, "CERTIFICATE");
        assert.equal(payload.sync.certificates[0].key_pem, "PRIVATEKEY");
        assert.deepEqual(payload.sync.certificates[1], {
          id: 22,
          domain: "192.0.2.44",
          revision: 4,
          cert_pem: "IP_CERTIFICATE",
          key_pem: "IP_PRIVATEKEY",
        });
        assert.ok(Array.isArray(payload.sync.certificate_policies));
        assert.deepEqual(payload.sync.certificate_policies[0], {
          id: 21,
          domain: "sync.example.com",
          enabled: true,
          scope: "domain",
          issuer_mode: "local_http01",
          status: "issued",
          last_issue_at: null,
          last_error: "",
          acme_info: {
            Main_Domain: "",
            KeyLength: "",
            SAN_Domains: "",
            Profile: "",
            CA: "",
            Created: "",
            Renew: "",
          },
          tags: [],
          revision: 3,
          usage: "relay_ca",
          certificate_type: "internal_ca",
          self_signed: true,
        });
        assert.deepEqual(payload.sync.certificate_policies[1], {
          id: 22,
          domain: "192.0.2.44",
          enabled: true,
          scope: "ip",
          issuer_mode: "local_http01",
          status: "issued",
          last_issue_at: null,
          last_error: "",
          acme_info: {
            Main_Domain: "",
            KeyLength: "",
            SAN_Domains: "",
            Profile: "",
            CA: "",
            Created: "",
            Renew: "",
          },
          tags: [],
          revision: 4,
          usage: "https",
          certificate_type: "uploaded",
          self_signed: false,
        });
        assert.equal(payload.sync.desired_version, "4.0.0");
        assert.equal(payload.sync.version_package, "https://example.com/agent-linux.tar.gz");
        assert.deepEqual(payload.sync.version_package_meta, {
          platform: "linux-amd64",
          url: "https://example.com/agent-linux.tar.gz",
          sha256: "sha-linux-sync",
        });
        assert.equal(payload.sync.version_sha256, "sha-linux-sync");
      },
    );
  });

  it("uses relay listener revisions when recalculating desired sync revision", async () => {
    await withBackendServer(
      {
        env: {
          PANEL_ROLE: "master",
        },
        agents: [
          {
            id: "remote-agent-4",
            name: "remote-agent-4",
            agent_token: "token-remote-agent-4",
            desired_revision: 2,
            current_revision: 2,
            created_at: "2026-04-01T00:00:00.000Z",
            updated_at: "2026-04-01T00:00:00.000Z",
          },
        ],
        relayListenersByAgentId: {
          "remote-agent-4": [
            {
              id: 9,
              agent_id: "remote-agent-4",
              name: "relay-revision-only",
              listen_host: "0.0.0.0",
              listen_port: 7443,
              enabled: true,
              certificate_id: 19,
              tls_mode: "pin_or_ca",
              pin_set: [
                {
                  type: "sha256",
                  value: "def456",
                },
              ],
              revision: 7,
            },
          ],
        },
      },
      async ({ baseUrl }) => {
        const applyResponse = await fetch(`${baseUrl}/api/agents/remote-agent-4/apply`, {
          method: "POST",
        });
        assert.equal(applyResponse.status, 200);

        const detailResponse = await fetch(`${baseUrl}/api/agents/remote-agent-4`);
        assert.equal(detailResponse.status, 200);
        const detailPayload = await detailResponse.json();
        assert.equal(detailPayload.agent.desired_revision, 7);

        const heartbeatResponse = await fetch(`${baseUrl}/api/agents/heartbeat`, {
          method: "POST",
          headers: {
            "content-type": "application/json",
            "x-agent-token": "token-remote-agent-4",
          },
          body: JSON.stringify({
            name: "remote-agent-4",
            current_revision: 2,
          }),
        });
        assert.equal(heartbeatResponse.status, 200);
        const heartbeatPayload = await heartbeatResponse.json();
        assert.equal(heartbeatPayload.sync.has_update, true);
        assert.equal(heartbeatPayload.sync.desired_revision, 7);
        assert.equal(heartbeatPayload.sync.relay_listeners[0].revision, 7);
      },
    );
  });

  it("rejects master_cf_dns certificates unless they target only the local master agent", async () => {
    await withBackendServer(
      {
        env: {
          PANEL_ROLE: "master",
          ACME_DNS_PROVIDER: "cf",
          CF_Token: "test-token",
        },
        agents: [
          {
            id: "remote-agent-9",
            name: "remote-agent-9",
            agent_token: "token-remote-agent-9",
            desired_revision: 1,
            current_revision: 1,
            capabilities: ["cert_install", "local_acme"],
            created_at: "2026-04-01T00:00:00.000Z",
            updated_at: "2026-04-01T00:00:00.000Z",
          },
        ],
        managedCertificates: [
          {
            id: 91,
            domain: "local-only.example.com",
            enabled: true,
            scope: "domain",
            issuer_mode: "master_cf_dns",
            usage: "https",
            certificate_type: "acme",
            self_signed: false,
            target_agent_ids: ["local"],
            status: "pending",
            revision: 1,
          },
        ],
      },
      async ({ baseUrl }) => {
        const createResponse = await fetch(`${baseUrl}/api/certificates`, {
          method: "POST",
          headers: {
            "content-type": "application/json",
          },
          body: JSON.stringify({
            domain: "remote-invalid.example.com",
            enabled: true,
            scope: "domain",
            issuer_mode: "master_cf_dns",
            usage: "https",
            certificate_type: "acme",
            self_signed: false,
            target_agent_ids: ["remote-agent-9"],
          }),
        });

        assert.equal(createResponse.status, 400);
        const createPayload = await createResponse.json();
        assert.match(createPayload.message, /master_cf_dns certificates must target only the local master agent/i);

        const updateResponse = await fetch(`${baseUrl}/api/certificates/91`, {
          method: "PUT",
          headers: {
            "content-type": "application/json",
          },
          body: JSON.stringify({
            target_agent_ids: ["local", "remote-agent-9"],
          }),
        });

        assert.equal(updateResponse.status, 400);
        const updatePayload = await updateResponse.json();
        assert.match(updatePayload.message, /master_cf_dns certificates must target only the local master agent/i);

        const invalidTypeResponse = await fetch(`${baseUrl}/api/certificates/91`, {
          method: "PUT",
          headers: {
            "content-type": "application/json",
          },
          body: JSON.stringify({
            target_agent_ids: ["local"],
            certificate_type: "uploaded",
          }),
        });

        assert.equal(invalidTypeResponse.status, 400);
        const invalidTypePayload = await invalidTypeResponse.json();
        assert.match(invalidTypePayload.message, /master_cf_dns certificates must use certificate_type=acme/i);
      },
    );
  });

  it("allows uploaded relay certificates for cert_install agents without local_acme", async () => {
    await withBackendServer(
      {
        env: {
          PANEL_ROLE: "master",
        },
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
      async ({ baseUrl }) => {
        const response = await fetch(`${baseUrl}/api/agents/remote-agent-relay/certificates`, {
          method: "POST",
          headers: {
            "content-type": "application/json",
          },
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
        assert.equal(payload.certificate.domain, "relay-uploaded.example.com");
        assert.equal(payload.certificate.certificate_type, "uploaded");
        assert.equal(payload.certificate.self_signed, true);
        assert.deepEqual(payload.certificate.target_agent_ids, ["remote-agent-relay"]);

        const acmeResponse = await fetch(`${baseUrl}/api/agents/remote-agent-relay/certificates`, {
          method: "POST",
          headers: {
            "content-type": "application/json",
          },
          body: JSON.stringify({
            domain: "relay-acme.example.com",
            enabled: true,
            scope: "domain",
            issuer_mode: "local_http01",
            usage: "relay_tunnel",
            certificate_type: "acme",
            self_signed: false,
          }),
        });

        assert.equal(acmeResponse.status, 400);
        const acmePayload = await acmeResponse.json();
        assert.match(acmePayload.message, /does not support local ACME issuance/i);
      },
    );
  });

  it("creates uploaded relay certificates from PEM input and immediately syncs material", async () => {
    await withBackendServer(
      {
        env: {
          PANEL_ROLE: "master",
        },
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
          headers: {
            "content-type": "application/json",
          },
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
        assert.equal(await fs.promises.readFile(certPath, "utf8"), TEST_SERVER_CHAIN_PEM);
        assert.equal(await fs.promises.readFile(keyPath, "utf8"), TEST_SERVER_KEY_PEM);

        const syncResponse = await fetch(`${baseUrl}/api/agents/heartbeat`, {
          method: "POST",
          headers: {
            "content-type": "application/json",
            "x-agent-token": "token-remote-agent-relay",
          },
          body: JSON.stringify({
            name: "remote-agent-relay",
            current_revision: 1,
          }),
        });
        assert.equal(syncResponse.status, 200);
        const syncPayload = await syncResponse.json();
        const uploaded = syncPayload.sync.certificates.find(
          (cert) => cert.domain === "relay-uploaded.example.com",
        );
        assert.ok(uploaded, "expected uploaded relay certificate in sync payload");
        assert.equal(uploaded.cert_pem, TEST_SERVER_CHAIN_PEM);
        assert.equal(uploaded.key_pem, TEST_SERVER_KEY_PEM);
      },
    );
  });

  it("issues uploaded local_http01 certificates by syncing existing material", async () => {
    await withBackendServer(
      {
        env: {
          PANEL_ROLE: "master",
        },
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
        managedCertificates: [
          {
            id: 41,
            domain: "relay-uploaded.example.com",
            enabled: true,
            scope: "domain",
            issuer_mode: "local_http01",
            usage: "relay_tunnel",
            certificate_type: "uploaded",
            self_signed: true,
            target_agent_ids: ["remote-agent-relay"],
            status: "pending",
            revision: 3,
          },
        ],
        managedCertificateMaterial: {
          "relay-uploaded.example.com": {
            cert_pem: "CERTIFICATE",
            key_pem: "PRIVATEKEY",
          },
          "relay-internal.example.com": {
            cert_pem: "INTERNAL_CERTIFICATE",
            key_pem: "INTERNAL_PRIVATEKEY",
          },
        },
      },
      async ({ baseUrl }) => {
        const response = await fetch(`${baseUrl}/api/agents/remote-agent-relay/certificates/41/issue`, {
          method: "POST",
          headers: {
            "content-type": "application/json",
          },
          body: "{}",
        });

        assert.equal(response.status, 200);
        const payload = await response.json();
        assert.equal(payload.certificate.id, 41);
        assert.equal(payload.certificate.status, "active");
        assert.equal(payload.certificate.last_error, "");
        assert.ok(payload.certificate.last_issue_at);
        assert.match(String(payload.certificate.material_hash || ""), /^[0-9a-f]{64}$/i);

        const createInternalResponse = await fetch(`${baseUrl}/api/agents/remote-agent-relay/certificates`, {
          method: "POST",
          headers: {
            "content-type": "application/json",
          },
          body: JSON.stringify({
            domain: "relay-internal.example.com",
            enabled: true,
            scope: "domain",
            issuer_mode: "local_http01",
            usage: "relay_ca",
            certificate_type: "internal_ca",
            self_signed: true,
          }),
        });

        assert.equal(createInternalResponse.status, 201);
        const createInternalPayload = await createInternalResponse.json();

        const issueInternalResponse = await fetch(`${baseUrl}/api/certificates/${createInternalPayload.certificate.id}/issue`, {
          method: "POST",
          headers: {
            "content-type": "application/json",
          },
          body: "{}",
        });

        assert.equal(issueInternalResponse.status, 200);
        const issueInternalPayload = await issueInternalResponse.json();
        assert.equal(issueInternalPayload.certificate.certificate_type, "internal_ca");
        assert.equal(issueInternalPayload.certificate.status, "active");
        assert.match(String(issueInternalPayload.certificate.material_hash || ""), /^[0-9a-f]{64}$/i);
      },
    );
  });

  it("rejects issuing static local_http01 certificates when target agent lacks cert_install", async () => {
    await withBackendServer(
      {
        env: {
          PANEL_ROLE: "master",
        },
        agents: [
          {
            id: "remote-agent-relay",
            name: "remote-agent-relay",
            agent_token: "token-remote-agent-relay",
            desired_revision: 1,
            current_revision: 1,
            capabilities: [],
            created_at: "2026-04-01T00:00:00.000Z",
            updated_at: "2026-04-01T00:00:00.000Z",
          },
        ],
        managedCertificates: [
          {
            id: 51,
            domain: "relay-uploaded.example.com",
            enabled: true,
            scope: "domain",
            issuer_mode: "local_http01",
            usage: "relay_tunnel",
            certificate_type: "uploaded",
            self_signed: true,
            target_agent_ids: ["remote-agent-relay"],
            status: "pending",
            revision: 3,
          },
        ],
        managedCertificateMaterial: {
          "relay-uploaded.example.com": {
            cert_pem: "CERTIFICATE",
            key_pem: "PRIVATEKEY",
          },
        },
      },
      async ({ baseUrl }) => {
        const response = await fetch(`${baseUrl}/api/certificates/51/issue`, {
          method: "POST",
          headers: {
            "content-type": "application/json",
          },
          body: "{}",
        });

        assert.equal(response.status, 400);
        const payload = await response.json();
        assert.match(payload.message, /does not support certificate install/i);
      },
    );
  });

  it("rejects issuing acme local_http01 certificates when target agent lacks local_acme", async () => {
    await withBackendServer(
      {
        env: {
          PANEL_ROLE: "master",
        },
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
        managedCertificates: [
          {
            id: 61,
            domain: "relay-acme.example.com",
            enabled: true,
            scope: "domain",
            issuer_mode: "local_http01",
            usage: "relay_tunnel",
            certificate_type: "acme",
            self_signed: false,
            target_agent_ids: ["remote-agent-relay"],
            status: "pending",
            revision: 3,
          },
        ],
      },
      async ({ baseUrl }) => {
        const response = await fetch(`${baseUrl}/api/certificates/61/issue`, {
          method: "POST",
          headers: {
            "content-type": "application/json",
          },
          body: "{}",
        });

        assert.equal(response.status, 400);
        const payload = await response.json();
        assert.match(payload.message, /does not support local ACME issuance/i);
      },
    );
  });
});
