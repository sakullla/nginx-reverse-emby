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
const L4_RULES_DIR =
  process.env.PANEL_L4_RULES_DIR || path.join(DATA_ROOT, "l4_agent_rules");
const MANAGED_CERTS_JSON =
  process.env.PANEL_MANAGED_CERTS_JSON ||
  path.join(DATA_ROOT, "managed_certificates.json");
const MANAGED_CERTS_DIR =
  process.env.PANEL_MANAGED_CERTS_DIR ||
  path.join(DATA_ROOT, "managed_certificates");
const LOCAL_MANAGED_CERT_BUNDLE_JSON =
  process.env.PANEL_LOCAL_MANAGED_CERT_BUNDLE_JSON ||
  path.join(DATA_ROOT, "managed_cert_bundle.local.json");
const LOCAL_AGENT_STATE_JSON =
  process.env.PANEL_LOCAL_AGENT_STATE_JSON ||
  path.join(DATA_ROOT, "local_agent_state.json");
const GENERATOR_SCRIPT =
  process.env.PANEL_GENERATOR_SCRIPT ||
  "/docker-entrypoint.d/25-dynamic-reverse-proxy.sh";
const MANAGED_CERT_HELPER_SCRIPT =
  process.env.PANEL_MANAGED_CERT_HELPER_SCRIPT ||
  path.resolve(__dirname, "..", "..", "scripts", "managed-cert-helper.sh");
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
const DEFAULT_AGENT_CAPABILITIES = normalizeCapabilities(
  (process.env.AGENT_CAPABILITIES || "http_rules,local_acme,cert_install,l4")
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean),
);
const ACME_HOME =
  process.env.ACME_HOME || path.join(DATA_ROOT, ".acme.sh");
