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

function createDeterministicChildEnv(envOverrides, port, dataRoot) {
  const passthroughKeys = [
    "PATH",
    "Path",
    "PATHEXT",
    "SystemRoot",
    "SYSTEMROOT",
    "WINDIR",
    "windir",
    "COMSPEC",
    "TEMP",
    "TMP",
    "HOME",
    "USERPROFILE",
    "APPDATA",
    "LOCALAPPDATA",
    "ProgramData",
    "PROGRAMDATA",
  ];
  const baseEnv = {};
  for (const key of passthroughKeys) {
    if (process.env[key] !== undefined) {
      baseEnv[key] = process.env[key];
    }
  }
  return {
    ...baseEnv,
    API_TOKEN: "",
    AGENT_API_TOKEN: "",
    MASTER_REGISTER_TOKEN: "",
    PANEL_REGISTER_TOKEN: "",
    PANEL_BACKEND_HOST: "127.0.0.1",
    PANEL_BACKEND_PORT: String(port),
    PANEL_DATA_ROOT: dataRoot,
    PANEL_STORAGE_BACKEND: "json",
    PROXY_PASS_PROXY_HEADERS: "",
    MASTER_LOCAL_AGENT_ENABLED: "1",
    ...envOverrides,
  };
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
  if (options?.agentRulesByAgentId && typeof options.agentRulesByAgentId === "object") {
    for (const [agentId, rules] of Object.entries(options.agentRulesByAgentId)) {
      await writeJson(path.join(dataRoot, "agent_rules", `${agentId}.json`), rules);
    }
  }

  const serverProcess = spawn(process.execPath, ["server.js"], {
    cwd: path.resolve(__dirname, ".."),
    env: createDeterministicChildEnv(envOverrides, port, dataRoot),
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

function toPosixPath(filePath) {
  return String(filePath).replace(/\\/g, "/");
}

function resolveShCommand() {
  if (process.platform !== "win32") {
    return "sh";
  }
  const candidates = [
    "C:/Program Files/Git/bin/sh.exe",
    "C:/Program Files/Git/usr/bin/sh.exe",
  ];
  const found = candidates.find((candidate) => require("node:fs").existsSync(candidate));
  if (!found) {
    throw new Error("unable to locate sh.exe for generator test");
  }
  return found;
}

async function generateNginxConfig(options = {}) {
  const tempRoot = await fs.mkdtemp(path.join(os.tmpdir(), "nginx-rule-gen-"));
  const dataRoot = path.join(tempRoot, "data");
  const dynamicDir = path.join(tempRoot, "conf.d", "dynamic");
  const streamDynamicDir = path.join(tempRoot, "stream-conf.d", "dynamic");
  const directCertDir = path.join(tempRoot, "certs");
  const rulesJsonPath = path.join(dataRoot, "proxy_rules.json");
  const repoRoot = path.resolve(__dirname, "..", "..", "..");
  const scriptPath = toPosixPath(path.join(repoRoot, "docker", "25-dynamic-reverse-proxy.sh"));
  const templatePath = toPosixPath(path.join(repoRoot, "docker", "default.conf.template"));
  const directNoTlsTemplatePath = toPosixPath(
    path.join(repoRoot, "docker", "default.direct.no_tls.conf.template"),
  );
  const directTlsTemplatePath = toPosixPath(
    path.join(repoRoot, "docker", "default.direct.tls.conf.template"),
  );

  await fs.mkdir(dynamicDir, { recursive: true });
  await fs.mkdir(streamDynamicDir, { recursive: true });
  await writeJson(rulesJsonPath, options.proxyRules || []);

  const childEnv = {
    ...process.env,
    PANEL_DATA_ROOT: toPosixPath(dataRoot),
    PANEL_RULES_JSON: toPosixPath(rulesJsonPath),
    NRE_DYNAMIC_DIR: toPosixPath(dynamicDir),
    NRE_STREAM_DYNAMIC_DIR: toPosixPath(streamDynamicDir),
    DIRECT_CERT_DIR: toPosixPath(directCertDir),
    NRE_TEMPLATE_FILE: templatePath,
    NRE_DIRECT_NO_TLS_TEMPLATE_FILE: directNoTlsTemplatePath,
    NRE_DIRECT_TLS_TEMPLATE_FILE: directTlsTemplatePath,
    NGINX_LOCAL_RESOLVERS: "127.0.0.1",
    NGINX_ENABLE_IPV6: "0",
    NGINX_ENTRYPOINT_QUIET_LOGS: "1",
    PROXY_DEPLOY_MODE: "front_proxy",
    ...(options.env || {}),
  };

  if (!Object.prototype.hasOwnProperty.call(options.env || {}, "PROXY_PASS_PROXY_HEADERS")) {
    delete childEnv.PROXY_PASS_PROXY_HEADERS;
  }

  const child = spawn(resolveShCommand(), [scriptPath], {
    cwd: repoRoot,
    env: childEnv,
    stdio: ["ignore", "pipe", "pipe"],
  });

  let stdout = "";
  let stderr = "";
  child.stdout.on("data", (chunk) => {
    stdout += String(chunk);
  });
  child.stderr.on("data", (chunk) => {
    stderr += String(chunk);
  });

  try {
    const [exitCode, signal] = await once(child, "exit");
    if (exitCode !== 0) {
      throw new Error(
        `generator exited with code ${exitCode} (${signal || "no-signal"}): ${stderr || stdout}`,
      );
    }

    const generatedFiles = await fs.readdir(dynamicDir);
    assert.equal(generatedFiles.length, 1, `expected one generated config, got: ${generatedFiles.join(", ")}`);

    return {
      stdout,
      stderr,
      config: await fs.readFile(path.join(dynamicDir, generatedFiles[0]), "utf8"),
    };
  } finally {
    await fs.rm(tempRoot, { recursive: true, force: true });
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

  it("rejects present non-array custom_headers payloads", () => {
    assert.throws(
      () =>
        normalizeRuleRequestHeaders(
          {
            custom_headers: "x-test: value",
          },
          {},
        ),
      /array/i,
    );
  });

  it("rejects non-boolean pass_proxy_headers payloads", () => {
    assert.throws(
      () =>
        normalizeRuleRequestHeaders(
          {
            pass_proxy_headers: "false",
          },
          {},
        ),
      /boolean/i,
    );

    assert.throws(
      () =>
        normalizeRuleRequestHeaders(
          {
            pass_proxy_headers: "0",
          },
          {},
        ),
      /boolean/i,
    );
  });

  it("rejects non-string user_agent payloads", () => {
    assert.throws(
      () =>
        normalizeRuleRequestHeaders(
          {
            user_agent: {},
          },
          {},
        ),
      /string/i,
    );
  });

  it("rejects explicit null user_agent payloads", () => {
    assert.throws(
      () =>
        normalizeRuleRequestHeaders(
          {
            user_agent: null,
          },
          {},
        ),
      /string/i,
    );
  });

  it("rejects non-string custom header values", () => {
    assert.throws(
      () =>
        normalizeRuleRequestHeaders(
          {
            custom_headers: [{ name: "x-test", value: [] }],
          },
          {},
        ),
      /string/i,
    );
  });

  it("rejects non-string custom header names", () => {
    assert.throws(
      () =>
        normalizeRuleRequestHeaders(
          {
            custom_headers: [{ name: 123, value: "ok" }],
          },
          {},
        ),
      /string/i,
    );

    assert.throws(
      () =>
        normalizeRuleRequestHeaders(
          {
            custom_headers: [{ name: true, value: "ok" }],
          },
          {},
        ),
      /string/i,
    );
  });

  it("rejects explicit null custom header values", () => {
    assert.throws(
      () =>
        normalizeRuleRequestHeaders(
          {
            custom_headers: [{ name: "x-test", value: null }],
          },
          {},
        ),
      /string/i,
    );
  });

  it("generator honors rule-level proxy headers when PROXY_PASS_PROXY_HEADERS is unset", async () => {
    const { config } = await generateNginxConfig({
      proxyRules: [
        {
          frontend_url: "https://frontend.example.com",
          backend_url: "http://backend.internal:8096",
          proxy_redirect: true,
          pass_proxy_headers: true,
        },
      ],
    });

    assert.match(config, /proxy_set_header X-Real-IP "\$remote_addr";/);
    assert.match(config, /proxy_set_header X-Forwarded-For "\$proxy_add_x_forwarded_for";/);
    assert.match(config, /proxy_set_header X-Forwarded-Proto "\$scheme";/);
  });

  it("generator treats PROXY_PASS_PROXY_HEADERS as a global disable override", async () => {
    const { config } = await generateNginxConfig({
      env: {
        PROXY_PASS_PROXY_HEADERS: "0",
      },
      proxyRules: [
        {
          frontend_url: "https://frontend.example.com",
          backend_url: "http://backend.internal:8096",
          proxy_redirect: true,
          pass_proxy_headers: true,
          custom_headers: [{ name: "X-Test", value: "still-there" }],
        },
      ],
    });

    assert.doesNotMatch(config, /proxy_set_header X-Real-IP /);
    assert.doesNotMatch(config, /proxy_set_header X-Forwarded-For /);
    assert.match(config, /proxy_set_header X-Test "still-there";/);
  });

  it("generator renders literal header values safely for nginx", async () => {
    const { config } = await generateNginxConfig({
      proxyRules: [
        {
          frontend_url: "https://frontend.example.com",
          backend_url: "http://backend.internal:8096",
          proxy_redirect: true,
          pass_proxy_headers: true,
          user_agent: 'Agent $browser "quoted" \\ slash',
          custom_headers: [
            {
              name: "X-Test",
              value: 'start$evil\r\n        proxy_set_header X-Evil "oops";\u0007end\\tail',
            },
          ],
        },
      ],
    });

    const userAgentLine = config
      .split(/\r?\n/)
      .find((line) => line.includes("proxy_set_header User-Agent "));
    const customHeaderLine = config
      .split(/\r?\n/)
      .find((line) => line.includes("proxy_set_header X-Test "));

    assert.ok(userAgentLine, "expected User-Agent header line");
    assert.ok(customHeaderLine, "expected X-Test header line");
    assert.match(config, /map "" \$nre_literal_dollar_[A-Za-z0-9_]+ \{\s*default "\$";\s*\}/s);
    assert.match(userAgentLine, /\$\{nre_literal_dollar_[A-Za-z0-9_]+\}browser/);
    assert.match(customHeaderLine, /\$\{nre_literal_dollar_[A-Za-z0-9_]+\}evil/);
    assert.ok(userAgentLine.includes('\\"quoted\\"'));
    assert.ok(userAgentLine.includes("\\\\ slash"));
    assert.ok(customHeaderLine.includes('\\"oops\\"'));
    assert.match(customHeaderLine, /end\\\\+tail/);
    assert.doesNotMatch(customHeaderLine, /[\u0000-\u001F\u007F]/);
    assert.doesNotMatch(config, /^\s*proxy_set_header X-Evil /m);
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

  it("treats unset PROXY_PASS_PROXY_HEADERS as not globally disabled on /api/info", async () => {
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
        assert.equal(payload.proxy_headers_globally_disabled, false);
      },
    );
  });

  it("treats unrecognized PROXY_PASS_PROXY_HEADERS values as not globally disabled on /api/info", async () => {
    await withBackendServer(
      {
        env: {
          PANEL_ROLE: "master",
          PROXY_PASS_PROXY_HEADERS: "banana",
        },
      },
      async ({ baseUrl }) => {
        const response = await fetch(`${baseUrl}/api/info`);
        assert.equal(response.status, 200);

        const payload = await response.json();
        assert.equal(payload.proxy_headers_globally_disabled, false);
      },
    );
  });

  it("keeps proxy headers enabled on /api/info when PROXY_PASS_PROXY_HEADERS is truthy", async () => {
    await withBackendServer(
      {
        env: {
          PANEL_ROLE: "master",
          PROXY_PASS_PROXY_HEADERS: "true",
        },
      },
      async ({ baseUrl }) => {
        const response = await fetch(`${baseUrl}/api/info`);
        assert.equal(response.status, 200);

        const payload = await response.json();
        assert.equal(payload.proxy_headers_globally_disabled, false);
      },
    );
  });

  it("isolates backend child env from ambient proxy-header and local-agent overrides", async () => {
    const previousProxyPassProxyHeaders = process.env.PROXY_PASS_PROXY_HEADERS;
    const previousMasterLocalAgentEnabled = process.env.MASTER_LOCAL_AGENT_ENABLED;
    process.env.PROXY_PASS_PROXY_HEADERS = "true";
    process.env.MASTER_LOCAL_AGENT_ENABLED = "0";
    try {
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
          assert.equal(payload.proxy_headers_globally_disabled, false);
          assert.equal(payload.local_agent_enabled, true);
        },
      );
    } finally {
      if (previousProxyPassProxyHeaders === undefined) {
        delete process.env.PROXY_PASS_PROXY_HEADERS;
      } else {
        process.env.PROXY_PASS_PROXY_HEADERS = previousProxyPassProxyHeaders;
      }
      if (previousMasterLocalAgentEnabled === undefined) {
        delete process.env.MASTER_LOCAL_AGENT_ENABLED;
      } else {
        process.env.MASTER_LOCAL_AGENT_ENABLED = previousMasterLocalAgentEnabled;
      }
    }
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

  it("backfills request-header defaults in heartbeat sync payloads for legacy agent rules", async () => {
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
            created_at: "2026-04-01T00:00:00.000Z",
            updated_at: "2026-04-01T00:00:00.000Z",
          },
        ],
        agentRulesByAgentId: {
          "remote-agent-1": [
            {
              id: 1,
              frontend_url: "https://frontend.example.com",
              backend_url: "http://backend.internal:8096",
              enabled: true,
              tags: [],
              proxy_redirect: true,
              revision: 8,
            },
          ],
        },
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
          }),
        });
        assert.equal(response.status, 200);

        const payload = await response.json();
        const rule = payload.sync.rules[0];
        assert.equal(rule.pass_proxy_headers, true);
        assert.equal(rule.user_agent, "");
        assert.deepEqual(rule.custom_headers, []);
      },
    );
  });
});
