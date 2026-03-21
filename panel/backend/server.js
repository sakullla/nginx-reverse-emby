#!/usr/bin/env node
"use strict";

const fs = require("fs");
const http = require("http");
const os = require("os");
const path = require("path");
const crypto = require("crypto");
const { spawnSync } = require("child_process");

const HOST = process.env.PANEL_BACKEND_HOST || "127.0.0.1";
const PORT = Number(process.env.PANEL_BACKEND_PORT || "18081");
const DATA_ROOT =
  process.env.PANEL_DATA_ROOT || "/opt/nginx-reverse-emby/panel/data";
const ROLE = normalizeRole(process.env.PANEL_ROLE || "master");
const RULES_JSON =
  process.env.PANEL_RULES_JSON || path.join(DATA_ROOT, "proxy_rules.json");
const RULES_CSV =
  process.env.PANEL_RULES_FILE || path.join(DATA_ROOT, "proxy_rules.csv");
const AGENTS_JSON =
  process.env.PANEL_AGENTS_JSON || path.join(DATA_ROOT, "agents.json");
const AGENT_RULES_DIR =
  process.env.PANEL_AGENT_RULES_DIR || path.join(DATA_ROOT, "agent_rules");
const GENERATOR_SCRIPT =
  process.env.PANEL_GENERATOR_SCRIPT ||
  "/docker-entrypoint.d/25-dynamic-reverse-proxy.sh";
const NGINX_BIN = process.env.PANEL_NGINX_BIN || "nginx";
const APPLY_COMMAND = process.env.PANEL_APPLY_COMMAND || "";
const APPLY_COMMAND_ARGS = parseJsonArray(process.env.PANEL_APPLY_ARGS, []);
const AUTO_APPLY = /^(1|true|yes|on)$/i.test(
  process.env.PANEL_AUTO_APPLY || "1",
);
const NGINX_STATUS_URL =
  process.env.NGINX_STATUS_URL || "http://127.0.0.1:18080/nginx_status";
const PANEL_TOKEN = process.env.API_TOKEN || "";
const MASTER_REGISTER_TOKEN =
  process.env.MASTER_REGISTER_TOKEN ||
  process.env.PANEL_REGISTER_TOKEN ||
  process.env.API_TOKEN ||
  "";
const AGENT_API_TOKEN = process.env.AGENT_API_TOKEN || process.env.API_TOKEN || "";
const AGENT_NAME = process.env.AGENT_NAME || os.hostname();
const AGENT_PUBLIC_URL = trimSlash(process.env.AGENT_PUBLIC_URL || "");
const AGENT_VERSION = process.env.AGENT_VERSION || "1";
const AGENT_TAGS = normalizeTags(
  (process.env.AGENT_TAGS || "")
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean),
);
const AGENT_HEARTBEAT_TIMEOUT_MS = Number(
  process.env.AGENT_HEARTBEAT_TIMEOUT_MS || "90000",
);
const AGENT_POLL_INTERVAL_MS = Number(
  process.env.AGENT_POLL_INTERVAL_MS || "10000",
);
const LOCAL_AGENT_ENABLED =
  ROLE === "master" &&
  !/^(0|false|no|off)$/i.test(process.env.MASTER_LOCAL_AGENT_ENABLED || "1");
const LOCAL_AGENT_ID = process.env.MASTER_LOCAL_AGENT_ID || "local";
const LOCAL_AGENT_NAME =
  process.env.MASTER_LOCAL_AGENT_NAME || `${os.hostname()} (����)`;
const LOCAL_AGENT_URL = trimSlash(process.env.MASTER_LOCAL_AGENT_URL || "");
const LOCAL_AGENT_TAGS = normalizeTags(
  (process.env.MASTER_LOCAL_AGENT_TAGS || "local")
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean),
);

const PROJECT_ROOT = path.resolve(__dirname, "..", "..");
const PUBLIC_AGENT_ASSETS = {
  "light-agent.js": {
    file: path.join(PROJECT_ROOT, "scripts", "light-agent.js"),
    contentType: "application/javascript; charset=utf-8",
  },
  "light-agent-apply.sh": {
    file: path.join(PROJECT_ROOT, "scripts", "light-agent-apply.sh"),
    contentType: "application/x-sh; charset=utf-8",
  },
  "25-dynamic-reverse-proxy.sh": {
    file: path.join(PROJECT_ROOT, "docker", "25-dynamic-reverse-proxy.sh"),
    contentType: "application/x-sh; charset=utf-8",
  },
  "default.conf.template": {
    file: path.join(PROJECT_ROOT, "docker", "default.conf.template"),
    contentType: "text/plain; charset=utf-8",
  },
  "default.direct.no_tls.conf.template": {
    file: path.join(PROJECT_ROOT, "docker", "default.direct.no_tls.conf.template"),
    contentType: "text/plain; charset=utf-8",
  },
  "default.direct.tls.conf.template": {
    file: path.join(PROJECT_ROOT, "docker", "default.direct.tls.conf.template"),
    contentType: "text/plain; charset=utf-8",
  },
};

function normalizeRole(value) {
  const role = String(value || "master").trim().toLowerCase();
  return role === "agent" ? "agent" : "master";
}

function trimSlash(value) {
  return String(value || "").trim().replace(/\/+$/, "");
}

function nowIso() {
  return new Date().toISOString();
}

function ensureDataDir() {
  fs.mkdirSync(DATA_ROOT, { recursive: true });
  fs.mkdirSync(AGENT_RULES_DIR, { recursive: true });
}