const ACME_CA = process.env.ACME_CA || "letsencrypt";
const ACME_DNS_PROVIDER = String(process.env.ACME_DNS_PROVIDER || "").trim();
const CF_TOKEN = String(process.env.CF_Token || process.env.CF_TOKEN || "").trim();
const CF_ACCOUNT_ID = String(
  process.env.CF_Account_ID || process.env.CF_ACCOUNT_ID || "",
).trim();
const MANAGED_CERTS_ENABLED = ACME_DNS_PROVIDER === "cf" && !!CF_TOKEN;
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
  process.env.MASTER_LOCAL_AGENT_NAME || `${os.hostname()} (本机节点)`;
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
    files: [path.join(PROJECT_ROOT, "scripts", "light-agent.js")],
    contentType: "application/javascript; charset=utf-8",
  },
  "light-agent-apply.sh": {
    files: [path.join(PROJECT_ROOT, "scripts", "light-agent-apply.sh")],
    contentType: "application/x-sh; charset=utf-8",
  },
  "25-dynamic-reverse-proxy.sh": {
    files: [
      path.join(PROJECT_ROOT, "docker", "25-dynamic-reverse-proxy.sh"),
      "/docker-entrypoint.d/25-dynamic-reverse-proxy.sh",
    ],
    contentType: "application/x-sh; charset=utf-8",
  },
  "default.conf.template": {
    files: [
      path.join(PROJECT_ROOT, "docker", "default.conf.template"),
      "/etc/nginx/templates/default.conf",
    ],
    contentType: "text/plain; charset=utf-8",
  },
  "default.direct.no_tls.conf.template": {
    files: [
      path.join(PROJECT_ROOT, "docker", "default.direct.no_tls.conf.template"),
      "/etc/nginx/templates/default.direct.no_tls.conf",
    ],
    contentType: "text/plain; charset=utf-8",
  },
  "default.direct.tls.conf.template": {
    files: [
      path.join(PROJECT_ROOT, "docker", "default.direct.tls.conf.template"),
      "/etc/nginx/templates/default.direct.tls.conf",
    ],
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

function logAgentEvent(agentOrId, message, details = null) {
  const agentLabel =
    typeof agentOrId === "object" && agentOrId
      ? `${agentOrId.name || agentOrId.id || "unknown"} (${agentOrId.id || "unknown"})`
      : String(agentOrId || "unknown");
  if (details === null || details === undefined || details === "") {
    console.log(`[agent] ${agentLabel} - ${message}`);
    return;
  }
  console.log(`[agent] ${agentLabel} - ${message}`, details);
}

function ensureDataDir() {
  fs.mkdirSync(DATA_ROOT, { recursive: true });
  fs.mkdirSync(AGENT_RULES_DIR, { recursive: true });
  fs.mkdirSync(L4_RULES_DIR, { recursive: true });
  fs.mkdirSync(MANAGED_CERTS_DIR, { recursive: true });
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

function removePath(targetPath) {
  if (fs.existsSync(targetPath)) {
    fs.rmSync(targetPath, { recursive: true, force: true });
  }
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
  const forwardedHost = String(req.headers["x-forwarded-host"] || "")
    .split(",")[0]
    .trim();
  const requestHost = String(req.headers.host || "")
    .split(",")[0]
    .trim();
  const host = forwardedHost || requestHost;
  const forwardedPort = String(req.headers["x-forwarded-port"] || "")
    .split(",")[0]
    .trim();

  if (!host) {
    return `${proto}://${HOST}:${PORT}`;
  }

  if (host.includes(":")) {
    return `${proto}://${host}`;
  }

  const normalizedPort = Number(forwardedPort);
  const isDefaultPort =
    (proto === "http" && normalizedPort === 80) ||
    (proto === "https" && normalizedPort === 443);

  if (Number.isFinite(normalizedPort) && normalizedPort > 0 && !isDefaultPort) {
    return `${proto}://${host}:${normalizedPort}`;
  }

  return `${proto}://${host}`;
}

function resolvePublicAgentAssetFile(asset) {
  const candidates = Array.isArray(asset.files)
    ? asset.files
    : asset.file
      ? [asset.file]
      : [];
  return candidates.find((file) => file && fs.existsSync(file)) || null;
}

function readPublicAgentAsset(assetName) {
  const asset = PUBLIC_AGENT_ASSETS[assetName];
  if (!asset) return null;
  const assetFile = resolvePublicAgentAssetFile(asset);
  if (!assetFile) {
    const knownFiles = (Array.isArray(asset.files) ? asset.files : [asset.file])
      .filter(Boolean)
      .join(", ");
    throw new Error(`asset file not found: ${knownFiles}`);
  }
  return {
    ...asset,
    file: assetFile,
    body: fs.readFileSync(assetFile, "utf8"),
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

function normalizeCapabilities(capabilities) {
  const allowed = new Set([
    "http_rules",
    "local_acme",
    "cert_install",
    "l4",
  ]);

  return [
    ...new Set(
      (Array.isArray(capabilities) ? capabilities : [])
        .map((item) => String(item || "").trim())
        .filter((item) => allowed.has(item)),
    ),
  ];
}

function resolveRemoteAgentMode(agentUrl) {
  return trimSlash(agentUrl || "") ? "master" : "pull";
}

function normalizeRuleRevision(value, fallback = 0) {
  const parsed = Number(value);
  if (Number.isFinite(parsed) && parsed >= 0) return parsed;
  return normalizeRevision(fallback);
}

function normalizeStoredRule(rule, suggestedId = null) {
  const normalized = normalizeRulePayload(rule || {}, rule || {}, suggestedId);
  normalized.revision = normalizeRuleRevision(rule?.revision);
  return normalized;
}

function getHighestRuleRevision(rules = []) {
  return (Array.isArray(rules) ? rules : []).reduce(
    (max, rule) => Math.max(max, normalizeRuleRevision(rule?.revision)),
    0,
  );
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
  const rules = readJsonFile(getRuleFileForAgent(agentId), []);
  if (!Array.isArray(rules)) return [];
  return rules.map((rule, index) => normalizeStoredRule(rule, index + 1));
}

function saveRulesForAgent(agentId, rules) {
  const normalizedRules = Array.isArray(rules)
    ? rules.map((rule, index) => normalizeStoredRule(rule, index + 1))
    : [];
  writeJsonFile(getRuleFileForAgent(agentId), normalizedRules);
}

function deleteRulesForAgent(agentId) {
  const file = getRuleFileForAgent(agentId);
  if (agentId === LOCAL_AGENT_ID) {
    writeJsonFile(file, []);
    return;
  }
  if (fs.existsSync(file)) fs.unlinkSync(file);
}

function getL4RuleFileForAgent(agentId) {
  return path.join(L4_RULES_DIR, `${agentId}.json`);
}

function normalizeHost(value) {
  return String(value || "").trim().replace(/^\[(.*)\]$/, "$1");
}

function validatePort(value) {
  const port = Number(value);
  return Number.isInteger(port) && port >= 1 && port <= 65535;
}

function validateNetworkHost(value) {
  const host = normalizeHost(value);
  if (!host) return false;
  if (/^[a-zA-Z0-9.-]+$/.test(host)) return true;
  if (/^[0-9A-Fa-f:.]+$/.test(host)) return true;
  return false;
}

function normalizeL4RuleRevision(value, fallback = 0) {
  return normalizeRuleRevision(value, fallback);
}

function normalizeL4Backends(backends, fallbackUpstreamHost, fallbackUpstreamPort) {
  const validBackends = [];
  const arr = Array.isArray(backends) ? backends : [];
  for (const b of arr) {
    const host = normalizeHost(b?.host || b?.address || "");
    const port = Number(b?.port) || Number(fallbackUpstreamPort) || 0;
    if (!host || !port) continue;
    const weight = Number(b?.weight) || 1;
    const resolve = b?.resolve === true || String(b?.resolve).toLowerCase() === "true";
    validBackends.push({ host, port, weight, resolve });
  }
  return validBackends;
}

function normalizeL4LoadBalancing(lb, defaultStrategy = "round_robin") {
  const strategy = String(lb?.strategy !== undefined ? lb.strategy : defaultStrategy).toLowerCase();
  const validStrategies = ["round_robin", "least_conn", "random", "hash"];
  const normalizedStrategy = validStrategies.includes(strategy) ? strategy : "round_robin";
  const hashKey = normalizedStrategy === "hash" ? String(lb?.hash_key || "$remote_addr") : undefined;
  const zoneSize = String(lb?.zone_size || "64k");
  return {
    strategy: normalizedStrategy,
    hash_key: hashKey,
    zone_size: zoneSize,
  };
}

function normalizeL4RulePayload(body, fallback = {}, suggestedId = null) {
  const protocol = String(
    body.protocol !== undefined ? body.protocol : fallback.protocol || "tcp",
  )
    .trim()
    .toLowerCase();
  const listenHost = normalizeHost(
    body.listen_host !== undefined ? body.listen_host : fallback.listen_host || "0.0.0.0",
  );
  const listenPort =
    body.listen_port !== undefined ? Number(body.listen_port) : Number(fallback.listen_port);
  const name = String(body.name !== undefined ? body.name : fallback.name || "").trim();

  if (!["tcp", "udp"].includes(protocol)) {
    throw new Error("protocol must be tcp or udp");
  }
  if (!validateNetworkHost(listenHost)) {
    throw new Error("listen_host must be a valid host or IP");
  }
  if (!validatePort(listenPort)) {
    throw new Error("listen_port must be a valid port");
  }

  const parsedId =
    body.id !== undefined
      ? Number(body.id)
      : fallback.id !== undefined
        ? Number(fallback.id)
        : Number(suggestedId);

  // Support both legacy single upstream and new backends array
  const legacyUpstreamHost = normalizeHost(
    body.upstream_host !== undefined ? body.upstream_host : fallback.upstream_host,
  );
  const legacyUpstreamPort =
    body.upstream_port !== undefined
      ? Number(body.upstream_port)
      : Number(fallback.upstream_port);

  // Normalize backends array (takes precedence if provided)
  const hasBackends = Array.isArray(body?.backends) && body.backends.length > 0;
  const hasFallbackBackends = Array.isArray(fallback?.backends) && fallback.backends.length > 0;
  const backends = hasBackends
    ? normalizeL4Backends(body.backends, legacyUpstreamHost, legacyUpstreamPort)
    : hasFallbackBackends
      ? normalizeL4Backends(fallback.backends, legacyUpstreamHost, legacyUpstreamPort)
      : [];

  // If no backends array provided, use legacy single upstream as single backend
  if (backends.length === 0 && legacyUpstreamHost && legacyUpstreamPort) {
    backends.push({
      host: legacyUpstreamHost,
      port: legacyUpstreamPort,
      weight: 1,
      resolve: false,
    });
  }

  if (backends.length === 0) {
    throw new Error("at least one valid backend (upstream_host:upstream_port or backends) is required");
  }

  // Normalize load balancing settings
  const loadBalancing = normalizeL4LoadBalancing(
    body?.load_balancing !== undefined ? body.load_balancing : fallback?.load_balancing,
    "round_robin",
  );

  return {
    id:
      Number.isFinite(parsedId) && parsedId > 0
        ? parsedId
        : Number(suggestedId) || 1,
    name: name || `${protocol.toUpperCase()} ${listenPort}`,
    protocol,
    listen_host: listenHost,
    listen_port: listenPort,
    // Keep legacy fields for backward compatibility
    upstream_host: backends[0]?.host || legacyUpstreamHost,
    upstream_port: backends[0]?.port || legacyUpstreamPort,
    // New multi-backend fields
    backends,
    load_balancing: loadBalancing,
    enabled:
      body.enabled !== undefined ? !!body.enabled : fallback.enabled !== false,
    tags:
      body.tags !== undefined ? normalizeTags(body.tags) : normalizeTags(fallback.tags || []),
  };
}

function normalizeStoredL4Rule(rule, suggestedId = null) {
  const normalized = normalizeL4RulePayload(rule || {}, rule || {}, suggestedId);
  normalized.revision = normalizeL4RuleRevision(rule?.revision);
  return normalized;
}

function getHighestL4RuleRevision(rules = []) {
  return (Array.isArray(rules) ? rules : []).reduce(
    (max, rule) => Math.max(max, normalizeL4RuleRevision(rule?.revision)),
    0,
  );
}

function loadL4RulesForAgent(agentId) {
  const rules = readJsonFile(getL4RuleFileForAgent(agentId), []);
  if (!Array.isArray(rules)) return [];
  return rules.map((rule, index) => normalizeStoredL4Rule(rule, index + 1));
}

function ensureUniqueL4Listen(rules, nextRule, excludeId = null) {
  const conflict = (Array.isArray(rules) ? rules : []).find((rule) => {
    if (!rule || Number(rule.id) === Number(excludeId)) return false;
    return (
      String(rule.protocol || "tcp") === String(nextRule.protocol || "tcp") &&
      normalizeHost(rule.listen_host) === normalizeHost(nextRule.listen_host) &&
      Number(rule.listen_port) === Number(nextRule.listen_port)
    );
  });
  if (conflict) {
    throw new Error(
      `listen ${nextRule.protocol}:${nextRule.listen_host}:${nextRule.listen_port} conflicts with rule #${conflict.id}`,
    );
  }
}

function saveL4RulesForAgent(agentId, rules) {
  const normalizedRules = Array.isArray(rules)
    ? rules.map((rule, index) => normalizeStoredL4Rule(rule, index + 1))
    : [];
  writeJsonFile(getL4RuleFileForAgent(agentId), normalizedRules);
}

function deleteL4RulesForAgent(agentId) {
  const file = getL4RuleFileForAgent(agentId);
  if (fs.existsSync(file)) fs.unlinkSync(file);
}

function getCertStoreDir(domain) {
  return path.join(MANAGED_CERTS_DIR, normalizeHost(domain));
}

function normalizeManagedCertificatePayload(body, fallback = {}, suggestedId = null) {
  const domain = normalizeHost(
    body.domain !== undefined ? body.domain : fallback.domain,
  ).toLowerCase();
  const targetAgentIds = [
    ...new Set(
      (body.target_agent_ids !== undefined
        ? body.target_agent_ids
        : fallback.target_agent_ids || []
      )
        .map((item) => String(item || "").trim())
        .filter(Boolean),
    ),
  ];
  const scope = String(body.scope !== undefined ? body.scope : fallback.scope || "domain")
    .trim()
    .toLowerCase();
  const issuerMode = String(
    body.issuer_mode !== undefined
      ? body.issuer_mode
      : fallback.issuer_mode || "master_cf_dns",
  )
    .trim()
    .toLowerCase();

  if (!domain || !validateNetworkHost(domain)) {
    throw new Error("domain must be a valid domain or IP");
  }
  if (!["domain", "ip"].includes(scope)) {
    throw new Error("scope must be domain or ip");
  }
  if (!["master_cf_dns", "local_http01"].includes(issuerMode)) {
    throw new Error("issuer_mode must be master_cf_dns or local_http01");
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
    domain,
    enabled:
      body.enabled !== undefined ? !!body.enabled : fallback.enabled !== false,
    scope,
    issuer_mode: issuerMode,
    target_agent_ids: targetAgentIds,
    status: String(body.status !== undefined ? body.status : fallback.status || "pending"),
    last_issue_at:
      body.last_issue_at !== undefined
        ? body.last_issue_at
        : fallback.last_issue_at || null,
    last_error:
      body.last_error !== undefined ? String(body.last_error || "") : String(fallback.last_error || ""),
  };
}

function normalizeStoredManagedCertificate(cert, suggestedId = null) {
  const normalized = normalizeManagedCertificatePayload(cert || {}, cert || {}, suggestedId);
  normalized.revision = normalizeRevision(cert?.revision);
  return normalized;
}

function loadManagedCertificates() {
  const certs = readJsonFile(MANAGED_CERTS_JSON, []);
  if (!Array.isArray(certs)) return [];
  return certs.map((cert, index) => normalizeStoredManagedCertificate(cert, index + 1));
}

function saveManagedCertificates(certs) {
  const normalized = Array.isArray(certs)
    ? certs.map((cert, index) => normalizeStoredManagedCertificate(cert, index + 1))
    : [];
  writeJsonFile(MANAGED_CERTS_JSON, normalized);
}

function getManagedCertificateById(certId) {
  return loadManagedCertificates().find((item) => Number(item.id) === Number(certId)) || null;
}

function getHighestManagedCertificateRevisionForAgent(agentId) {
  const agent = getAgentById(agentId);
  if (!agent || !agentHasCapability(agent, "cert_install")) return 0;
  return loadManagedCertificates().reduce((max, cert) => {
    if (!cert.enabled) return max;
    if (!Array.isArray(cert.target_agent_ids) || !cert.target_agent_ids.includes(agentId)) {
      return max;
    }
    return Math.max(max, normalizeRevision(cert.revision));
  }, 0);
}

function readManagedCertificateMaterial(domain) {
  const certDir = getCertStoreDir(domain);
  const certFile = path.join(certDir, "cert");
  const keyFile = path.join(certDir, "key");
  if (!fs.existsSync(certFile) || !fs.existsSync(keyFile)) return null;
  return {
    cert_pem: fs.readFileSync(certFile, "utf8"),
    key_pem: fs.readFileSync(keyFile, "utf8"),
  };
}

function buildManagedCertificateBundleForAgent(agentId) {
  return loadManagedCertificates()
    .filter((cert) => cert.enabled && cert.scope === "domain")
    .filter((cert) => Array.isArray(cert.target_agent_ids) && cert.target_agent_ids.includes(agentId))
    .map((cert) => {
      const material = readManagedCertificateMaterial(cert.domain);
      if (!material) return null;
      return {
        id: cert.id,
        domain: cert.domain,
        revision: normalizeRevision(cert.revision),
        cert_pem: material.cert_pem,
        key_pem: material.key_pem,
      };
    })
    .filter(Boolean);
}

function getManagedCertBundleFileForAgent(agentId) {
  if (agentId === LOCAL_AGENT_ID) return LOCAL_MANAGED_CERT_BUNDLE_JSON;
  return path.join(AGENT_RULES_DIR, `${agentId}.managed-certs.json`);
}

function persistManagedCertificateBundleForAgent(agentId) {
  writeJsonFile(getManagedCertBundleFileForAgent(agentId), buildManagedCertificateBundleForAgent(agentId));
}

function loadRegisteredAgents() {
  return readJsonFile(AGENTS_JSON, []);
}

function saveRegisteredAgents(agents) {
  writeJsonFile(AGENTS_JSON, agents);
}

function getNextGlobalRevision() {
  const registered = loadRegisteredAgents();
  const agentRevisions = registered.reduce(
    (max, agent) =>
      Math.max(
        max,
        normalizeRevision(agent?.desired_revision),
        normalizeRevision(agent?.current_revision),
        normalizeRevision(agent?.last_apply_revision),
      ),
    0,
  );
  const localState = loadLocalAgentState();
  const localMax = Math.max(
    normalizeRevision(localState?.desired_revision),
    normalizeRevision(localState?.current_revision),
    normalizeRevision(localState?.last_apply_revision),
  );
  const certMax = loadManagedCertificates().reduce(
    (max, cert) => Math.max(max, normalizeRevision(cert?.revision)),
    0,
  );
  return Math.max(agentRevisions, localMax, certMax) + 1;
}

function ensureLocalAgentState(state = {}) {
  return {
    desired_revision: normalizeRevision(state.desired_revision),
    current_revision: normalizeRevision(state.current_revision),
    last_apply_revision: normalizeRevision(
      state.last_apply_revision,
    ),
    last_apply_status:
      state.last_apply_status === undefined || state.last_apply_status === null
        ? "success"
        : String(state.last_apply_status || "").trim() || null,
    last_apply_message: String(state.last_apply_message || ""),
  };
}

function loadLocalAgentState() {
  return ensureLocalAgentState(readJsonFile(LOCAL_AGENT_STATE_JSON, {}));
}

function saveLocalAgentState(state) {
  writeJsonFile(LOCAL_AGENT_STATE_JSON, ensureLocalAgentState(state));
}

function getAgentLastApplyRevision(agent) {
  const value =
    agent?.last_apply_revision !== undefined
      ? agent.last_apply_revision
      : agent?.current_revision;
  return normalizeRevision(value);
}

function getNextPendingRevision(agent) {
  const desiredRevision = normalizeRevision(agent?.desired_revision);
  const currentRevision = normalizeRevision(agent?.current_revision);
  const lastApplyRevision = getAgentLastApplyRevision(agent);

  if (desiredRevision > currentRevision && lastApplyRevision < desiredRevision) {
    return desiredRevision;
  }

  return Math.max(desiredRevision, currentRevision) + 1;
}

function getDesiredRevisionForSync(agent, agentId, rules = [], options = {}) {
  const desiredRevision = normalizeRevision(agent?.desired_revision);
  const currentRevision = normalizeRevision(agent?.current_revision);
  const highestRuleRevision = getHighestRuleRevision(rules);
  const highestL4Revision = getHighestL4RuleRevision(loadL4RulesForAgent(agentId));
  const highestManagedCertRevision = getHighestManagedCertificateRevisionForAgent(agentId);
  const highestConfigRevision = Math.max(
    highestRuleRevision,
    highestL4Revision,
    highestManagedCertRevision,
  );

  if (desiredRevision > currentRevision) {
    return Math.max(desiredRevision, highestConfigRevision);
  }

  if (highestConfigRevision > currentRevision) {
    return highestConfigRevision;
  }

  if (options.force) {
    return currentRevision + 1;
  }

  return desiredRevision;
}

function normalizeRevision(value) {
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : 0;
}

function ensureAgentState(agent) {
  agent.mode = agent.is_local ? "local" : resolveRemoteAgentMode(agent.agent_url);
  agent.desired_revision = normalizeRevision(agent.desired_revision);
  agent.current_revision = normalizeRevision(agent.current_revision);
  agent.last_apply_revision = normalizeRevision(
    agent.last_apply_revision !== undefined
      ? agent.last_apply_revision
      : agent.current_revision,
  );
  agent.last_apply_status = agent.last_apply_status || null;
  agent.last_apply_message = agent.last_apply_message || "";
  agent.last_reported_stats = agent.last_reported_stats || null;
  agent.capabilities = normalizeCapabilities(
    agent.capabilities && agent.capabilities.length
      ? agent.capabilities
      : agent.is_local
        ? DEFAULT_AGENT_CAPABILITIES
        : ["http_rules"],
  );
  return agent;
}

function getAgentStatus(agent) {
  if (agent.is_local) return "online";
  const lastSeen = Date.parse(agent.last_seen_at || "");
  if (!lastSeen) return "offline";
  return Date.now() - lastSeen <= AGENT_HEARTBEAT_TIMEOUT_MS ? "online" : "offline";
}

function agentHasCapability(agent, capability) {
  return normalizeCapabilities(agent?.capabilities || []).includes(capability);
}

function makeLocalAgent() {
  if (!LOCAL_AGENT_ENABLED) return null;
  const timestamp = nowIso();
  const state = loadLocalAgentState();
  return {
    id: LOCAL_AGENT_ID,
    name: LOCAL_AGENT_NAME,
    agent_url: LOCAL_AGENT_URL,
    version: AGENT_VERSION,
    tags: LOCAL_AGENT_TAGS,
    mode: "local",
    desired_revision: state.desired_revision,
    current_revision: state.current_revision,
    last_apply_revision: state.last_apply_revision,
    last_apply_status: state.last_apply_status,
    last_apply_message: state.last_apply_message,
    last_reported_stats: null,
    status: "online",
    last_seen_at: timestamp,
    created_at: timestamp,
    updated_at: timestamp,
    is_local: true,
    capabilities: DEFAULT_AGENT_CAPABILITIES,
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
    last_apply_revision: hydrated.last_apply_revision,
    last_apply_status: hydrated.last_apply_status,
    last_apply_message: hydrated.last_apply_message,
    last_reported_stats: hydrated.last_reported_stats,
    last_seen_at: hydrated.last_seen_at || null,
    created_at: hydrated.created_at || null,
    updated_at: hydrated.updated_at || null,
    status: getAgentStatus(hydrated),
    error: hydrated.error || null,
    is_local: !!hydrated.is_local,
    last_seen_ip: hydrated.last_seen_ip || null,
    capabilities: normalizeCapabilities(hydrated.capabilities || []),
  };
}

function getAgentById(agentId) {
  if (LOCAL_AGENT_ENABLED && agentId === LOCAL_AGENT_ID) {
    return makeLocalAgent();
  }
  const agent = loadRegisteredAgents().find((item) => item.id === agentId) || null;
  return agent ? ensureAgentState(agent) : null;
}

function runChecked(command, args, extraEnv = {}) {
  const result = spawnSync(command, args, {
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
    env: { ...process.env, ...extraEnv },
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

function prepareLocalManagedCertificateBundle() {
  persistManagedCertificateBundleForAgent(LOCAL_AGENT_ID);
  return getManagedCertBundleFileForAgent(LOCAL_AGENT_ID);
}

function applyNginxConfig() {
  const extraEnv = {
    PANEL_L4_RULES_JSON: getL4RuleFileForAgent(LOCAL_AGENT_ID),
    PANEL_MANAGED_CERTS_SYNC_JSON: prepareLocalManagedCertificateBundle(),
  };

  if (APPLY_COMMAND) {
    runChecked(APPLY_COMMAND, APPLY_COMMAND_ARGS, extraEnv);
    return;
  }

  runChecked(GENERATOR_SCRIPT, [], extraEnv);
  runChecked(NGINX_BIN, ["-t"], extraEnv);
  runChecked(NGINX_BIN, ["-s", "reload"], extraEnv);
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
    status: "本机节点",
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
          status: "本机节点异常",
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

function assertManagedCertificateEnabled() {
  if (!MANAGED_CERTS_ENABLED) {
    throw new Error("managed certificates require ACME_DNS_PROVIDER=cf and CF_Token");
  }
}

function updateManagedCertificate(certId, updater) {
  const certs = loadManagedCertificates();
  const index = certs.findIndex((item) => Number(item.id) === Number(certId));
  if (index === -1) throw new Error("certificate not found");
  certs[index] = updater({ ...certs[index] });
  saveManagedCertificates(certs);
  return certs[index];
}

function runManagedCertificateHelper(domain) {
  const targetDir = getCertStoreDir(domain);
  runChecked(
    "sh",
    [MANAGED_CERT_HELPER_SCRIPT, "issue", domain, targetDir],
    {
      ACME_HOME,
      ACME_CA,
      ACME_DNS_PROVIDER,
      CF_Token: CF_TOKEN,
      CF_Account_ID: CF_ACCOUNT_ID,
      MANAGED_CERTS_DIR,
    },
  );
}

async function syncManagedCertificateTargets(cert) {
  const targetIds = Array.isArray(cert?.target_agent_ids) ? cert.target_agent_ids : [];
  return syncManagedCertificateAgentIds(targetIds);
}

async function syncManagedCertificateAgentIds(agentIds) {
  const targetIds = [...new Set((Array.isArray(agentIds) ? agentIds : []).filter(Boolean))];
  for (const agentId of targetIds) {
    const agent = getAgentById(agentId);
    if (!agent) continue;
    if (!agentHasCapability(agent, "cert_install")) continue;
    if (agentId === LOCAL_AGENT_ID) {
      persistManagedCertificateBundleForAgent(agentId);
    }
    if (AUTO_APPLY) {
      await applyAgent(agentId);
    }
  }
}

function getManagedCertificateAffectedAgentIds(previousCert, nextCert) {
  return [
    ...new Set([
      ...(Array.isArray(previousCert?.target_agent_ids) ? previousCert.target_agent_ids : []),
      ...(Array.isArray(nextCert?.target_agent_ids) ? nextCert.target_agent_ids : []),
    ]),
  ];
}

function getManagedCertificateRemovedAgentIds(previousCert, nextCert) {
  const previousIds = Array.isArray(previousCert?.target_agent_ids) ? previousCert.target_agent_ids : [];
  const nextIds = new Set(
    Array.isArray(nextCert?.target_agent_ids) ? nextCert.target_agent_ids : [],
  );
  return [...new Set(previousIds.filter((agentId) => !nextIds.has(agentId)))];
}

function validateManagedCertificateTargets(cert) {
  for (const agentId of cert.target_agent_ids || []) {
    const agent = getAgentById(agentId);
    if (!agent) {
      throw new Error(`target agent not found: ${agentId}`);
    }
    if (!agentHasCapability(agent, "cert_install")) {
      throw new Error(`target agent does not support certificate install: ${agent.name || agentId}`);
    }
  }
}

async function issueManagedCertificateById(certId, options = {}) {
  const { bumpRevision = true } = options;
  assertManagedCertificateEnabled();

  let cert = getManagedCertificateById(certId);
  if (!cert) throw new Error("certificate not found");
  if (cert.scope !== "domain") {
    throw new Error("only domain certificates can be managed by master");
  }
  if (!cert.enabled) {
    throw new Error("certificate is disabled");
  }

  try {
    runManagedCertificateHelper(cert.domain);
    cert = updateManagedCertificate(certId, (current) => ({
      ...current,
      status: "active",
      last_issue_at: nowIso(),
      last_error: "",
      revision: bumpRevision ? getNextGlobalRevision() : normalizeRevision(current.revision),
    }));
  } catch (err) {
    cert = updateManagedCertificate(certId, (current) => ({
      ...current,
      status: "error",
      last_error: String(err.message || err),
      revision: bumpRevision ? getNextGlobalRevision() : normalizeRevision(current.revision),
    }));
    throw new Error(cert.last_error);
  }

  await syncManagedCertificateTargets(cert);
  return cert;
}

async function syncAgentRules(agentId) {
  const agent = getAgentById(agentId);
  if (!agent) throw new Error("agent not found");
  const rules = loadRulesForAgent(agentId);
  if (agentId === LOCAL_AGENT_ID) {
    const desiredRevision = getDesiredRevisionForSync(agent, agentId, rules);
    if (desiredRevision > agent.desired_revision) {
      saveLocalAgentState({
        ...loadLocalAgentState(),
        desired_revision: desiredRevision,
      });
    }
    return { ok: true, rules, desired_revision: desiredRevision };
  }
  const agents = loadRegisteredAgents();
  const index = agents.findIndex((item) => item.id === agentId);
  if (index === -1) throw new Error("agent not found");
  agents[index] = ensureAgentState(agents[index]);
  agents[index].desired_revision = getDesiredRevisionForSync(agents[index], agentId, rules, {
    force: true,
  });
  agents[index].updated_at = nowIso();
  saveRegisteredAgents(agents);
  logAgentEvent(agents[index], "queued rules sync", {
    desired_revision: agents[index].desired_revision,
    rule_count: Array.isArray(rules) ? rules.length : 0,
  });
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
    const rules = loadRulesForAgent(agentId);
    const desiredRevision = getDesiredRevisionForSync(agent, agentId, rules, { force: true });
    const nextState = {
      ...loadLocalAgentState(),
      desired_revision: desiredRevision,
      last_apply_revision: desiredRevision,
    };
    try {
      applyNginxConfig();
      nextState.current_revision = desiredRevision;
      nextState.last_apply_status = "success";
      nextState.last_apply_message = "";
      saveLocalAgentState(nextState);
      return { ok: true, message: "applied", desired_revision: desiredRevision };
    } catch (err) {
      nextState.current_revision = agent.current_revision;
      nextState.last_apply_status = "error";
      nextState.last_apply_message = String(err.message || err);
      saveLocalAgentState(nextState);
      throw err;
    }
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
  const l4Rules = loadL4RulesForAgent(agent.id);
  const certificates = buildManagedCertificateBundleForAgent(agent.id);
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
      l4_rules: hasUpdate ? l4Rules : undefined,
      certificates: hasUpdate ? certificates : undefined,
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

function extractTrailingId(urlPath) {
  const match = urlPath.match(/\/(\d+)$/);
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
        capabilities: DEFAULT_AGENT_CAPABILITIES,
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
        capabilities: DEFAULT_AGENT_CAPABILITIES,
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
      const agent = getAgentById(LOCAL_AGENT_ID);
      const rules = loadOrInitRules(LOCAL_AGENT_ID);
      const maxId = rules.reduce((max, rule) => Math.max(max, Number(rule.id) || 0), 0);
      const newRule = normalizeRulePayload(body, {}, maxId + 1);
      newRule.revision = getNextPendingRevision(agent);
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
      const agent = getAgentById(LOCAL_AGENT_ID);
      const rules = loadOrInitRules(LOCAL_AGENT_ID);
      const index = rules.findIndex((rule) => Number(rule.id) === ruleId);
      if (index === -1) {
        sendJson(res, 404, errorPayload("rule id not found"));
        return;
      }
      rules[index] = normalizeRulePayload(body, rules[index], ruleId);
      rules[index].revision = getNextPendingRevision(agent);
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
      managed_certificates_enabled: MANAGED_CERTS_ENABLED,
      cf_token_configured: !!CF_TOKEN,
      acme_dns_provider: ACME_DNS_PROVIDER || null,
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
      const capabilities = normalizeCapabilities(
        body.capabilities !== undefined ? body.capabilities : ["http_rules"],
      );
      const mode = resolveRemoteAgentMode(agentUrl);

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
        agent.capabilities = capabilities;
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
          capabilities,
          mode,
          last_seen_at: null,
          created_at: timestamp,
          updated_at: timestamp,
        });
        agents.push(agent);
      }

      saveRegisteredAgents(agents);
      logAgentEvent(agent, "registered/updated", {
        mode: agent.mode,
        agent_url: agent.agent_url || "",
        tags: agent.tags || [],
      });
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
      const previous = { ...agent };
      agent.last_seen_at = nowIso();
      agent.updated_at = nowIso();
      const remoteIp =
        String(req.headers["x-forwarded-for"] || "").split(",")[0].trim() ||
        req.socket.remoteAddress ||
        "";
      if (remoteIp) agent.last_seen_ip = remoteIp;
      agent.version = String(body.version || agent.version || "").trim();
      agent.tags = body.tags !== undefined ? normalizeTags(body.tags) : agent.tags;
      if (body.capabilities !== undefined) {
        agent.capabilities = normalizeCapabilities(body.capabilities);
      }
      if (body.agent_url !== undefined) {
        const nextUrl = trimSlash(body.agent_url || "");
        if (nextUrl && !validateUrl(nextUrl)) {
          sendJson(res, 400, errorPayload("agent_url must be a valid http/https URL"));
          return;
        }
        agent.agent_url = nextUrl;
        agent.mode = resolveRemoteAgentMode(nextUrl);
      }
      agent.current_revision = normalizeRevision(
        body.current_revision !== undefined
          ? body.current_revision
          : agent.current_revision,
      );
      if (body.last_apply_revision !== undefined) {
        agent.last_apply_revision = normalizeRevision(body.last_apply_revision);
      } else {
        agent.last_apply_revision = normalizeRevision(
          agent.last_apply_revision !== undefined
            ? agent.last_apply_revision
            : agent.current_revision,
        );
      }
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
      const hasRevisionChange = previous.current_revision !== agent.current_revision;
      const hasApplyStatusChange =
        previous.last_apply_revision !== agent.last_apply_revision ||
        previous.last_apply_status !== agent.last_apply_status ||
        previous.last_apply_message !== agent.last_apply_message;
      if (hasRevisionChange || hasApplyStatusChange) {
        logAgentEvent(agent, "heartbeat updated state", {
          current_revision: agent.current_revision,
          desired_revision: agent.desired_revision,
          last_apply_revision: agent.last_apply_revision,
          last_apply_status: agent.last_apply_status,
          last_apply_message: agent.last_apply_message,
        });
      }
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
      sendJson(res, 400, errorPayload("本机节点不支持此操作"));
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
        mode: resolveRemoteAgentMode(nextUrl),
        version:
          body.version !== undefined
            ? String(body.version).trim()
            : agents[index].version,
        tags: body.tags !== undefined ? normalizeTags(body.tags) : agents[index].tags,
        capabilities:
          body.capabilities !== undefined
            ? normalizeCapabilities(body.capabilities)
            : agents[index].capabilities,
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
      sendJson(res, 400, errorPayload("本机节点不支持此操作"));
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
          sendJson(res, 400, errorPayload("名称不能为空"));
          return;
        }
        agents[index] = { ...agents[index], name: nextName, updated_at: nowIso() };
      }
      if (body.capabilities !== undefined) {
        agents[index] = {
          ...agents[index],
          capabilities: normalizeCapabilities(body.capabilities),
          updated_at: nowIso(),
        };
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
      sendJson(res, 400, errorPayload("本机节点不可删除"));
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
    deleteL4RulesForAgent(agentId);
    removePath(getManagedCertBundleFileForAgent(agentId));
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
      newRule.revision = getNextPendingRevision(agent);
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

  if (req.method === "GET" && /^\/api\/agents\/[^/]+\/l4-rules$/.test(urlPath)) {
    const agentId = extractAgentId(urlPath);
    const agent = getAgentById(agentId);
    if (!agent) {
      sendJson(res, 404, errorPayload("agent not found"));
      return;
    }
    sendJson(res, 200, { ok: true, rules: loadL4RulesForAgent(agentId) });
    return;
  }

  if (req.method === "POST" && /^\/api\/agents\/[^/]+\/l4-rules$/.test(urlPath)) {
    try {
      const agentId = extractAgentId(urlPath);
      const agent = getAgentById(agentId);
      if (!agent) {
        sendJson(res, 404, errorPayload("agent not found"));
        return;
      }
      if (!agentHasCapability(agent, "l4")) {
        sendJson(res, 400, errorPayload("agent does not support L4 rules"));
        return;
      }
      const body = await parseJsonBody(req);
      const rules = loadL4RulesForAgent(agentId);
      const maxId = rules.reduce((max, rule) => Math.max(max, Number(rule.id) || 0), 0);
      const newRule = normalizeL4RulePayload(body, {}, maxId + 1);
      ensureUniqueL4Listen(rules, newRule);
      newRule.revision = getNextPendingRevision(agent);
      rules.push(newRule);
      saveL4RulesForAgent(agentId, rules);

      if (AUTO_APPLY) {
        try {
          await applyAgent(agentId);
        } catch (err) {
          sendJson(
            res,
            400,
            errorPayload(
              "L4 rule saved but failed to sync/apply agent config",
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
      rules[index].revision = getNextPendingRevision(agent);
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

  if (req.method === "PUT" && /^\/api\/agents\/[^/]+\/l4-rules\/\d+$/.test(urlPath)) {
    try {
      const agentId = extractAgentId(urlPath);
      const agent = getAgentById(agentId);
      if (!agent) {
        sendJson(res, 404, errorPayload("agent not found"));
        return;
      }
      if (!agentHasCapability(agent, "l4")) {
        sendJson(res, 400, errorPayload("agent does not support L4 rules"));
        return;
      }
      const ruleId = extractTrailingId(urlPath);
      const body = await parseJsonBody(req);
      const rules = loadL4RulesForAgent(agentId);
      const index = rules.findIndex((rule) => Number(rule.id) === ruleId);
      if (index === -1) {
        sendJson(res, 404, errorPayload("rule id not found"));
        return;
      }
      const nextRule = normalizeL4RulePayload(body, rules[index], ruleId);
      ensureUniqueL4Listen(rules, nextRule, ruleId);
      nextRule.revision = getNextPendingRevision(agent);
      rules[index] = nextRule;
      saveL4RulesForAgent(agentId, rules);

      if (AUTO_APPLY) {
        try {
          await applyAgent(agentId);
        } catch (err) {
          sendJson(
            res,
            400,
            errorPayload(
              "L4 rule updated but failed to sync/apply agent config",
              String(err.message || err),
            ),
          );
          return;
        }
      }

      sendJson(res, 200, { ok: true, rule: nextRule });
    } catch (err) {
      sendJson(res, 400, errorPayload(String(err.message || err)));
    }
    return;
  }

  if (req.method === "DELETE" && /^\/api\/agents\/[^/]+\/l4-rules\/\d+$/.test(urlPath)) {
    try {
      const agentId = extractAgentId(urlPath);
      const agent = getAgentById(agentId);
      if (!agent) {
        sendJson(res, 404, errorPayload("agent not found"));
        return;
      }
      const ruleId = extractTrailingId(urlPath);
      const rules = loadL4RulesForAgent(agentId);
      const index = rules.findIndex((rule) => Number(rule.id) === ruleId);
      if (index === -1) {
        sendJson(res, 404, errorPayload("rule id not found"));
        return;
      }
      const deleted = rules.splice(index, 1)[0];
      saveL4RulesForAgent(agentId, rules);

      if (AUTO_APPLY) {
        try {
          await applyAgent(agentId);
        } catch (err) {
          sendJson(
            res,
            400,
            errorPayload(
              "L4 rule deleted but failed to sync/apply agent config",
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

  if (req.method === "GET" && urlPath === "/api/certificates") {
    sendJson(res, 200, { ok: true, certificates: loadManagedCertificates() });
    return;
  }

  if (req.method === "POST" && urlPath === "/api/certificates") {
    try {
      const body = await parseJsonBody(req);
      const certs = loadManagedCertificates();
      const maxId = certs.reduce((max, cert) => Math.max(max, Number(cert.id) || 0), 0);
      const nextCert = normalizeManagedCertificatePayload(body, {}, maxId + 1);
      if (nextCert.scope === "domain" && nextCert.issuer_mode === "master_cf_dns") {
        assertManagedCertificateEnabled();
        validateManagedCertificateTargets(nextCert);
      }
      nextCert.revision = getNextGlobalRevision();
      certs.push(nextCert);
      saveManagedCertificates(certs);

      let savedCert = nextCert;
      if (nextCert.enabled && nextCert.scope === "domain" && nextCert.issuer_mode === "master_cf_dns") {
        savedCert = await issueManagedCertificateById(nextCert.id, { bumpRevision: false });
      }

      sendJson(res, 201, { ok: true, certificate: savedCert });
    } catch (err) {
      sendJson(res, 400, errorPayload(String(err.message || err)));
    }
    return;
  }

  if (req.method === "PUT" && /^\/api\/certificates\/\d+$/.test(urlPath)) {
    try {
      const certId = extractTrailingId(urlPath);
      const body = await parseJsonBody(req);
      const certs = loadManagedCertificates();
      const index = certs.findIndex((cert) => Number(cert.id) === certId);
      if (index === -1) {
        sendJson(res, 404, errorPayload("certificate not found"));
        return;
      }
      const previousCert = { ...certs[index] };
      const nextCert = normalizeManagedCertificatePayload(body, certs[index], certId);
      if (nextCert.scope === "domain" && nextCert.issuer_mode === "master_cf_dns") {
        assertManagedCertificateEnabled();
        validateManagedCertificateTargets(nextCert);
      }
      nextCert.revision = getNextGlobalRevision();
      certs[index] = nextCert;
      saveManagedCertificates(certs);

      let savedCert = nextCert;
      const affectedAgentIds = getManagedCertificateAffectedAgentIds(previousCert, nextCert);
      const removedAgentIds = getManagedCertificateRemovedAgentIds(previousCert, nextCert);
      if (nextCert.enabled && nextCert.scope === "domain" && nextCert.issuer_mode === "master_cf_dns") {
        savedCert = await issueManagedCertificateById(certId, { bumpRevision: false });
        if (removedAgentIds.length > 0) {
          await syncManagedCertificateAgentIds(removedAgentIds);
        }
      } else {
        await syncManagedCertificateAgentIds(affectedAgentIds);
      }

      sendJson(res, 200, { ok: true, certificate: savedCert });
    } catch (err) {
      sendJson(res, 400, errorPayload(String(err.message || err)));
    }
    return;
  }

  if (req.method === "DELETE" && /^\/api\/certificates\/\d+$/.test(urlPath)) {
    try {
      const certId = extractTrailingId(urlPath);
      const certs = loadManagedCertificates();
      const index = certs.findIndex((cert) => Number(cert.id) === certId);
      if (index === -1) {
        sendJson(res, 404, errorPayload("certificate not found"));
        return;
      }
      const deleted = certs.splice(index, 1)[0];
      saveManagedCertificates(certs);
      removePath(getCertStoreDir(deleted.domain));
      for (const agentId of deleted.target_agent_ids || []) {
        if (agentId === LOCAL_AGENT_ID) {
          persistManagedCertificateBundleForAgent(agentId);
        }
        if (AUTO_APPLY) {
          await applyAgent(agentId);
        }
      }
      sendJson(res, 200, { ok: true, certificate: deleted });
    } catch (err) {
      sendJson(res, 400, errorPayload(String(err.message || err)));
    }
    return;
  }

  if (req.method === "POST" && /^\/api\/certificates\/\d+\/issue$/.test(urlPath)) {
    try {
      const certId = extractTrailingId(urlPath.replace(/\/issue$/, ""));
      const cert = await issueManagedCertificateById(certId, { bumpRevision: true });
      sendJson(res, 200, { ok: true, certificate: cert });
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
