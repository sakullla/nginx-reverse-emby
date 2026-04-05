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
        assert.equal(payload.sync.version_sha256, "sha-windows-match");
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
});
