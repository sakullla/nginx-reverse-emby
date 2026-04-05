"use strict";

const fc = require("fast-check");
const { spawn } = require("node:child_process");
const fsp = require("node:fs/promises");
const net = require("node:net");
const os = require("node:os");
const path = require("node:path");
const { once } = require("node:events");

const SQLITE_TARGET = ":memory:";
const safeString = fc.string({ maxLength: 50 }).map((s) => s.replace(/\0/g, ""));
const nonEmptyString = fc.string({ minLength: 1, maxLength: 50 }).map((s) => s.replace(/\0/g, ""));

function detectSqliteAvailability() {
  try {
    const storage = require("../storage-sqlite");
    storage.init(SQLITE_TARGET);
    storage.close();
    return true;
  } catch (_) {
    return false;
  }
}

function loadFreshStorage(modulePath, initArg) {
  const resolved = require.resolve(modulePath);
  delete require.cache[resolved];
  const storage = require(modulePath);
  if (initArg !== undefined) {
    storage.init(initArg);
  }
  return storage;
}

function closeQuietly(storage) {
  try {
    storage.close();
  } catch (_) {
    // ignore test teardown noise
  }
}

function dedupById(items, key = "id") {
  const map = new Map();
  for (const item of items) {
    map.set(item[key], item);
  }
  return [...map.values()];
}

function getNumRuns(suiteName, fallback) {
  const suiteKey = `PANEL_BACKEND_TEST_NUM_RUNS_${String(suiteName).toUpperCase()}`;
  const raw = process.env[suiteKey] || process.env.PANEL_BACKEND_TEST_NUM_RUNS;
  const parsed = Number.parseInt(raw, 10);
  return Number.isInteger(parsed) && parsed > 0 ? parsed : fallback;
}

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
    } catch (_) {
      // ignore connection failures until timeout
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error(`server did not become ready: ${url}`);
}

async function writeJson(filePath, value) {
  await fsp.mkdir(path.dirname(filePath), { recursive: true });
  await fsp.writeFile(filePath, JSON.stringify(value, null, 2), "utf8");
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
  const dataRoot = await fsp.mkdtemp(path.join(os.tmpdir(), "panel-backend-test-"));
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
  if (options?.relayListenersByAgentId && typeof options.relayListenersByAgentId === "object") {
    for (const [agentId, listeners] of Object.entries(options.relayListenersByAgentId)) {
      await writeJson(path.join(dataRoot, "relay_listeners", `${agentId}.json`), listeners);
    }
  }
  if (options?.versionPolicies) {
    await writeJson(path.join(dataRoot, "version_policies.json"), options.versionPolicies);
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
    await testFn({ baseUrl, dataRoot });
  } finally {
    if (serverProcess.exitCode === null) {
      serverProcess.kill("SIGTERM");
      await once(serverProcess, "exit");
    }
    await fsp.rm(dataRoot, { recursive: true, force: true });
  }
}

module.exports = {
  SQLITE_TARGET,
  canRunSqlite: detectSqliteAvailability(),
  safeString,
  nonEmptyString,
  loadFreshStorage,
  closeQuietly,
  dedupById,
  getNumRuns,
  withBackendServer,
};