function readJsonFile(file, fallback) {
  try {
    if (!fs.existsSync(file)) return fallback;
    return JSON.parse(fs.readFileSync(file, "utf8"));
  } catch (err) {
    console.error(`Error reading ${file}:`, err);
    return fallback;
  }
}

function writeJsonFile(file, value) {
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, JSON.stringify(value, null, 2), "utf8");
}

function sendJson(res, statusCode, payload) {
  const body = Buffer.from(JSON.stringify(payload), "utf8");
  res.writeHead(statusCode, {
    "Content-Type": "application/json; charset=utf-8",
    "Content-Length": String(body.length),
  });
  res.end(body);
}

function sendText(res, statusCode, body, contentType = "text/plain; charset=utf-8") {
  const payload = Buffer.from(String(body), "utf8");
  res.writeHead(statusCode, {
    "Content-Type": contentType,
    "Content-Length": String(payload.length),
    "Cache-Control": "no-store",
  });
  res.end(payload);
}

function errorPayload(message, details) {
  const payload = { ok: false, message };
  if (details) payload.details = details;
  return payload;
}

function escapeForDoubleQuotedShell(value) {
  return String(value || "")
    .replace(/\\/g, "\\\\")
    .replace(/"/g, '\\"')
    .replace(/\$/g, "\\$")
    .replace(/`/g, "\\`");
}

function getRequestBaseUrl(req) {
  const proto = String(req.headers["x-forwarded-proto"] || "http")
    .split(",")[0]
    .trim() || "http";
  const host = String(req.headers["x-forwarded-host"] || req.headers.host || "")
    .split(",")[0]
    .trim();
  if (!host) {
    return `${proto}://${HOST}:${PORT}`;
  }
  return `${proto}://${host}`;
}

function readPublicAgentAsset(assetName) {
  const asset = PUBLIC_AGENT_ASSETS[assetName];
  if (!asset) return null;
  if (!fs.existsSync(asset.file)) {
    throw new Error(`asset file not found: ${asset.file}`);
  }
  return {
    ...asset,
    body: fs.readFileSync(asset.file, "utf8"),
  };
}

function buildJoinAgentScript(req) {
  const joinScriptPath = path.join(PROJECT_ROOT, "scripts", "join-agent.sh");
  const baseUrl = getRequestBaseUrl(req);
  const assetBaseUrl = `${baseUrl}/panel-api/public/agent-assets`;
  return fs
    .readFileSync(joinScriptPath, "utf8")
    .replace(/__DEFAULT_MASTER_URL__/g, escapeForDoubleQuotedShell(baseUrl))
    .replace(/__DEFAULT_ASSET_BASE_URL__/g, escapeForDoubleQuotedShell(assetBaseUrl));
}

function parseJsonBody(req) {
  return new Promise((resolve, reject) => {
    let raw = "";
    req.setEncoding("utf8");
    req.on("data", (chunk) => {
      raw += chunk;
      if (raw.length > 1024 * 1024) {
        reject(new Error("request body too large"));
        req.destroy();
      }
    });
    req.on("end", () => {
      if (!raw) {
        resolve({});
        return;
      }
      try {
        resolve(JSON.parse(raw));
      } catch {
        reject(new Error("invalid JSON body"));
      }
    });
    req.on("error", (err) => reject(err));
  });
}

function validateUrl(value) {
  try {
    const u = new URL(value);
    return u.protocol === "http:" || u.protocol === "https:";
  } catch {
    return false;
  }
}

function parseJsonArray(value, fallback = []) {
  if (!value) return fallback;
  try {
    const parsed = JSON.parse(value);
    return Array.isArray(parsed) ? parsed.map((item) => String(item)) : fallback;
  } catch {
    return fallback;
  }
}

function normalizeTags(tags) {
  return [
    ...new Set(
      (Array.isArray(tags) ? tags : [])
        .map((item) => String(item || "").trim())
        .filter(Boolean),
    ),
  ];
}

function normalizeRulePayload(body, fallback = {}, suggestedId = null) {
  const frontend =
    body.frontend_url !== undefined
      ? String(body.frontend_url).trim()
      : fallback.frontend_url;
  const backend =
    body.backend_url !== undefined
      ? String(body.backend_url).trim()
      : fallback.backend_url;

  if (!validateUrl(frontend) || !validateUrl(backend)) {
    throw new Error("frontend_url and backend_url must be valid http/https URLs");
  }

  const parsedId =
    body.id !== undefined
      ? Number(body.id)
      : fallback.id !== undefined
        ? Number(fallback.id)
        : Number(suggestedId);

  return {
    id:
      Number.isFinite(parsedId) && parsedId > 0
        ? parsedId
        : Number(suggestedId) || 1,
    frontend_url: frontend,
    backend_url: backend,
    enabled:
      body.enabled !== undefined ? !!body.enabled : fallback.enabled !== false,
    tags:
      body.tags !== undefined ? normalizeTags(body.tags) : normalizeTags(fallback.tags || []),
    proxy_redirect:
      body.proxy_redirect !== undefined
        ? !!body.proxy_redirect
        : fallback.proxy_redirect !== false,
  };
}
function migrateCsvToJson() {
  if (!fs.existsSync(RULES_JSON) && fs.existsSync(RULES_CSV)) {
    console.log("Migrating proxy_rules.csv to proxy_rules.json...");
    try {
      const raw = fs.readFileSync(RULES_CSV, "utf8");
      const lines = raw.split(/\r?\n/);
      const rules = [];
      let id = 1;

      for (const lineRaw of lines) {
        const line = lineRaw.trim();
        if (!line || line.startsWith("#")) continue;
        const commaIndex = line.indexOf(",");
        if (commaIndex === -1) continue;
        const frontend = line.slice(0, commaIndex).trim();
        const backend = line.slice(commaIndex + 1).trim();
        if (!frontend || !backend) continue;
        rules.push({
          id: id++,
          frontend_url: frontend,
          backend_url: backend,
          enabled: true,
          tags: [],
          proxy_redirect: true,
        });
      }

      writeJsonFile(RULES_JSON, rules);
      console.log(`Migration complete. ${rules.length} rules migrated.`);
    } catch (err) {
      console.error("Migration failed:", err);
    }
  }
}

function getRuleFileForAgent(agentId) {
  if (agentId === LOCAL_AGENT_ID) return RULES_JSON;
  return path.join(AGENT_RULES_DIR, `${agentId}.json`);
}

function loadRulesForAgent(agentId) {
  if (agentId === LOCAL_AGENT_ID) {
    migrateCsvToJson();
  }
  return readJsonFile(getRuleFileForAgent(agentId), []);
}

function saveRulesForAgent(agentId, rules) {
  writeJsonFile(getRuleFileForAgent(agentId), rules);
}

function deleteRulesForAgent(agentId) {
  const file = getRuleFileForAgent(agentId);
  if (agentId === LOCAL_AGENT_ID) {
    writeJsonFile(file, []);
    return;
  }
  if (fs.existsSync(file)) fs.unlinkSync(file);
}

function loadRegisteredAgents() {
  return readJsonFile(AGENTS_JSON, []);
}

function saveRegisteredAgents(agents) {
  writeJsonFile(AGENTS_JSON, agents);
}

function normalizeRevision(value) {
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : 0;
}

function ensureAgentState(agent) {
  agent.mode = agent.mode || "pull";
  agent.desired_revision = normalizeRevision(agent.desired_revision);
  agent.current_revision = normalizeRevision(agent.current_revision);
  agent.last_apply_status = agent.last_apply_status || null;
  agent.last_apply_message = agent.last_apply_message || "";
  agent.last_reported_stats = agent.last_reported_stats || null;
  return agent;
}

function getAgentStatus(agent) {
  if (agent.is_local) return "online";
  const lastSeen = Date.parse(agent.last_seen_at || "");
  if (!lastSeen) return "offline";
  return Date.now() - lastSeen <= AGENT_HEARTBEAT_TIMEOUT_MS ? "online" : "offline";
}

function makeLocalAgent() {
  if (!LOCAL_AGENT_ENABLED) return null;
  const timestamp = nowIso();
  return {
    id: LOCAL_AGENT_ID,
    name: LOCAL_AGENT_NAME,
    agent_url: LOCAL_AGENT_URL,
    version: AGENT_VERSION,
    tags: LOCAL_AGENT_TAGS,
    mode: "local",
    desired_revision: 0,
    current_revision: 0,
    last_apply_status: "success",
    last_apply_message: "",
    last_reported_stats: null,
    status: "online",
    last_seen_at: timestamp,
    created_at: timestamp,
    updated_at: timestamp,
    is_local: true,
  };
}

function sanitizeAgent(agent) {
  const hydrated = ensureAgentState({ ...agent });
  return {
    id: String(hydrated.id),
    name: String(hydrated.name || "").trim(),
    agent_url: trimSlash(hydrated.agent_url || ""),
    version: String(hydrated.version || ""),
    tags: normalizeTags(hydrated.tags || []),
    mode: hydrated.mode,
    desired_revision: hydrated.desired_revision,
    current_revision: hydrated.current_revision,
    last_apply_status: hydrated.last_apply_status,
    last_apply_message: hydrated.last_apply_message,
    last_reported_stats: hydrated.last_reported_stats,
    last_seen_at: hydrated.last_seen_at || null,
    created_at: hydrated.created_at || null,
    updated_at: hydrated.updated_at || null,
    status: getAgentStatus(hydrated),
    error: hydrated.error || null,
    is_local: !!hydrated.is_local,
  };
}

function getAgentById(agentId) {
  if (LOCAL_AGENT_ENABLED && agentId === LOCAL_AGENT_ID) {
    return makeLocalAgent();
  }
  const agent = loadRegisteredAgents().find((item) => item.id === agentId) || null;
  return agent ? ensureAgentState(agent) : null;
}

function runChecked(command, args) {
  const result = spawnSync(command, args, {
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
  });
  if (result.error) {
    throw new Error(result.error.message);
  }
  if (result.status !== 0) {
    const details = (
      result.stderr ||
      result.stdout ||
      `exit code ${result.status}`
    ).trim();
    throw new Error(details);
  }
}

function applyNginxConfig() {
  if (APPLY_COMMAND) {
    runChecked(APPLY_COMMAND, APPLY_COMMAND_ARGS);
    return;
  }

  runChecked(GENERATOR_SCRIPT, []);
  runChecked(NGINX_BIN, ["-t"]);
  runChecked(NGINX_BIN, ["-s", "reload"]);
}

function isPanelAuthorized(req) {
  if (!PANEL_TOKEN) return true;
  const token = req.headers["x-panel-token"];
  return token === PANEL_TOKEN;
}

function isRegisterAuthorized(req, body) {
  if (!MASTER_REGISTER_TOKEN) return true;
  const headerToken = req.headers["x-register-token"];
  const bodyToken = body.register_token;
  return headerToken === MASTER_REGISTER_TOKEN || bodyToken === MASTER_REGISTER_TOKEN;
}

function isAgentAuthorized(req) {
  if (!AGENT_API_TOKEN) return true;
  const token = req.headers["x-agent-token"];
  return token === AGENT_API_TOKEN;
}

function parseStubStatus(data) {
  const lines = String(data || "").split("\n");
  const activeMatch = (lines[0] || "").match(/\d+/);
  const requestsLine = (lines[2] || "").trim().split(/\s+/);

  return {
    activeConnections: activeMatch ? activeMatch[0] : "0",
    totalRequests: requestsLine.length >= 3 ? requestsLine[2] : "0",
    status: "����",
  };
}

function getNginxStats() {
  return new Promise((resolve) => {
    http
      .get(NGINX_STATUS_URL, (res) => {
        let data = "";
        res.on("data", (chunk) => (data += chunk));
        res.on("end", () => {
          try {
            resolve(parseStubStatus(data));
          } catch (e) {
            resolve({
              activeConnections: "0",
              totalRequests: "0",
              status: "��ȡʧ��",
              error: e.message,
            });
          }
        });
      })
      .on("error", (err) => {
        resolve({
          activeConnections: "0",
          totalRequests: "0",
          status: "����ʧ��",
          error: err.message,
        });
      });
  });
}
async function hydrateAgents() {
  const registered = loadRegisteredAgents();
  const enriched = [];

  if (LOCAL_AGENT_ENABLED) {
    enriched.push(sanitizeAgent(makeLocalAgent()));
  }

  for (const agent of registered) {
    enriched.push(sanitizeAgent(agent));
  }

  return enriched;
}

async function getAgentStats(agentId) {
  if (agentId === LOCAL_AGENT_ID) {
    return getNginxStats();
  }
  const agent = getAgentById(agentId);
  if (!agent) throw new Error("agent not found");
  return (
    agent.last_reported_stats || {
      totalRequests: "0",
      status: getAgentStatus(agent) === "online" ? "??????" : "??",
    }
  );
}

async function syncAgentRules(agentId) {
  const agent = getAgentById(agentId);
  if (!agent) throw new Error("agent not found");
  const rules = loadRulesForAgent(agentId);
  if (agentId === LOCAL_AGENT_ID) {
    return { ok: true, rules };
  }
  const agents = loadRegisteredAgents();
  const index = agents.findIndex((item) => item.id === agentId);
  if (index === -1) throw new Error("agent not found");
  agents[index] = ensureAgentState(agents[index]);
  agents[index].desired_revision += 1;
  agents[index].updated_at = nowIso();
  saveRegisteredAgents(agents);
  return {
    ok: true,
    mode: agents[index].mode,
    desired_revision: agents[index].desired_revision,
    pending: true,
  };
}

async function applyAgent(agentId) {
  const agent = getAgentById(agentId);
  if (!agent) throw new Error("agent not found");

  if (agentId === LOCAL_AGENT_ID) {
    applyNginxConfig();
    return { ok: true, message: "applied" };
  }

  const sync = await syncAgentRules(agentId);
  return {
    ok: true,
    message: "waiting for agent heartbeat to apply",
    ...sync,
  };
}

function findRegisteredAgentByToken(token) {
  if (!token) return null;
  return loadRegisteredAgents().find((agent) => agent.agent_token === token) || null;
}

function getAgentHeartbeatResponse(agent) {
  const rules = loadRulesForAgent(agent.id);
  const hasUpdate = agent.current_revision < agent.desired_revision;
  return {
    ok: true,
    now: nowIso(),
    agent: sanitizeAgent(agent),
    heartbeat_interval_ms: AGENT_POLL_INTERVAL_MS,
    sync: {
      has_update: hasUpdate,
      desired_revision: agent.desired_revision,
      current_revision: agent.current_revision,
      rules: hasUpdate ? rules : undefined,
    },
  };
}

function getDefaultAgentId() {
  return LOCAL_AGENT_ENABLED ? LOCAL_AGENT_ID : null;
}

function extractAgentId(urlPath) {
  const match = urlPath.match(/^\/api\/agents\/([^/]+)(?:\/.*)?$/);
  return match ? decodeURIComponent(match[1]) : null;
}

function extractRuleId(urlPath) {
  const match = urlPath.match(/\/rules\/(\d+)$/);
  return match ? Number(match[1]) : null;
}

function loadOrInitRules(agentId) {
  const rules = loadRulesForAgent(agentId);
  return Array.isArray(rules) ? rules : [];
}

async function handleAgentApi(req, res) {
  const urlPath = (req.url || "").split("?")[0];

  if (!isAgentAuthorized(req)) {
    sendJson(res, 401, errorPayload("Unauthorized: Invalid or missing X-Agent-Token"));
    return;
  }

  if (req.method === "GET" && urlPath === "/agent-api/health") {
    const stats = await getNginxStats();
    sendJson(res, 200, {
      ok: true,
      now: nowIso(),
      agent: {
        name: AGENT_NAME,
        url: AGENT_PUBLIC_URL,
        version: AGENT_VERSION,
        role: "agent",
        tags: AGENT_TAGS,
      },
      stats,
    });
    return;
  }

  if (req.method === "GET" && urlPath === "/agent-api/info") {
    sendJson(res, 200, {
      ok: true,
      agent: {
        name: AGENT_NAME,
        url: AGENT_PUBLIC_URL,
        version: AGENT_VERSION,
        role: "agent",
        tags: AGENT_TAGS,
      },
    });
    return;
  }

  if (req.method === "GET" && urlPath === "/agent-api/rules") {
    sendJson(res, 200, { ok: true, rules: loadRulesForAgent(LOCAL_AGENT_ID) });
    return;
  }

  if (req.method === "PUT" && urlPath === "/agent-api/rules") {
    try {
      const body = await parseJsonBody(req);
      if (!Array.isArray(body.rules)) {
        sendJson(res, 400, errorPayload("rules must be an array"));
        return;
      }
      const rules = body.rules.map((rule, index) =>
        normalizeRulePayload(rule, {}, index + 1),
      );
      saveRulesForAgent(LOCAL_AGENT_ID, rules);
      sendJson(res, 200, { ok: true, rules });
    } catch (err) {
      sendJson(res, 400, errorPayload(String(err.message || err)));
    }
    return;
  }

  if (req.method === "POST" && urlPath === "/agent-api/apply") {
    try {
      applyNginxConfig();
      sendJson(res, 200, { ok: true, message: "applied" });
    } catch (err) {
      sendJson(
        res,
        400,
        errorPayload("failed to apply nginx config", String(err.message || err)),
      );
    }
    return;
  }

  sendJson(res, 404, errorPayload("not found"));
}
async function handleLegacyLocalRules(req, res, urlPath) {
  if (!LOCAL_AGENT_ENABLED) {
    sendJson(res, 404, errorPayload("local agent is disabled"));
    return;
  }

  if (req.method === "GET" && urlPath === "/api/rules") {
    sendJson(res, 200, { ok: true, rules: loadRulesForAgent(LOCAL_AGENT_ID) });
    return;
  }

  if (req.method === "POST" && urlPath === "/api/rules") {
    try {
      const body = await parseJsonBody(req);
      const rules = loadOrInitRules(LOCAL_AGENT_ID);
      const maxId = rules.reduce((max, rule) => Math.max(max, Number(rule.id) || 0), 0);
      const newRule = normalizeRulePayload(body, {}, maxId + 1);
      rules.push(newRule);
      saveRulesForAgent(LOCAL_AGENT_ID, rules);
      if (AUTO_APPLY) {
        try {
          await applyAgent(LOCAL_AGENT_ID);
        } catch (err) {
          sendJson(
            res,
            400,
            errorPayload(
              "rule saved but failed to apply nginx config",
              String(err.message || err),
            ),
          );
          return;
        }
      }
      sendJson(res, 201, { ok: true, rule: newRule });
    } catch (err) {
      sendJson(res, 400, errorPayload(String(err.message || err)));
    }
    return;
  }

  if (req.method === "PUT" && /^\/api\/rules\/\d+$/.test(urlPath)) {
    try {
      const ruleId = Number(urlPath.split("/").pop());
      const body = await parseJsonBody(req);
      const rules = loadOrInitRules(LOCAL_AGENT_ID);
      const index = rules.findIndex((rule) => Number(rule.id) === ruleId);
      if (index === -1) {
        sendJson(res, 404, errorPayload("rule id not found"));
        return;
      }
      rules[index] = normalizeRulePayload(body, rules[index], ruleId);
      saveRulesForAgent(LOCAL_AGENT_ID, rules);
      if (AUTO_APPLY) {
        try {
          await applyAgent(LOCAL_AGENT_ID);
        } catch (err) {
          sendJson(
            res,
            400,
            errorPayload(
              "rule updated but failed to apply nginx config",
              String(err.message || err),
            ),
          );
          return;
        }
      }
      sendJson(res, 200, { ok: true, rule: rules[index] });
    } catch (err) {
      sendJson(res, 400, errorPayload(String(err.message || err)));
    }
    return;
  }

  if (req.method === "DELETE" && /^\/api\/rules\/\d+$/.test(urlPath)) {
    const ruleId = Number(urlPath.split("/").pop());
    const rules = loadOrInitRules(LOCAL_AGENT_ID);
    const index = rules.findIndex((rule) => Number(rule.id) === ruleId);
    if (index === -1) {
      sendJson(res, 404, errorPayload("rule id not found"));
      return;
    }
    const deleted = rules.splice(index, 1)[0];
    saveRulesForAgent(LOCAL_AGENT_ID, rules);
    if (AUTO_APPLY) {
      try {
        await applyAgent(LOCAL_AGENT_ID);
      } catch (err) {
        sendJson(
          res,
          400,
          errorPayload(
            "rule deleted but failed to apply nginx config",
            String(err.message || err),
          ),
        );
        return;
      }
    }
    sendJson(res, 200, { ok: true, rule: deleted });
    return;
  }

  if (req.method === "POST" && urlPath === "/api/apply") {
    try {
      const result = await applyAgent(LOCAL_AGENT_ID);
      sendJson(res, 200, { ok: true, message: result.message || "applied" });
    } catch (err) {
      sendJson(
        res,
        400,
        errorPayload("failed to apply nginx config", String(err.message || err)),
      );
    }
    return;
  }

  if (req.method === "GET" && urlPath === "/api/stats") {
    const stats = await getNginxStats();
    sendJson(res, 200, { ok: true, stats });
    return;
  }

  sendJson(res, 404, errorPayload("not found"));
}
async function handleMasterApi(req, res) {
  const urlPath = (req.url || "").split("?")[0];

  if (req.method === "GET" && urlPath === "/api/public/join-agent.sh") {
    sendText(res, 200, buildJoinAgentScript(req), "application/x-sh; charset=utf-8");
    return;
  }

  if (req.method === "GET" && urlPath.startsWith("/api/public/agent-assets/")) {
    const assetName = decodeURIComponent(urlPath.slice("/api/public/agent-assets/".length));
    const asset = readPublicAgentAsset(assetName);
    if (!asset) {
      sendJson(res, 404, errorPayload("asset not found"));
      return;
    }
    sendText(res, 200, asset.body, asset.contentType);
    return;
  }

  if (req.method === "GET" && urlPath === "/api/auth/verify") {
    const authorized = isPanelAuthorized(req);
    sendJson(res, authorized ? 200 : 401, { ok: authorized, role: ROLE });
    return;
  }

  if (req.method === "GET" && urlPath === "/api/info") {
    sendJson(res, 200, {
      ok: true,
      role: ROLE,
      local_agent_enabled: LOCAL_AGENT_ENABLED,
      default_agent_id: getDefaultAgentId(),
    });
    return;
  }

  if (req.method === "POST" && urlPath === "/api/agents/register") {
    try {
      const body = await parseJsonBody(req);
      if (!isRegisterAuthorized(req, body)) {
        sendJson(
          res,
          401,
          errorPayload("Unauthorized: Invalid or missing register token"),
        );
        return;
      }

      const name = String(body.name || "").trim();
      const agentUrl = trimSlash(body.agent_url || "");
      const agentToken = String(body.agent_token || "").trim();
      const version = String(body.version || "").trim();
      const tags = normalizeTags(body.tags || []);
      const mode = body.mode === "direct" ? "direct" : "pull";

      if (!name) {
        sendJson(res, 400, errorPayload("name is required"));
        return;
      }
      if (agentUrl && !validateUrl(agentUrl)) {
        sendJson(res, 400, errorPayload("agent_url must be a valid http/https URL"));
        return;
      }
      if (!agentToken) {
        sendJson(res, 400, errorPayload("agent_token is required"));
        return;
      }

      const agents = loadRegisteredAgents();
      let agent =
        agents.find((item) => item.agent_token === agentToken) ||
        (agentUrl ? agents.find((item) => item.agent_url === agentUrl) : null) ||
        agents.find((item) => item.name === name) ||
        null;
      const timestamp = nowIso();

      if (agent) {
        agent = ensureAgentState(agent);
        agent.name = name;
        agent.agent_url = agentUrl;
        agent.agent_token = agentToken;
        agent.version = version;
        agent.tags = tags;
        agent.mode = mode;
        agent.updated_at = timestamp;
      } else {
        agent = ensureAgentState({
          id: crypto.randomUUID(),
          name,
          agent_url: agentUrl,
          agent_token: agentToken,
          version,
          tags,
          mode,
          last_seen_at: null,
          created_at: timestamp,
          updated_at: timestamp,
        });
        agents.push(agent);
      }

      saveRegisteredAgents(agents);
      sendJson(res, 200, { ok: true, agent: sanitizeAgent(agent) });
    } catch (err) {
      sendJson(res, 400, errorPayload(String(err.message || err)));
    }
    return;
  }

  if (req.method === "POST" && urlPath === "/api/agents/heartbeat") {
    try {
      const body = await parseJsonBody(req);
      const token =
        String(req.headers["x-agent-token"] || body.agent_token || "").trim();
      const name = String(body.name || "").trim();
      if (!token) {
        sendJson(res, 401, errorPayload("Unauthorized: missing agent token"));
        return;
      }

      const agents = loadRegisteredAgents();
      const index = agents.findIndex(
        (agent) => agent.agent_token === token || (name && agent.name === name),
      );

      if (index === -1) {
        sendJson(res, 404, errorPayload("agent not registered"));
        return;
      }

      const agent = ensureAgentState(agents[index]);
      agent.last_seen_at = nowIso();
      agent.updated_at = nowIso();
      agent.version = String(body.version || agent.version || "").trim();
      agent.tags = body.tags !== undefined ? normalizeTags(body.tags) : agent.tags;
      if (body.agent_url !== undefined) {
        const nextUrl = trimSlash(body.agent_url || "");
        if (nextUrl && !validateUrl(nextUrl)) {
          sendJson(res, 400, errorPayload("agent_url must be a valid http/https URL"));
          return;
        }
        agent.agent_url = nextUrl;
      }
      agent.current_revision = normalizeRevision(
        body.current_revision !== undefined
          ? body.current_revision
          : agent.current_revision,
      );
      if (body.last_apply_status !== undefined) {
        agent.last_apply_status = String(body.last_apply_status || "").trim() || null;
      }
      if (body.last_apply_message !== undefined) {
        agent.last_apply_message = String(body.last_apply_message || "");
      }
      if (body.stats && typeof body.stats === "object") {
        agent.last_reported_stats = body.stats;
      }

      agents[index] = agent;
      saveRegisteredAgents(agents);
      sendJson(res, 200, getAgentHeartbeatResponse(agent));
    } catch (err) {
      sendJson(res, 400, errorPayload(String(err.message || err)));
    }
    return;
  }

  if (!isPanelAuthorized(req)) {
    sendJson(
      res,
      401,
      errorPayload("Unauthorized: Invalid or missing X-Panel-Token"),
    );
    return;
  }

  if (req.method === "GET" && urlPath === "/api/health") {
    sendJson(res, 200, { ok: true, role: ROLE });
    return;
  }

  if (req.method === "GET" && urlPath === "/api/agents") {
    const agents = await hydrateAgents();
    sendJson(res, 200, { ok: true, agents });
    return;
  }

  if (req.method === "GET" && /^\/api\/agents\/[^/]+$/.test(urlPath)) {
    const agents = await hydrateAgents();
    const agentId = extractAgentId(urlPath);
    const agent = agents.find((item) => item.id === agentId);
    if (!agent) {
      sendJson(res, 404, errorPayload("agent not found"));
      return;
    }
    sendJson(res, 200, { ok: true, agent });
    return;
  }

  if (req.method === "PUT" && /^\/api\/agents\/[^/]+$/.test(urlPath)) {
    const agentId = extractAgentId(urlPath);
    if (agentId === LOCAL_AGENT_ID) {
      sendJson(res, 400, errorPayload("local agent cannot be modified"));
      return;
    }

    try {
      const body = await parseJsonBody(req);
      const agents = loadRegisteredAgents();
      const index = agents.findIndex((agent) => agent.id === agentId);
      if (index === -1) {
        sendJson(res, 404, errorPayload("agent not found"));
        return;
      }

      const nextName =
        body.name !== undefined ? String(body.name).trim() : agents[index].name;
      const nextUrl =
        body.agent_url !== undefined
          ? trimSlash(body.agent_url)
          : agents[index].agent_url;
      const nextToken =
        body.agent_token !== undefined
          ? String(body.agent_token).trim()
          : agents[index].agent_token;

      if (!nextName) {
        sendJson(res, 400, errorPayload("name is required"));
        return;
      }
      if (nextUrl && !validateUrl(nextUrl)) {
        sendJson(res, 400, errorPayload("agent_url must be a valid http/https URL"));
        return;
      }
      if (!nextToken) {
        sendJson(res, 400, errorPayload("agent_token is required"));
        return;
      }

      agents[index] = {
        ...agents[index],
        name: nextName,
        agent_url: nextUrl,
        agent_token: nextToken,
        mode:
          body.mode !== undefined
            ? body.mode === "direct"
              ? "direct"
              : "pull"
            : agents[index].mode || "pull",
        version:
          body.version !== undefined
            ? String(body.version).trim()
            : agents[index].version,
        tags: body.tags !== undefined ? normalizeTags(body.tags) : agents[index].tags,
        updated_at: nowIso(),
      };

      saveRegisteredAgents(agents);
      sendJson(res, 200, { ok: true, agent: sanitizeAgent(agents[index]) });
    } catch (err) {
      sendJson(res, 400, errorPayload(String(err.message || err)));
    }
    return;
  }

  if (req.method === "PATCH" && /^\/api\/agents\/[^/]+$/.test(urlPath)) {
    const agentId = extractAgentId(urlPath);
    if (agentId === LOCAL_AGENT_ID) {
      sendJson(res, 400, errorPayload("local agent cannot be modified"));
      return;
    }
    try {
      const body = await parseJsonBody(req);
      const agents = loadRegisteredAgents();
      const index = agents.findIndex((agent) => agent.id === agentId);
      if (index === -1) {
        sendJson(res, 404, errorPayload("agent not found"));
        return;
      }
      if (body.name !== undefined) {
        const nextName = String(body.name).trim();
        if (!nextName) {
          sendJson(res, 400, errorPayload("name cannot be empty"));
          return;
        }
        agents[index] = { ...agents[index], name: nextName, updated_at: nowIso() };
      }
      saveRegisteredAgents(agents);
      sendJson(res, 200, { ok: true, agent: sanitizeAgent(agents[index]) });
    } catch (err) {
      sendJson(res, 400, errorPayload(String(err.message || err)));
    }
    return;
  }

  if (req.method === "DELETE" && /^\/api\/agents\/[^/]+$/.test(urlPath)) {
    const agentId = extractAgentId(urlPath);
    if (agentId === LOCAL_AGENT_ID) {
      sendJson(res, 400, errorPayload("local agent cannot be deleted"));
      return;
    }
    const agents = loadRegisteredAgents();
    const index = agents.findIndex((agent) => agent.id === agentId);
    if (index === -1) {
      sendJson(res, 404, errorPayload("agent not found"));
      return;
    }
    const deleted = agents.splice(index, 1)[0];
    saveRegisteredAgents(agents);
    deleteRulesForAgent(agentId);
    sendJson(res, 200, { ok: true, agent: sanitizeAgent(deleted) });
    return;
  }
  if (req.method === "GET" && /^\/api\/agents\/[^/]+\/stats$/.test(urlPath)) {
    try {
      const agentId = extractAgentId(urlPath);
      const stats = await getAgentStats(agentId);
      sendJson(res, 200, { ok: true, stats });
    } catch (err) {
      sendJson(res, 400, errorPayload(String(err.message || err)));
    }
    return;
  }

  if (req.method === "GET" && /^\/api\/agents\/[^/]+\/rules$/.test(urlPath)) {
    const agentId = extractAgentId(urlPath);
    const agent = getAgentById(agentId);
    if (!agent) {
      sendJson(res, 404, errorPayload("agent not found"));
      return;
    }
    sendJson(res, 200, { ok: true, rules: loadRulesForAgent(agentId) });
    return;
  }

  if (req.method === "POST" && /^\/api\/agents\/[^/]+\/rules$/.test(urlPath)) {
    try {
      const agentId = extractAgentId(urlPath);
      const agent = getAgentById(agentId);
      if (!agent) {
        sendJson(res, 404, errorPayload("agent not found"));
        return;
      }
      const body = await parseJsonBody(req);
      const rules = loadOrInitRules(agentId);
      const maxId = rules.reduce((max, rule) => Math.max(max, Number(rule.id) || 0), 0);
      const newRule = normalizeRulePayload(body, {}, maxId + 1);
      rules.push(newRule);
      saveRulesForAgent(agentId, rules);

      if (AUTO_APPLY) {
        try {
          await applyAgent(agentId);
        } catch (err) {
          sendJson(
            res,
            400,
            errorPayload(
              "rule saved but failed to sync/apply agent config",
              String(err.message || err),
            ),
          );
          return;
        }
      }

      sendJson(res, 201, { ok: true, rule: newRule });
    } catch (err) {
      sendJson(res, 400, errorPayload(String(err.message || err)));
    }
    return;
  }

  if (req.method === "PUT" && /^\/api\/agents\/[^/]+\/rules\/\d+$/.test(urlPath)) {
    try {
      const agentId = extractAgentId(urlPath);
      const agent = getAgentById(agentId);
      if (!agent) {
        sendJson(res, 404, errorPayload("agent not found"));
        return;
      }
      const ruleId = extractRuleId(urlPath);
      const body = await parseJsonBody(req);
      const rules = loadOrInitRules(agentId);
      const index = rules.findIndex((rule) => Number(rule.id) === ruleId);
      if (index === -1) {
        sendJson(res, 404, errorPayload("rule id not found"));
        return;
      }
      rules[index] = normalizeRulePayload(body, rules[index], ruleId);
      saveRulesForAgent(agentId, rules);

      if (AUTO_APPLY) {
        try {
          await applyAgent(agentId);
        } catch (err) {
          sendJson(
            res,
            400,
            errorPayload(
              "rule updated but failed to sync/apply agent config",
              String(err.message || err),
            ),
          );
          return;
        }
      }

      sendJson(res, 200, { ok: true, rule: rules[index] });
    } catch (err) {
      sendJson(res, 400, errorPayload(String(err.message || err)));
    }
    return;
  }

  if (req.method === "DELETE" && /^\/api\/agents\/[^/]+\/rules\/\d+$/.test(urlPath)) {
    try {
      const agentId = extractAgentId(urlPath);
      const agent = getAgentById(agentId);
      if (!agent) {
        sendJson(res, 404, errorPayload("agent not found"));
        return;
      }
      const ruleId = extractRuleId(urlPath);
      const rules = loadOrInitRules(agentId);
      const index = rules.findIndex((rule) => Number(rule.id) === ruleId);
      if (index === -1) {
        sendJson(res, 404, errorPayload("rule id not found"));
        return;
      }
      const deleted = rules.splice(index, 1)[0];
      saveRulesForAgent(agentId, rules);

      if (AUTO_APPLY) {
        try {
          await applyAgent(agentId);
        } catch (err) {
          sendJson(
            res,
            400,
            errorPayload(
              "rule deleted but failed to sync/apply agent config",
              String(err.message || err),
            ),
          );
          return;
        }
      }

      sendJson(res, 200, { ok: true, rule: deleted });
    } catch (err) {
      sendJson(res, 400, errorPayload(String(err.message || err)));
    }
    return;
  }

  if (req.method === "POST" && /^\/api\/agents\/[^/]+\/apply$/.test(urlPath)) {
    try {
      const agentId = extractAgentId(urlPath);
      const result = await applyAgent(agentId);
      sendJson(res, 200, { ok: true, message: result.message || "applied" });
    } catch (err) {
      sendJson(
        res,
        400,
        errorPayload("failed to sync/apply agent config", String(err.message || err)),
      );
    }
    return;
  }

  if (
    urlPath === "/api/rules" ||
    /^\/api\/rules\/\d+$/.test(urlPath) ||
    urlPath === "/api/apply" ||
    urlPath === "/api/stats"
  ) {
    await handleLegacyLocalRules(req, res, urlPath);
    return;
  }

  sendJson(res, 404, errorPayload("not found"));
}

async function handleRequest(req, res) {
  const urlPath = (req.url || "").split("?")[0];

  if (urlPath.startsWith("/agent-api/")) {
    await handleAgentApi(req, res);
    return;
  }

  if (ROLE === "agent") {
    if (req.method === "GET" && urlPath === "/api/auth/verify") {
      const authorized = isPanelAuthorized(req);
      sendJson(res, authorized ? 200 : 401, { ok: authorized, role: ROLE });
      return;
    }

    if (!isPanelAuthorized(req)) {
      sendJson(
        res,
        401,
        errorPayload("Unauthorized: Invalid or missing X-Panel-Token"),
      );
      return;
    }

    if (req.method === "GET" && urlPath === "/api/health") {
      sendJson(res, 200, { ok: true, role: ROLE });
      return;
    }

    if (req.method === "GET" && urlPath === "/api/info") {
      sendJson(res, 200, {
        ok: true,
        role: ROLE,
        agent_name: AGENT_NAME,
        agent_url: AGENT_PUBLIC_URL,
      });
      return;
    }

    sendJson(res, 404, errorPayload("agent mode does not expose panel management APIs"));
    return;
  }

  await handleMasterApi(req, res);
}

ensureDataDir();
migrateCsvToJson();

http
  .createServer((req, res) => {
    handleRequest(req, res).catch((err) => {
      sendJson(
        res,
        500,
        errorPayload("internal server error", String(err.message || err)),
      );
    });
  })
  .listen(PORT, HOST);
