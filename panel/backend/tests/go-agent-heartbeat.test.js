"use strict";

const { describe, it } = require("node:test");
const assert = require("node:assert/strict");
const { withBackendServer } = require("./helpers");

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
});
