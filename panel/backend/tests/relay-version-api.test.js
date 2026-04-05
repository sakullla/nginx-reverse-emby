"use strict";

const { describe, it } = require("node:test");
const assert = require("node:assert/strict");
const { withBackendServer } = require("./helpers");

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
      },
      async ({ baseUrl }) => {
        const created = await jsonRequest(baseUrl, "POST", "/api/agents/edge-1/relay-listeners", {
          name: "edge relay",
          listen_host: "0.0.0.0",
          listen_port: 10443,
          enabled: true,
          tls_mode: "pin_or_ca",
          certificate_id: null,
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
