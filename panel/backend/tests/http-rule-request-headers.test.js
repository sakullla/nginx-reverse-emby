"use strict";

const { describe, it } = require("node:test");
const assert = require("node:assert/strict");
const { spawn } = require("node:child_process");
const fs = require("node:fs/promises");
const net = require("node:net");
const os = require("node:os");
const path = require("node:path");
const { once } = require("node:events");
const {
  normalizeRuleRequestHeaders,
} = require("../http-rule-request-headers");

async function getFreePort() {
  const server = net.createServer();
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const address = server.address();
  const port = address && typeof address === "object" ? address.port : 0;
  await new Promise((resolve, reject) => {
    server.close((err) => (err ? reject(err) : resolve()));
  });
  if (!port) {
    throw new Error("failed to get free port");
  }
  return port;
}

async function waitForServer(url, serverProcess, readStderr) {
  const deadline = Date.now() + 10000;
  while (Date.now() < deadline) {
    if (serverProcess.exitCode !== null) {
      throw new Error(
        `server exited early with code ${serverProcess.exitCode}: ${readStderr()}`,
      );
    }
    try {
      const response = await fetch(url);
      if (response.ok) {
        return;
      }
    } catch (error) {
      // ignore connection failures until timeout
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error(`server did not become ready: ${url}`);
}

async function writeJson(filePath, value) {
  await fs.mkdir(path.dirname(filePath), { recursive: true });
  await fs.writeFile(filePath, JSON.stringify(value, null, 2), "utf8");
}

async function withBackendServer(options, testFn) {
  const port = await getFreePort();
  const dataRoot = await fs.mkdtemp(path.join(os.tmpdir(), "panel-http-rule-"));
  const envOverrides = options?.env || {};

  if (options?.proxyRules) {
    await writeJson(path.join(dataRoot, "proxy_rules.json"), options.proxyRules);
  }
  if (options?.agents) {
    await writeJson(path.join(dataRoot, "agents.json"), options.agents);
  }

  const serverProcess = spawn(process.execPath, ["server.js"], {
    cwd: path.resolve(__dirname, ".."),
    env: {
      ...process.env,
      PANEL_BACKEND_HOST: "127.0.0.1",
      PANEL_BACKEND_PORT: String(port),
      PANEL_DATA_ROOT: dataRoot,
      PANEL_STORAGE_BACKEND: "json",
      ...envOverrides,
    },
    stdio: ["ignore", "pipe", "pipe"],
  });

  const baseUrl = `http://127.0.0.1:${port}`;
  let stderr = "";
  serverProcess.stderr.on("data", (chunk) => {
    stderr += String(chunk);
  });

  try {
    await waitForServer(
      `${baseUrl}${options?.readyPath || "/api/info"}`,
      serverProcess,
      () => stderr,
    );
    await testFn({ baseUrl });
  } finally {
    if (serverProcess.exitCode === null) {
      serverProcess.kill("SIGTERM");
      await once(serverProcess, "exit");
    }
    await fs.rm(dataRoot, { recursive: true, force: true });
  }
}

describe("HTTP rule request header normalization", () => {
  it("fills defaults for pass_proxy_headers, user_agent, and custom_headers", () => {
    const rule = normalizeRuleRequestHeaders({}, {});

    assert.equal(rule.pass_proxy_headers, true);
    assert.equal(rule.user_agent, "");
    assert.deepEqual(rule.custom_headers, []);
  });

  it("normalizes and validates fallback user_agent values", () => {
    const rule = normalizeRuleRequestHeaders({}, { user_agent: "  Mozilla/5.0  " });
    assert.equal(rule.user_agent, "Mozilla/5.0");

    assert.throws(
      () => normalizeRuleRequestHeaders({}, { user_agent: "bad\u0007ua" }),
      /control characters/i,
    );
  });

  it("rejects custom User-Agent rows", () => {
    assert.throws(
      () =>
        normalizeRuleRequestHeaders(
          {
            custom_headers: [{ name: "User-Agent", value: "bad" }],
          },
          {},
        ),
      /User-Agent/i,
    );
  });

  it("rejects case-insensitive duplicate custom header names", () => {
    assert.throws(
      () =>
        normalizeRuleRequestHeaders(
          {
            custom_headers: [
              { name: "x-forwarded-for", value: "1.2.3.4" },
              { name: "X-Forwarded-For", value: "5.6.7.8" },
            ],
          },
          {},
        ),
      /duplicate/i,
    );
  });

  it("exposes proxy_headers_globally_disabled on /api/info in agent mode", async () => {
    await withBackendServer(
      {
        env: {
          PANEL_ROLE: "agent",
          PROXY_PASS_PROXY_HEADERS: "0",
        },
        readyPath: "/api/health",
      },
      async ({ baseUrl }) => {
        const response = await fetch(`${baseUrl}/api/info`);
        assert.equal(response.status, 200);

        const payload = await response.json();
        assert.equal(payload.proxy_headers_globally_disabled, true);
      },
    );
  });

  it("exposes proxy_headers_globally_disabled on /api/info in master mode", async () => {
    await withBackendServer(
      {
        env: {
          PANEL_ROLE: "master",
          PROXY_PASS_PROXY_HEADERS: "0",
        },
      },
      async ({ baseUrl }) => {
        const response = await fetch(`${baseUrl}/api/info`);
        assert.equal(response.status, 200);

        const payload = await response.json();
        assert.equal(payload.proxy_headers_globally_disabled, true);
      },
    );
  });

  it("treats unset PROXY_PASS_PROXY_HEADERS as globally disabled on /api/info", async () => {
    await withBackendServer(
      {
        env: {
          PANEL_ROLE: "master",
        },
      },
      async ({ baseUrl }) => {
        const response = await fetch(`${baseUrl}/api/info`);
        assert.equal(response.status, 200);

        const payload = await response.json();
        assert.equal(payload.proxy_headers_globally_disabled, true);
      },
    );
  });

  it("backfills request-header defaults on GET /agent-api/rules for legacy stored rules", async () => {
    await withBackendServer(
      {
        env: {
          PANEL_ROLE: "agent",
        },
        readyPath: "/agent-api/health",
        proxyRules: [
          {
            id: 1,
            frontend_url: "https://frontend.example.com",
            backend_url: "http://backend.internal:8096",
            enabled: true,
            tags: [],
            proxy_redirect: true,
            revision: 2,
          },
        ],
      },
      async ({ baseUrl }) => {
        const response = await fetch(`${baseUrl}/agent-api/rules`);
        assert.equal(response.status, 200);

        const payload = await response.json();
        const rule = payload.rules[0];
        assert.equal(rule.pass_proxy_headers, true);
        assert.equal(rule.user_agent, "");
        assert.deepEqual(rule.custom_headers, []);
      },
    );
  });

  it("backfills request-header defaults on master rule GET APIs for legacy stored rules", async () => {
    await withBackendServer(
      {
        env: {
          PANEL_ROLE: "master",
        },
        proxyRules: [
          {
            id: 1,
            frontend_url: "https://frontend.example.com",
            backend_url: "http://backend.internal:8096",
            enabled: true,
            tags: [],
            proxy_redirect: true,
            revision: 4,
          },
        ],
      },
      async ({ baseUrl }) => {
        const legacyResponse = await fetch(`${baseUrl}/api/rules`);
        assert.equal(legacyResponse.status, 200);
        const legacyPayload = await legacyResponse.json();
        const legacyRule = legacyPayload.rules[0];
        assert.equal(legacyRule.pass_proxy_headers, true);
        assert.equal(legacyRule.user_agent, "");
        assert.deepEqual(legacyRule.custom_headers, []);

        const agentResponse = await fetch(`${baseUrl}/api/agents/local/rules`);
        assert.equal(agentResponse.status, 200);
        const agentPayload = await agentResponse.json();
        const agentRule = agentPayload.rules[0];
        assert.equal(agentRule.pass_proxy_headers, true);
        assert.equal(agentRule.user_agent, "");
        assert.deepEqual(agentRule.custom_headers, []);
      },
    );
  });
});
