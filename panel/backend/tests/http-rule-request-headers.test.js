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
    const port = await getFreePort();
    const dataRoot = await fs.mkdtemp(path.join(os.tmpdir(), "panel-agent-info-"));
    const serverProcess = spawn(process.execPath, ["server.js"], {
      cwd: path.resolve(__dirname, ".."),
      env: {
        ...process.env,
        PANEL_ROLE: "agent",
        PANEL_BACKEND_HOST: "127.0.0.1",
        PANEL_BACKEND_PORT: String(port),
        PANEL_DATA_ROOT: dataRoot,
        PANEL_STORAGE_BACKEND: "json",
        PROXY_PASS_PROXY_HEADERS: "0",
      },
      stdio: ["ignore", "pipe", "pipe"],
    });

    let stderr = "";
    serverProcess.stderr.on("data", (chunk) => {
      stderr += String(chunk);
    });

    try {
      await waitForServer(
        `http://127.0.0.1:${port}/api/health`,
        serverProcess,
        () => stderr,
      );
      const response = await fetch(`http://127.0.0.1:${port}/api/info`);
      assert.equal(response.status, 200);

      const payload = await response.json();
      assert.equal(payload.proxy_headers_globally_disabled, true);
    } finally {
      if (serverProcess.exitCode === null) {
        serverProcess.kill("SIGTERM");
        await once(serverProcess, "exit");
      }
      await fs.rm(dataRoot, { recursive: true, force: true });
    }
  });
});
