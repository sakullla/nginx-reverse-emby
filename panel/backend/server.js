#!/usr/bin/env node
"use strict";

const fs = require("fs");
const http = require("http");
const os = require("os");
const path = require("path");
const crypto = require("crypto");
const { spawnSync } = require("child_process");
const storage = require("./storage");
const {
  normalizeRuleRequestHeaders,
} = require("./http-rule-request-headers");
const { normalizeRelayListenerPayload } = require("./relay-listener-normalize");
const { normalizeVersionPolicyPayload } = require("./version-policy-normalize");

const HOST = process.env.PANEL_BACKEND_HOST || "127.0.0.1";
const PORT = Number(process.env.PANEL_BACKEND_PORT || "18081");
const DATA_ROOT =
  process.env.PANEL_DATA_ROOT || "/opt/nginx-reverse-emby/panel/data";
const ROLE = normalizeRole(process.env.PANEL_ROLE || "master");
const RULES_JSON =
  process.env.PANEL_RULES_JSON || path.join(DATA_ROOT, "proxy_rules.json");
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
const LOCAL_MANAGED_CERT_POLICY_JSON =
  process.env.PANEL_LOCAL_MANAGED_CERT_POLICY_JSON ||
  path.join(DATA_ROOT, "managed_cert_policy.local.json");
const LOCAL_AGENT_STATE_JSON =
  process.env.PANEL_LOCAL_AGENT_STATE_JSON ||
  path.join(DATA_ROOT, "local_agent_state.json");
const GENERATOR_SCRIPT =
  process.env.PANEL_GENERATOR_SCRIPT || "";
const MANAGED_CERT_HELPER_SCRIPT =
  process.env.PANEL_MANAGED_CERT_HELPER_SCRIPT ||
  path.resolve(__dirname, "..", "..", "scripts", "managed-cert-helper.sh");
const NGINX_BIN = process.env.PANEL_NGINX_BIN || "nginx";
const NGINX_ERROR_LOG_FILE =
  process.env.PANEL_NGINX_ERROR_LOG || "/proc/1/fd/2";
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
const ACME_SCRIPT = path.join(ACME_HOME, "acme.sh");
const ACME_COMMON_ARGS = ["--home", ACME_HOME, "--config-home", ACME_HOME, "--cert-home", ACME_HOME];
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
const MANAGED_CERT_RENEW_INTERVAL_MS = Number(
  process.env.PANEL_MANAGED_CERT_RENEW_INTERVAL_MS || "86400000",
);
const LOCAL_AGENT_ENABLED =
  ROLE === "master" &&
  !/^(0|false|no|off)$/i.test(process.env.MASTER_LOCAL_AGENT_ENABLED || "1");
const LOCAL_AGENT_ID = process.env.MASTER_LOCAL_AGENT_ID || "local";
const LOCAL_AGENT_NAME =
  process.env.MASTER_LOCAL_AGENT_NAME || `${os.hostname()} (本地 Agent)`;
const LOCAL_AGENT_URL = trimSlash(process.env.MASTER_LOCAL_AGENT_URL || "");
const LOCAL_AGENT_TAGS = normalizeTags(
  (process.env.MASTER_LOCAL_AGENT_TAGS || "local")
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean),
);

const PROJECT_ROOT = path.resolve(__dirname, "..", "..");
const FRONTEND_DIST_DIR =
  process.env.PANEL_FRONTEND_DIST_DIR ||
  path.join(PROJECT_ROOT, "panel", "frontend", "dist");
const PUBLIC_AGENT_ASSETS_DIR =
  process.env.PANEL_PUBLIC_AGENT_ASSETS_DIR ||
  path.join(PROJECT_ROOT, "panel", "public", "agent-assets");
const PUBLIC_AGENT_ASSETS = {
  "nre-agent-linux-amd64": {
    files: [path.join(PUBLIC_AGENT_ASSETS_DIR, "nre-agent-linux-amd64")],
    contentType: "application/octet-stream",
    binary: true,
  },
  "nre-agent-linux-arm64": {
    files: [path.join(PUBLIC_AGENT_ASSETS_DIR, "nre-agent-linux-arm64")],
    contentType: "application/octet-stream",
    binary: true,
  },
  "nre-agent-darwin-amd64": {
    files: [path.join(PUBLIC_AGENT_ASSETS_DIR, "nre-agent-darwin-amd64")],
    contentType: "application/octet-stream",
    binary: true,
  },
  "nre-agent-darwin-arm64": {
    files: [path.join(PUBLIC_AGENT_ASSETS_DIR, "nre-agent-darwin-arm64")],
    contentType: "application/octet-stream",
    binary: true,
  },
};
let isManagedCertificateRenewRunning = false;

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

function sendBody(res, statusCode, body, contentType, extraHeaders = {}) {
  const payload = Buffer.isBuffer(body) ? body : Buffer.from(String(body), "utf8");
  res.writeHead(statusCode, {
    "Content-Type": contentType,
    "Content-Length": String(payload.length),
    ...extraHeaders,
  });
  res.end(payload);
}

function sendJson(res, statusCode, payload) {
  sendBody(
    res,
    statusCode,
    JSON.stringify(payload),
    "application/json; charset=utf-8",
  );
}

function sendText(res, statusCode, body, contentType = "text/plain; charset=utf-8") {
  sendBody(res, statusCode, body, contentType, { "Cache-Control": "no-store" });
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
    body: asset.binary ? fs.readFileSync(assetFile) : fs.readFileSync(assetFile, "utf8"),
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

function getStaticContentType(filePath) {
  switch (path.extname(filePath).toLowerCase()) {
    case ".css":
      return "text/css; charset=utf-8";
    case ".html":
      return "text/html; charset=utf-8";
    case ".ico":
      return "image/x-icon";
    case ".js":
      return "application/javascript; charset=utf-8";
    case ".json":
      return "application/json; charset=utf-8";
    case ".png":
      return "image/png";
    case ".svg":
      return "image/svg+xml";
    case ".txt":
      return "text/plain; charset=utf-8";
    default:
      return "application/octet-stream";
  }
}

function tryServeFrontend(req, res, urlPath) {
  if ((req.method !== "GET" && req.method !== "HEAD") || !fs.existsSync(FRONTEND_DIST_DIR)) {
    return false;
  }

  const relativePath =
    urlPath === "/" ? "index.html" : urlPath.replace(/^\/+/, "");
  const requestedFile = path.resolve(FRONTEND_DIST_DIR, relativePath);
  const distRoot = path.resolve(FRONTEND_DIST_DIR);
  if (!requestedFile.startsWith(`${distRoot}${path.sep}`) && requestedFile !== distRoot) {
    sendJson(res, 403, errorPayload("forbidden"));
    return true;
  }

  if (fs.existsSync(requestedFile) && fs.statSync(requestedFile).isFile()) {
    sendBody(res, 200, fs.readFileSync(requestedFile), getStaticContentType(requestedFile), {
      "Cache-Control": "public, max-age=300",
    });
    return true;
  }

  if (path.extname(relativePath)) {
    return false;
  }

  const indexFile = path.join(FRONTEND_DIST_DIR, "index.html");
  if (!fs.existsSync(indexFile)) {
    return false;
  }

  sendBody(res, 200, fs.readFileSync(indexFile), "text/html; charset=utf-8", {
    "Cache-Control": "no-store",
  });
  return true;
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
  const headerConfig = normalizeRuleRequestHeaders(body, fallback);
  const relayChain = normalizeRelayChainPayload(
    body.relay_chain !== undefined ? body.relay_chain : fallback.relay_chain,
    { protocol: "tcp" },
  );

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
    relay_chain: relayChain,
    ...headerConfig,
  };
}

function normalizeRelayChainPayload(value, options = {}) {
  const relayChain = Array.isArray(value)
    ? value
    : value === undefined || value === null || value === ""
      ? []
      : [value];
  const normalized = [];
  const seen = new Set();
  for (const entry of relayChain) {
    const parsed = Number(entry);
    if (!Number.isInteger(parsed) || parsed <= 0) {
      throw new Error("relay_chain entries must be positive integer listener IDs");
    }
    if (seen.has(parsed)) {
      throw new Error("relay_chain entries must not contain duplicates");
    }
    seen.add(parsed);
    normalized.push(parsed);
  }

  const protocol = String(options.protocol || "tcp").trim().toLowerCase();
  if (protocol !== "tcp" && normalized.length > 0) {
    throw new Error("relay_chain is only supported for tcp protocol");
  }
  if (normalized.length === 0) {
    return [];
  }

  const relayListeners = listAllRelayListenersById();
  for (const listenerId of normalized) {
    const listener = relayListeners.get(listenerId);
    if (!listener) {
      throw new Error(`relay listener not found: ${listenerId}`);
    }
    if (listener.enabled === false) {
      throw new Error(`relay listener is disabled: ${listenerId}`);
    }
    const ownerAgent = getAgentById(String(listener.agent_id || ""));
    if (!ownerAgent) {
      throw new Error(`relay listener belongs to unknown agent: ${listenerId}`);
    }
  }
  return normalized;
}

function listAllRelayListenersById() {
  const listenersById = new Map();
  for (const agentId of getAllKnownAgentIds()) {
    const listeners = storage.loadRelayListenersForAgent(agentId);
    for (const listener of Array.isArray(listeners) ? listeners : []) {
      const parsedId = Number(listener?.id);
      if (!Number.isInteger(parsedId) || parsedId <= 0) {
        continue;
      }
      listenersById.set(parsedId, listener);
    }
  }
  return listenersById;
}

function getAllKnownAgentIds() {
  const candidateAgentIds = new Set();
  if (LOCAL_AGENT_ENABLED) {
    candidateAgentIds.add(LOCAL_AGENT_ID);
  }
  for (const agent of storage.loadRegisteredAgents()) {
    const agentId = String(agent?.id || "").trim();
    if (agentId) {
      candidateAgentIds.add(agentId);
    }
  }
  return candidateAgentIds;
}

function findRelayListenerReferenceAcrossAgents(listenerId, options = {}) {
  const excludeAgentIds = new Set(
    (Array.isArray(options.excludeAgentIds) ? options.excludeAgentIds : [])
      .map((id) => String(id || "").trim())
      .filter(Boolean),
  );
  for (const knownAgentId of getAllKnownAgentIds()) {
    if (excludeAgentIds.has(String(knownAgentId))) {
      continue;
    }
    const inUseByHttp = loadNormalizedRulesForAgent(knownAgentId).find((rule) =>
      Array.isArray(rule.relay_chain) && rule.relay_chain.includes(listenerId),
    );
    if (inUseByHttp) {
      return { protocol: "http", agentId: knownAgentId, ruleId: inUseByHttp.id };
    }
    const inUseByL4 = storage.loadL4RulesForAgent(knownAgentId).find((rule) =>
      Array.isArray(rule.relay_chain) && rule.relay_chain.includes(listenerId),
    );
    if (inUseByL4) {
      return { protocol: "l4", agentId: knownAgentId, ruleId: inUseByL4.id };
    }
  }
  return null;
}

function isProxyHeadersGloballyDisabled() {
  const value = String(process.env.PROXY_PASS_PROXY_HEADERS || "").trim().toLowerCase();
  return /^(0|false|no|off)$/.test(value);
}


function normalizeHost(value) {
  return String(value || "").trim().replace(/^\[(.*)\]$/, "$1");
}

function isWildcardDomain(value) {
  return /^\*\.[a-zA-Z0-9.-]+$/.test(normalizeHost(value));
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

function validateManagedCertificateHost(value, options = {}) {
  const { allowWildcard = false } = options;
  const host = normalizeHost(value);
  if (!host) return false;
  if (allowWildcard && isWildcardDomain(host)) return true;
  return validateNetworkHost(host);
}

function isIpAddress(value) {
  const host = normalizeHost(value);
  if (!host) return false;
  if (/^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$/.test(host)) return true;
  return /^[0-9A-Fa-f:.]+$/.test(host) && host.includes(":");
}

function parseRuleFrontendTarget(rule) {
  try {
    const frontendUrl = new URL(String(rule?.frontend_url || "").trim());
    return {
      protocol: frontendUrl.protocol,
      hostname: normalizeHost(frontendUrl.hostname).toLowerCase(),
    };
  } catch {
    return null;
  }
}

function isExactManagedCertificateMatch(certDomain, host) {
  return normalizeHost(certDomain).toLowerCase() === normalizeHost(host).toLowerCase();
}

function isWildcardManagedCertificateMatch(certDomain, host) {
  const pattern = normalizeHost(certDomain).toLowerCase();
  const target = normalizeHost(host).toLowerCase();
  if (!isWildcardDomain(pattern)) return false;
  const suffix = pattern.slice(2);
  if (!target.endsWith(`.${suffix}`)) return false;
  const targetParts = target.split(".");
  const suffixParts = suffix.split(".");
  return targetParts.length === suffixParts.length + 1;
}

function doesManagedCertificateMatchHost(cert, host) {
  if (!cert || !host) return false;
  if (cert.scope === "ip") {
    return isExactManagedCertificateMatch(cert.domain, host);
  }
  return (
    isExactManagedCertificateMatch(cert.domain, host) ||
    isWildcardManagedCertificateMatch(cert.domain, host)
  );
}

// --- L4 Tuning Validation Helpers ---

function isValidNginxTime(value) {
  if (typeof value !== "string") return false;
  return /^\d+[smhd]$/.test(value.trim());
}

function isValidNginxSize(value) {
  if (typeof value !== "string") return false;
  return /^\d+[km]$/i.test(value.trim());
}

function isNonNegativeInt(value) {
  const n = Number(value);
  return Number.isInteger(n) && n >= 0;
}

function isPositiveInt(value) {
  const n = Number(value);
  return Number.isInteger(n) && n > 0;
}

function isStrictBool(value) {
  return value === true || value === false;
}

function normalizeNginxTime(value, defaultValue, fieldPath) {
  if (value === undefined || value === null || value === "") return defaultValue;
  const str = String(value).trim();
  if (!isValidNginxTime(str)) {
    throw new Error(`${fieldPath} must be a valid nginx time (e.g. 10s, 5m, 1h, 1d), got: ${str}`);
  }
  return str;
}

function normalizeNginxSize(value, defaultValue, fieldPath) {
  if (value === undefined || value === null || value === "") return defaultValue;
  const str = String(value).trim().toLowerCase();
  if (!isValidNginxSize(str)) {
    throw new Error(`${fieldPath} must be a valid nginx size (e.g. 16k, 1m), got: ${str}`);
  }
  return str;
}

function normalizeNonNegativeInt(value, defaultValue, fieldPath) {
  if (value === undefined || value === null || value === "") return defaultValue;
  const n = Number(value);
  if (!Number.isInteger(n) || n < 0) {
    throw new Error(`${fieldPath} must be a non-negative integer, got: ${value}`);
  }
  return n;
}

function normalizeNullablePositiveInt(value, defaultValue, fieldPath) {
  if (value === undefined || value === null || value === "") return defaultValue;
  const n = Number(value);
  if (!Number.isInteger(n) || n < 1) {
    throw new Error(`${fieldPath} must be a positive integer or null, got: ${value}`);
  }
  return n;
}

function normalizeStrictBool(value, defaultValue, fieldPath) {
  if (value === undefined || value === null || value === "") return defaultValue;
  if (value === true || value === "true" || value === 1) return true;
  if (value === false || value === "false" || value === 0) return false;
  throw new Error(`${fieldPath} must be a boolean, got: ${value}`);
}

function buildDefaultL4Tuning(protocol) {
  const isUdp = protocol === "udp";
  return {
    listen: {
      reuseport: isUdp,
      backlog: null,
      so_keepalive: false,
      tcp_nodelay: true,
    },
    proxy: {
      connect_timeout: "10s",
      idle_timeout: isUdp ? "20s" : "10m",
      buffer_size: "16k",
      udp_proxy_requests: isUdp ? null : undefined,
      udp_proxy_responses: isUdp ? null : undefined,
    },
    upstream: {
      max_conns: 0,
      max_fails: 3,
      fail_timeout: "30s",
    },
    limit_conn: {
      key: "$binary_remote_addr",
      count: null,
      zone_size: "10m",
    },
    proxy_protocol: {
      decode: false,
      send: false,
    },
  };
}

function normalizeL4Tuning(tuning, protocol, prefix = "tuning") {
  const defaults = buildDefaultL4Tuning(protocol);
  const src = tuning && typeof tuning === "object" ? tuning : {};
  const isUdp = protocol === "udp";

  const listen = src.listen && typeof src.listen === "object" ? src.listen : {};
  const proxy = src.proxy && typeof src.proxy === "object" ? src.proxy : {};
  const upstream = src.upstream && typeof src.upstream === "object" ? src.upstream : {};
  const limitConn = src.limit_conn && typeof src.limit_conn === "object" ? src.limit_conn : {};
  const proxyProtocol = src.proxy_protocol && typeof src.proxy_protocol === "object" ? src.proxy_protocol : {};

  const result = {
    listen: {
      reuseport: normalizeStrictBool(listen.reuseport, defaults.listen.reuseport, `${prefix}.listen.reuseport`),
      backlog: normalizeNullablePositiveInt(listen.backlog, defaults.listen.backlog, `${prefix}.listen.backlog`),
      so_keepalive: normalizeStrictBool(listen.so_keepalive, defaults.listen.so_keepalive, `${prefix}.listen.so_keepalive`),
      tcp_nodelay: normalizeStrictBool(listen.tcp_nodelay, defaults.listen.tcp_nodelay, `${prefix}.listen.tcp_nodelay`),
    },
    proxy: {
      connect_timeout: normalizeNginxTime(proxy.connect_timeout, defaults.proxy.connect_timeout, `${prefix}.proxy.connect_timeout`),
      idle_timeout: normalizeNginxTime(proxy.idle_timeout, defaults.proxy.idle_timeout, `${prefix}.proxy.idle_timeout`),
      buffer_size: normalizeNginxSize(proxy.buffer_size, defaults.proxy.buffer_size, `${prefix}.proxy.buffer_size`),
    },
    upstream: {
      max_conns: normalizeNonNegativeInt(upstream.max_conns, defaults.upstream.max_conns, `${prefix}.upstream.max_conns`),
      max_fails: normalizeNonNegativeInt(upstream.max_fails, defaults.upstream.max_fails, `${prefix}.upstream.max_fails`),
      fail_timeout: normalizeNginxTime(upstream.fail_timeout, defaults.upstream.fail_timeout, `${prefix}.upstream.fail_timeout`),
    },
    limit_conn: {
      key: String(limitConn.key || defaults.limit_conn.key).trim() || defaults.limit_conn.key,
      count: normalizeNullablePositiveInt(limitConn.count, defaults.limit_conn.count, `${prefix}.limit_conn.count`),
      zone_size: normalizeNginxSize(limitConn.zone_size, defaults.limit_conn.zone_size, `${prefix}.limit_conn.zone_size`),
    },
    proxy_protocol: {
      decode: normalizeStrictBool(proxyProtocol.decode, defaults.proxy_protocol.decode, `${prefix}.proxy_protocol.decode`),
      send: normalizeStrictBool(proxyProtocol.send, defaults.proxy_protocol.send, `${prefix}.proxy_protocol.send`),
    },
  };

  // UDP-specific fields
  if (isUdp) {
    result.proxy.udp_proxy_requests = normalizeNullablePositiveInt(proxy.udp_proxy_requests, defaults.proxy.udp_proxy_requests, `${prefix}.proxy.udp_proxy_requests`);
    result.proxy.udp_proxy_responses = normalizeNullablePositiveInt(proxy.udp_proxy_responses, defaults.proxy.udp_proxy_responses, `${prefix}.proxy.udp_proxy_responses`);
  }

  return result;
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
    // Auto-detect resolve for domain hosts; explicit value takes precedence
    const autoResolve = !isIpAddress(host);
    const resolve = b?.resolve !== undefined
      ? (b.resolve === true || String(b.resolve).toLowerCase() === "true")
      : autoResolve;
    const backup = b?.backup === true || String(b?.backup || "").toLowerCase() === "true";
    const rawMaxConns = b?.max_conns !== undefined && b?.max_conns !== null && b?.max_conns !== "" ? Number(b.max_conns) : 0;
    if (b?.max_conns !== undefined && b?.max_conns !== null && b?.max_conns !== "" && (!Number.isInteger(rawMaxConns) || rawMaxConns < 0)) {
      throw new Error(`backends[].max_conns must be a non-negative integer, got: ${b.max_conns}`);
    }
    validBackends.push({ host, port, weight, resolve, backup, max_conns: rawMaxConns });
  }
  return validBackends;
}

function normalizeL4LoadBalancing(lb, defaultStrategy = "round_robin") {
  const strategy = String(lb?.strategy !== undefined ? lb.strategy : defaultStrategy).toLowerCase();
  const validStrategies = ["round_robin", "least_conn", "random", "hash"];
  const normalizedStrategy = validStrategies.includes(strategy) ? strategy : "round_robin";
  const hashKey = normalizedStrategy === "hash" ? String(lb?.hash_key || "$binary_remote_addr") : undefined;
  const zoneSize = String(lb?.zone_size || "128k");
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
      resolve: !isIpAddress(legacyUpstreamHost),
      backup: false,
      max_conns: 0,
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

  // Validate backup compatibility: only round_robin and least_conn support backup
  const hasBackupBackend = backends.some((b) => b.backup === true);
  if (hasBackupBackend && !["round_robin", "least_conn"].includes(loadBalancing.strategy)) {
    throw new Error(
      `backup backends are not supported with ${loadBalancing.strategy} strategy (only round_robin and least_conn)`
    );
  }

  // Normalize tuning: merge user input over defaults
  const rawTuning = body?.tuning !== undefined ? body.tuning : fallback?.tuning;
  const tuning = normalizeL4Tuning(rawTuning, protocol);
  const relayChain = normalizeRelayChainPayload(
    body.relay_chain !== undefined ? body.relay_chain : fallback.relay_chain,
    { protocol },
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
    tuning,
    relay_chain: relayChain,
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

function getHighestRelayListenerRevision(listeners = []) {
  return (Array.isArray(listeners) ? listeners : []).reduce(
    (max, listener) => Math.max(max, normalizeRevision(listener?.revision)),
    0,
  );
}

function getNextRelayListenerId() {
  let maxId = 0;
  const candidateAgentIds = new Set();
  if (LOCAL_AGENT_ENABLED) {
    candidateAgentIds.add(LOCAL_AGENT_ID);
  }
  for (const agent of storage.loadRegisteredAgents()) {
    const agentId = String(agent?.id || "").trim();
    if (agentId) {
      candidateAgentIds.add(agentId);
    }
  }
  for (const agentId of candidateAgentIds) {
    const listeners = storage.loadRelayListenersForAgent(agentId);
    for (const listener of Array.isArray(listeners) ? listeners : []) {
      maxId = Math.max(maxId, Number(listener?.id) || 0);
    }
  }
  return maxId + 1;
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



function getCertStoreDir(domain) {
  const safeDomain = normalizeHost(domain)
    .replace(/^\*\./, "_wildcard_.")
    .replace(/[<>:"/\\|?*]/g, "_");
  return path.join(MANAGED_CERTS_DIR, safeDomain);
}

function normalizeManagedCertificateAcmeInfo(value = {}) {
  const source = value && typeof value === "object" ? value : {};
  return {
    Main_Domain: String(source.Main_Domain || ""),
    KeyLength: String(source.KeyLength || ""),
    SAN_Domains: String(source.SAN_Domains || ""),
    Profile: String(source.Profile || ""),
    CA: String(source.CA || ""),
    Created: String(source.Created || ""),
    Renew: String(source.Renew || ""),
  };
}

function normalizeManagedCertificateReportStatus(value) {
  const status = String(value || "").trim().toLowerCase();
  return ["pending", "active", "error"].includes(status) ? status : "";
}

function normalizeManagedCertificateAgentReport(value = {}) {
  const source = value && typeof value === "object" ? value : {};
  return {
    status: normalizeManagedCertificateReportStatus(source.status),
    last_issue_at:
      source.last_issue_at !== undefined && source.last_issue_at !== null && String(source.last_issue_at).trim()
        ? String(source.last_issue_at)
        : null,
    last_error: String(source.last_error || ""),
    material_hash: String(source.material_hash || ""),
    acme_info: normalizeManagedCertificateAcmeInfo(source.acme_info || {}),
    updated_at:
      source.updated_at !== undefined && source.updated_at !== null && String(source.updated_at).trim()
        ? String(source.updated_at)
        : null,
  };
}

function normalizeManagedCertificateAgentReports(value = {}, targetAgentIds = []) {
  const source =
    value && typeof value === "object" && !Array.isArray(value) ? value : {};
  const allowedAgentIds = new Set(
    (Array.isArray(targetAgentIds) ? targetAgentIds : [])
      .map((item) => String(item || "").trim())
      .filter(Boolean),
  );
  const reports = {};
  for (const [agentIdRaw, report] of Object.entries(source)) {
    const agentId = String(agentIdRaw || "").trim();
    if (!agentId) continue;
    if (allowedAgentIds.size > 0 && !allowedAgentIds.has(agentId)) continue;
    reports[agentId] = normalizeManagedCertificateAgentReport(report);
  }
  return reports;
}

function getManagedCertificateAgentReport(cert, agentId) {
  const normalizedAgentId = String(agentId || "").trim();
  if (!normalizedAgentId) return null;
  const reports =
    cert?.agent_reports && typeof cert.agent_reports === "object"
      ? cert.agent_reports
      : null;
  if (!reports || Array.isArray(reports)) return null;
  return reports[normalizedAgentId]
    ? normalizeManagedCertificateAgentReport(reports[normalizedAgentId])
    : null;
}

function buildManagedCertificateViewForAgent(cert, agentId) {
  const report = getManagedCertificateAgentReport(cert, agentId);
  if (!report) return cert;

  return {
    ...cert,
    status: report.status || cert.status,
    last_issue_at: report.last_issue_at,
    last_error: report.last_error,
    material_hash: report.material_hash,
    acme_info: normalizeManagedCertificateAcmeInfo(report.acme_info || {}),
  };
}

function normalizeAgentManagedCertificateReportPayload(value = {}) {
  const source = value && typeof value === "object" ? value : {};
  return {
    id: Number.isFinite(Number(source.id)) && Number(source.id) > 0 ? Number(source.id) : null,
    domain: normalizeHost(source.domain || "").toLowerCase(),
    status: normalizeManagedCertificateReportStatus(source.status),
    last_issue_at:
      source.last_issue_at !== undefined && source.last_issue_at !== null && String(source.last_issue_at).trim()
        ? String(source.last_issue_at)
        : null,
    last_error: String(source.last_error || ""),
    material_hash: String(source.material_hash || ""),
    acme_info: normalizeManagedCertificateAcmeInfo(source.acme_info || {}),
    updated_at:
      source.updated_at !== undefined && source.updated_at !== null && String(source.updated_at).trim()
        ? String(source.updated_at)
        : null,
  };
}

function normalizeManagedCertificateUsage(value, fallback = "https") {
  const next = String(value === undefined ? fallback : value || "")
    .trim()
    .toLowerCase();
  const allowed = new Set(["https", "relay_tunnel", "relay_ca", "mixed"]);
  if (!allowed.has(next)) {
    throw new Error("usage must be https, relay_tunnel, relay_ca, or mixed");
  }
  return next;
}

function normalizeManagedCertificateType(value, fallback = "acme") {
  const next = String(value === undefined ? fallback : value || "")
    .trim()
    .toLowerCase();
  const allowed = new Set(["acme", "uploaded", "internal_ca"]);
  if (!allowed.has(next)) {
    throw new Error("certificate_type must be acme, uploaded, or internal_ca");
  }
  return next;
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

  if (!domain || !validateManagedCertificateHost(domain, { allowWildcard: scope === "domain" })) {
    throw new Error("domain must be a valid domain or IP");
  }
  if (!["domain", "ip"].includes(scope)) {
    throw new Error("scope must be domain or ip");
  }
  if (!["master_cf_dns", "local_http01"].includes(issuerMode)) {
    throw new Error("issuer_mode must be master_cf_dns or local_http01");
  }
  if (scope === "ip" && issuerMode !== "local_http01") {
    throw new Error("ip certificates must use local_http01");
  }
  if (scope === "ip" && isWildcardDomain(domain)) {
    throw new Error("ip certificates do not support wildcard domains");
  }
  if (scope === "domain" && isWildcardDomain(domain) && issuerMode !== "master_cf_dns") {
    throw new Error("wildcard domain certificates must use master_cf_dns");
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
    material_hash:
      body.material_hash !== undefined
        ? String(body.material_hash || "")
        : String(fallback.material_hash || ""),
    agent_reports:
      body.agent_reports !== undefined
        ? normalizeManagedCertificateAgentReports(body.agent_reports, targetAgentIds)
        : normalizeManagedCertificateAgentReports(fallback.agent_reports || {}, targetAgentIds),
    acme_info:
      body.acme_info !== undefined
        ? normalizeManagedCertificateAcmeInfo(body.acme_info)
        : normalizeManagedCertificateAcmeInfo(fallback.acme_info || {}),
    tags:
      body.tags !== undefined ? normalizeTags(body.tags) : normalizeTags(fallback.tags || []),
    usage: normalizeManagedCertificateUsage(
      body.usage !== undefined ? body.usage : fallback.usage,
      "https",
    ),
    certificate_type: normalizeManagedCertificateType(
      body.certificate_type !== undefined ? body.certificate_type : fallback.certificate_type,
      "acme",
    ),
    self_signed:
      body.self_signed !== undefined
        ? !!body.self_signed
        : fallback.self_signed === true,
  };
}

function getKnownManagedCertificateTargetAgentIds() {
  const ids = new Set();
  if (LOCAL_AGENT_ENABLED) {
    ids.add(LOCAL_AGENT_ID);
  }
  for (const agent of storage.loadRegisteredAgents()) {
    const agentId = String(agent?.id || "").trim();
    if (agentId) {
      ids.add(agentId);
    }
  }
  return ids;
}

function filterKnownManagedCertificateTargetAgentIds(agentIds, knownAgentIds = null) {
  const allowedAgentIds =
    knownAgentIds instanceof Set ? knownAgentIds : getKnownManagedCertificateTargetAgentIds();
  return [
    ...new Set(
      (Array.isArray(agentIds) ? agentIds : [])
        .map((item) => String(item || "").trim())
        .filter((agentId) => agentId && allowedAgentIds.has(agentId)),
    ),
  ];
}

function normalizeStoredManagedCertificate(cert, suggestedId = null) {
  const knownAgentIds = getKnownManagedCertificateTargetAgentIds();
  const normalized = normalizeManagedCertificatePayload(
    {
      ...(cert || {}),
      target_agent_ids: filterKnownManagedCertificateTargetAgentIds(
        cert?.target_agent_ids,
        knownAgentIds,
      ),
    },
    cert || {},
    suggestedId,
  );
  normalized.revision = normalizeRevision(cert?.revision);
  return normalized;
}



function getManagedCertificateById(certId) {
  return storage.loadManagedCertificates().find((item) => Number(item.id) === Number(certId)) || null;
}

function getManagedCertificatesForAgent(agentId) {
  return storage.loadManagedCertificates()
    .filter((cert) =>
      Array.isArray(cert.target_agent_ids) && cert.target_agent_ids.includes(agentId),
    )
    .map((cert) => buildManagedCertificateViewForAgent(cert, agentId));
}

function compareManagedCertificateMatchPriority(left, right, agentId) {
  const leftWildcard = isWildcardDomain(left?.domain || "");
  const rightWildcard = isWildcardDomain(right?.domain || "");
  if (leftWildcard !== rightWildcard) return leftWildcard ? 1 : -1;

  const leftTargetsAgent =
    Array.isArray(left?.target_agent_ids) && left.target_agent_ids.includes(agentId);
  const rightTargetsAgent =
    Array.isArray(right?.target_agent_ids) && right.target_agent_ids.includes(agentId);
  if (leftTargetsAgent !== rightTargetsAgent) return leftTargetsAgent ? -1 : 1;

  return normalizeRevision(right?.revision) - normalizeRevision(left?.revision);
}

function buildManagedCertificateAutoTargetTag(agentId) {
  return `auto_target:${String(agentId || "").trim()}`;
}

function hasManagedCertificateTag(cert, tag) {
  return Array.isArray(cert?.tags) && cert.tags.includes(tag);
}

function isAutoManagedCertificate(cert) {
  return hasManagedCertificateTag(cert, "auto");
}

function hasManagedCertificateAutoTarget(cert, agentId) {
  const tag = buildManagedCertificateAutoTargetTag(agentId);
  return Boolean(agentId) && hasManagedCertificateTag(cert, tag);
}

function addManagedCertificateAutoTarget(tags, agentId) {
  if (!agentId) return normalizeTags(tags || []);
  return normalizeTags([...(Array.isArray(tags) ? tags : []), buildManagedCertificateAutoTargetTag(agentId)]);
}

function removeManagedCertificateAutoTarget(tags, agentId) {
  const targetTag = buildManagedCertificateAutoTargetTag(agentId);
  return normalizeTags((Array.isArray(tags) ? tags : []).filter((tag) => tag !== targetTag));
}

function shouldRecycleManagedCertificateForAgent(cert, agentId) {
  return isAutoManagedCertificate(cert) || hasManagedCertificateAutoTarget(cert, agentId);
}

function findBestManagedCertificateForHost(agentId, host, scope) {
  return storage.loadManagedCertificates()
    .filter((cert) => cert.enabled !== false)
    .filter((cert) => cert.scope === scope)
    .filter((cert) => doesManagedCertificateMatchHost(cert, host))
    .sort((left, right) => compareManagedCertificateMatchPriority(left, right, agentId))[0] || null;
}

function chooseAutoManagedCertificateIssuerMode(agent, host, scope) {
  if (!agentHasCapability(agent, "cert_install")) {
    throw new Error(`agent does not support unified certificate install: ${agent?.name || agent?.id || "unknown"}`);
  }
  if (scope === "ip") {
    if (!agentHasCapability(agent, "local_acme")) {
      throw new Error(`agent does not support local ACME issuance for IP HTTPS: ${agent.name || agent.id}`);
    }
    return "local_http01";
  }
  if (MANAGED_CERTS_ENABLED) {
    return "master_cf_dns";
  }
  if (agentHasCapability(agent, "local_acme")) {
    return "local_http01";
  }
  throw new Error(`no available unified certificate issuer for ${host}`);
}

async function ensureManagedCertificateForRule(agentId, rule, options = {}) {
  const { applyNow = AUTO_APPLY } = options;
  const target = parseRuleFrontendTarget(rule);
  if (!target || target.protocol !== "https:") return null;

  const agent = getAgentById(agentId);
  if (!agent) throw new Error("agent not found");

  const scope = isIpAddress(target.hostname) ? "ip" : "domain";
  let cert = findBestManagedCertificateForHost(agentId, target.hostname, scope);
  if (cert) {
    if (!Array.isArray(cert.target_agent_ids) || !cert.target_agent_ids.includes(agentId)) {
      const nextTargets = [...new Set([...(cert.target_agent_ids || []), agentId])];
      const updated = normalizeManagedCertificatePayload(
        {
          ...cert,
          target_agent_ids: nextTargets,
          enabled: cert.enabled !== false,
          tags: addManagedCertificateAutoTarget(cert.tags || [], agentId),
        },
        cert,
        cert.id,
      );
      validateManagedCertificateTargets(updated);
      const prepared = prepareManagedCertificateForSave(cert, updated);
      prepared.revision = storage.getNextGlobalRevision();
      updateManagedCertificate(cert.id, () => prepared);
      if (prepared.enabled && prepared.scope === "domain" && prepared.issuer_mode === "master_cf_dns") {
        cert = await issueManagedCertificateById(cert.id, { bumpRevision: false, applyNow });
      } else {
        await syncManagedCertificateAgentIds(nextTargets, { applyNow });
        cert = getManagedCertificateById(cert.id);
      }
    }
    return cert;
  }

  const certs = storage.loadManagedCertificates();
  const maxId = certs.reduce((max, item) => Math.max(max, Number(item.id) || 0), 0);
  const issuerMode = chooseAutoManagedCertificateIssuerMode(agent, target.hostname, scope);
  const created = normalizeManagedCertificatePayload(
    {
      domain: target.hostname,
      enabled: true,
      scope,
      issuer_mode: issuerMode,
      target_agent_ids: [agentId],
      tags: addManagedCertificateAutoTarget(
        normalizeTags([...(Array.isArray(rule?.tags) ? rule.tags : []), "auto"]),
        agentId,
      ),
    },
    {},
    maxId + 1,
  );
  validateManagedCertificateTargets(created);
  const prepared = prepareManagedCertificateForSave(null, created);
  certs.push({ ...prepared, revision: storage.getNextGlobalRevision() });
  storage.saveManagedCertificates(certs);

  if (prepared.scope === "domain" && prepared.issuer_mode === "master_cf_dns") {
    return issueManagedCertificateById(prepared.id, { bumpRevision: false, applyNow });
  }
  await syncManagedCertificateAgentIds(prepared.target_agent_ids || [], { applyNow });
  return getManagedCertificateById(prepared.id);
}

async function detachManagedCertificateFromAgent(certId, agentId, options = {}) {
  const { applyNow = AUTO_APPLY } = options;
  const certs = storage.loadManagedCertificates();
  const index = certs.findIndex((item) => Number(item.id) === Number(certId));
  if (index === -1) return null;

  const existing = certs[index];
  if (!Array.isArray(existing.target_agent_ids) || !existing.target_agent_ids.includes(agentId)) {
    return existing;
  }

  const remainingTargets = existing.target_agent_ids.filter((id) => id !== agentId);
  if (remainingTargets.length > 0) {
    const nextCert = normalizeManagedCertificatePayload(
      {
        ...existing,
        target_agent_ids: remainingTargets,
        tags: removeManagedCertificateAutoTarget(existing.tags || [], agentId),
      },
      existing,
      certId,
    );
    nextCert.status = existing.status;
    nextCert.last_issue_at = existing.last_issue_at || null;
    nextCert.last_error = existing.last_error || "";
    nextCert.revision = storage.getNextGlobalRevision();
    certs[index] = nextCert;
    storage.saveManagedCertificates(certs);
    await syncManagedCertificateAgentIds(getManagedCertificateAffectedAgentIds(existing, nextCert), {
      applyNow,
    });
    return nextCert;
  }

  if (!isAutoManagedCertificate(existing)) {
    const nextCert = normalizeManagedCertificatePayload(
      {
        ...existing,
        target_agent_ids: [],
        tags: removeManagedCertificateAutoTarget(existing.tags || [], agentId),
      },
      existing,
      certId,
    );
    nextCert.status = existing.status;
    nextCert.last_issue_at = existing.last_issue_at || null;
    nextCert.last_error = existing.last_error || "";
    nextCert.revision = storage.getNextGlobalRevision();
    certs[index] = nextCert;
    storage.saveManagedCertificates(certs);
    await syncManagedCertificateAgentIds(getManagedCertificateAffectedAgentIds(existing, nextCert), {
      applyNow,
    });
    return nextCert;
  }

  const deleted = certs.splice(index, 1)[0];
  storage.saveManagedCertificates(certs);
  cleanupManagedCertificateArtifacts(deleted.domain);
  for (const targetAgentId of deleted.target_agent_ids || []) {
    if (targetAgentId === LOCAL_AGENT_ID) {
      persistManagedCertificateBundleForAgent(targetAgentId);
      persistManagedCertificatePolicyForAgent(targetAgentId);
    }
    if (applyNow) {
      await applyAgent(targetAgentId);
    }
  }
  return deleted;
}

function hasMatchingHttpsRuleForCertificateInRules(rules, cert) {
  return (Array.isArray(rules) ? rules : []).some((rule) => {
    if (!rule || rule.enabled === false) return false;
    const target = parseRuleFrontendTarget(rule);
    if (!target || target.protocol !== "https:") return false;
    return doesManagedCertificateMatchHost(cert, target.hostname);
  });
}

async function cleanupUnusedManagedCertificatesForAgent(agentId, rules = null, options = {}) {
  const currentCerts = getManagedCertificatesForAgent(agentId);
  const removed = [];
  const ruleSet = Array.isArray(rules) ? rules : storage.loadRulesForAgent(agentId);
  for (const cert of currentCerts) {
    if (hasMatchingHttpsRuleForCertificateInRules(ruleSet, cert)) continue;
    if (!shouldRecycleManagedCertificateForAgent(cert, agentId)) continue;
    await detachManagedCertificateFromAgent(cert.id, agentId, options);
    removed.push(cert.id);
  }
  return removed;
}

function getHighestManagedCertificateRevisionForAgent(agentId) {
  const agent = getAgentById(agentId);
  if (!agent || !agentHasCapability(agent, "cert_install")) return 0;
  return storage.loadManagedCertificates().reduce((max, cert) => {
    if (!Array.isArray(cert.target_agent_ids) || !cert.target_agent_ids.includes(agentId)) {
      return max;
    }
    return Math.max(max, normalizeRevision(cert.revision));
  }, 0);
}

function getManagedCertificateAcmeName(domain) {
  const normalizedDomain = normalizeHost(domain).toLowerCase();
  return isWildcardDomain(normalizedDomain) ? normalizedDomain.slice(2) : normalizedDomain;
}

function readManagedCertificateAcmeInfo(domain) {
  if (!fs.existsSync(ACME_SCRIPT)) {
    return normalizeManagedCertificateAcmeInfo();
  }

  const result = spawnSync(ACME_SCRIPT, ["--list", ...ACME_COMMON_ARGS], {
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
    env: process.env,
  });

  if (result.error || result.status !== 0) {
    return normalizeManagedCertificateAcmeInfo();
  }

  const lines = String(result.stdout || "")
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean);
  if (!lines.length) return normalizeManagedCertificateAcmeInfo();

  const headers = lines[0].split(/\s+/);
  const targetName = getManagedCertificateAcmeName(domain);
  const row = lines.slice(1).find((line) => {
    const columns = line.split(/\s+/);
    const mainDomain = String(columns[0] || "").trim().toLowerCase();
    return mainDomain === targetName;
  });
  if (!row) return normalizeManagedCertificateAcmeInfo();

  const columns = row.split(/\s+/);
  const mapped = {};
  headers.forEach((header, index) => {
    mapped[header] = columns[index] || "";
  });

  return normalizeManagedCertificateAcmeInfo({
    Main_Domain: mapped.Main_Domain || mapped.Domain || targetName,
    KeyLength: mapped.KeyLength || "",
    SAN_Domains: mapped.SAN_Domains || "",
    Profile: mapped.Profile || "",
    CA: mapped.CA || "",
    Created: mapped.Created || "",
    Renew: mapped.Renew || "",
  });
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

function hashManagedCertificateMaterial(material) {
  if (!material || !material.cert_pem || !material.key_pem) return "";
  return crypto
    .createHash("sha256")
    .update(String(material.cert_pem))
    .update("\n---\n")
    .update(String(material.key_pem))
    .digest("hex");
}

function getManagedCertificateMaterialHash(domain) {
  return hashManagedCertificateMaterial(readManagedCertificateMaterial(domain));
}

function buildManagedCertificateBundleForAgent(agentId) {
  return storage.loadManagedCertificates()
    .filter((cert) => cert.enabled)
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

function buildManagedCertificatePolicyForAgent(agentId) {
  return storage.loadManagedCertificates()
    .filter((cert) => Array.isArray(cert.target_agent_ids) && cert.target_agent_ids.includes(agentId))
    .map((cert) => {
      const view = buildManagedCertificateViewForAgent(cert, agentId);
      return {
        id: cert.id,
        domain: cert.domain,
        enabled: cert.enabled !== false,
        scope: cert.scope,
        issuer_mode: cert.issuer_mode,
        status: view.status,
        last_issue_at: view.last_issue_at || null,
        last_error: view.last_error || "",
        acme_info: normalizeManagedCertificateAcmeInfo(view.acme_info || {}),
        tags: normalizeTags(cert.tags || []),
        revision: normalizeRevision(cert.revision),
        usage: cert.usage,
        certificate_type: cert.certificate_type,
        self_signed: cert.self_signed === true,
      };
    });
}

function getManagedCertBundleFileForAgent(agentId) {
  if (agentId === LOCAL_AGENT_ID) return LOCAL_MANAGED_CERT_BUNDLE_JSON;
  return path.join(AGENT_RULES_DIR, `${agentId}.managed-certs.json`);
}

function getManagedCertPolicyFileForAgent(agentId) {
  if (agentId === LOCAL_AGENT_ID) return LOCAL_MANAGED_CERT_POLICY_JSON;
  return path.join(AGENT_RULES_DIR, `${agentId}.managed-certs.policy.json`);
}

function persistManagedCertificateBundleForAgent(agentId) {
  writeJsonFile(getManagedCertBundleFileForAgent(agentId), buildManagedCertificateBundleForAgent(agentId));
}

function persistManagedCertificatePolicyForAgent(agentId) {
  writeJsonFile(getManagedCertPolicyFileForAgent(agentId), buildManagedCertificatePolicyForAgent(agentId));
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
  const highestL4Revision = getHighestL4RuleRevision(storage.loadL4RulesForAgent(agentId));
  const highestRelayListenerRevision = getHighestRelayListenerRevision(
    storage.loadRelayListenersForAgent(agentId),
  );
  const highestManagedCertRevision = getHighestManagedCertificateRevisionForAgent(agentId);
  const highestConfigRevision = Math.max(
    highestRuleRevision,
    highestL4Revision,
    highestRelayListenerRevision,
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
  agent.platform = String(agent.platform || "").trim();
  agent.desired_version = String(agent.desired_version || "").trim();
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
  const state = storage.loadLocalAgentState();
  return {
    id: LOCAL_AGENT_ID,
    name: LOCAL_AGENT_NAME,
    agent_url: LOCAL_AGENT_URL,
    version: AGENT_VERSION,
    desired_version: state.desired_version,
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
    platform: String(hydrated.platform || ""),
    desired_version: String(hydrated.desired_version || ""),
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
    http_rules_count: typeof hydrated.http_rules_count === 'number' ? hydrated.http_rules_count : 0,
    l4_rules_count: typeof hydrated.l4_rules_count === 'number' ? hydrated.l4_rules_count : 0,
  };
}

function getAgentById(agentId) {
  if (LOCAL_AGENT_ENABLED && agentId === LOCAL_AGENT_ID) {
    return makeLocalAgent();
  }
  const agent = storage.loadRegisteredAgents().find((item) => item.id === agentId) || null;
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
    const details = [result.stderr, result.stdout]
      .filter((item) => typeof item === "string" && item.trim())
      .join("\n")
      .trim() || `exit code ${result.status}`;
    throw new Error(details);
  }
}

function prepareLocalManagedCertificateBundle() {
  persistManagedCertificateBundleForAgent(LOCAL_AGENT_ID);
  return getManagedCertBundleFileForAgent(LOCAL_AGENT_ID);
}

function prepareLocalManagedCertificatePolicy() {
  persistManagedCertificatePolicyForAgent(LOCAL_AGENT_ID);
  return getManagedCertPolicyFileForAgent(LOCAL_AGENT_ID);
}

function applyNginxConfig() {
  const extraEnv = {
    PANEL_L4_RULES_JSON: path.join(L4_RULES_DIR, `${LOCAL_AGENT_ID}.json`),
    PANEL_MANAGED_CERTS_SYNC_JSON: prepareLocalManagedCertificateBundle(),
    PANEL_MANAGED_CERTS_POLICY_JSON: prepareLocalManagedCertificatePolicy(),
  };
  const nginxArgsBase = ["-e", NGINX_ERROR_LOG_FILE];

  if (APPLY_COMMAND) {
    runChecked(APPLY_COMMAND, APPLY_COMMAND_ARGS, extraEnv);
    return;
  }

  if (!GENERATOR_SCRIPT || !fs.existsSync(GENERATOR_SCRIPT)) {
    throw new Error("no built-in local apply runtime is bundled; set PANEL_GENERATOR_SCRIPT or PANEL_APPLY_COMMAND");
  }

  // --- Diff-based apply with rollback ---
  const streamDynamicDir = process.env.NRE_STREAM_DYNAMIC_DIR || "/etc/nginx/stream-conf.d/dynamic";
  const streamBaseDir = path.dirname(streamDynamicDir);
  const limitConnZonesFile = path.join(streamBaseDir, "limit_conn_zones.inc");
  const dynamicDir = process.env.NRE_DYNAMIC_DIR || "/etc/nginx/conf.d/dynamic";
  const backupSuffix = `.bak.${Date.now()}`;

  // Snapshot current config files for diff comparison
  function snapshotDir(dir) {
    const snapshot = {};
    try {
      if (!fs.existsSync(dir)) return snapshot;
      for (const file of fs.readdirSync(dir)) {
        if (file.endsWith(".conf")) {
          snapshot[file] = fs.readFileSync(path.join(dir, file), "utf8");
        }
      }
    } catch { /* ignore */ }
    return snapshot;
  }

  function snapshotFile(filePath) {
    try {
      if (fs.existsSync(filePath)) return fs.readFileSync(filePath, "utf8");
    } catch { /* ignore */ }
    return null;
  }

  function backupFile(filePath, suffix) {
    const backupPath = filePath + suffix;
    try {
      if (fs.existsSync(filePath)) {
        fs.copyFileSync(filePath, backupPath);
      }
    } catch { /* ignore */ }
    return backupPath;
  }

  function restoreFile(backupPath, filePath) {
    try {
      if (fs.existsSync(backupPath)) {
        fs.copyFileSync(backupPath, filePath);
        fs.unlinkSync(backupPath);
      }
    } catch { /* ignore */ }
  }

  function backupDir(dir, suffix) {
    const backupPath = dir + suffix;
    try {
      if (fs.existsSync(dir)) {
        fs.cpSync(dir, backupPath, { recursive: true });
      }
    } catch { /* ignore */ }
    return backupPath;
  }

  function restoreDir(backupPath, dir) {
    try {
      if (fs.existsSync(backupPath)) {
        fs.rmSync(dir, { recursive: true, force: true });
        fs.renameSync(backupPath, dir);
      }
    } catch { /* ignore */ }
  }

  function cleanupBackup(backupPath) {
    try {
      if (fs.existsSync(backupPath)) {
        fs.rmSync(backupPath, { recursive: true, force: true });
      }
    } catch { /* ignore */ }
  }

  function diffSnapshots(before, after) {
    const added = [];
    const modified = [];
    const removed = [];
    for (const key of Object.keys(after)) {
      if (!(key in before)) added.push(key);
      else if (before[key] !== after[key]) modified.push(key);
    }
    for (const key of Object.keys(before)) {
      if (!(key in after)) removed.push(key);
    }
    return { added, modified, removed, hasChanges: added.length + modified.length + removed.length > 0 };
  }

  // Take snapshots before generation
  const beforeStream = snapshotDir(streamDynamicDir);
  const beforeHttp = snapshotDir(dynamicDir);
  const beforeLimitConn = snapshotFile(limitConnZonesFile);

  // Backup current configs
  const streamBackup = backupDir(streamDynamicDir, backupSuffix);
  const httpBackup = backupDir(dynamicDir, backupSuffix);
  const limitConnBackup = backupFile(limitConnZonesFile, backupSuffix);

  try {
    // Run generator
    runChecked(GENERATOR_SCRIPT, [], extraEnv);

    // Take snapshots after generation
    const afterStream = snapshotDir(streamDynamicDir);
    const afterHttp = snapshotDir(dynamicDir);
    const afterLimitConn = snapshotFile(limitConnZonesFile);

    const streamDiff = diffSnapshots(beforeStream, afterStream);
    const httpDiff = diffSnapshots(beforeHttp, afterHttp);
    const limitConnChanged = beforeLimitConn !== afterLimitConn;

    if (!streamDiff.hasChanges && !httpDiff.hasChanges && !limitConnChanged) {
      console.log("[apply] No config changes detected, skipping reload");
      cleanupBackup(streamBackup);
      cleanupBackup(httpBackup);
      cleanupBackup(limitConnBackup);
      return;
    }

    // Log change summary
    const changes = [];
    if (streamDiff.added.length) changes.push(`L4 added: ${streamDiff.added.length}`);
    if (streamDiff.modified.length) changes.push(`L4 modified: ${streamDiff.modified.length}`);
    if (streamDiff.removed.length) changes.push(`L4 removed: ${streamDiff.removed.length}`);
    if (limitConnChanged) changes.push("limit_conn_zones updated");
    if (httpDiff.added.length) changes.push(`HTTP added: ${httpDiff.added.length}`);
    if (httpDiff.modified.length) changes.push(`HTTP modified: ${httpDiff.modified.length}`);
    if (httpDiff.removed.length) changes.push(`HTTP removed: ${httpDiff.removed.length}`);
    console.log(`[apply] Config changes: ${changes.join(", ")}`);

    // Validate with nginx -t
    try {
      runChecked(NGINX_BIN, [...nginxArgsBase, "-t"], extraEnv);
    } catch (testErr) {
      // Rollback on validation failure
      console.error("[apply] nginx -t failed, rolling back config");
      restoreDir(streamBackup, streamDynamicDir);
      restoreDir(httpBackup, dynamicDir);
      restoreFile(limitConnBackup, limitConnZonesFile);
      throw new Error(`nginx config validation failed: ${testErr.message}`);
    }

    // Reload
    runChecked(NGINX_BIN, [...nginxArgsBase, "-s", "reload"], extraEnv);
    console.log("[apply] nginx reloaded successfully");

    // Cleanup backups
    cleanupBackup(streamBackup);
    cleanupBackup(httpBackup);
    cleanupBackup(limitConnBackup);
  } catch (err) {
    // Ensure backups are cleaned up even on unexpected errors
    cleanupBackup(streamBackup);
    cleanupBackup(httpBackup);
    cleanupBackup(limitConnBackup);
    throw err;
  }
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
    status: "运行中",
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
              status: "解析失败",
              error: e.message,
            });
          }
        });
      })
      .on("error", (err) => {
        resolve({
          activeConnections: "0",
          totalRequests: "0",
          status: "状态获取失败",
          error: err.message,
        });
      });
  });
}
async function hydrateAgents() {
  const registered = storage.loadRegisteredAgents();
  const enriched = [];

  if (LOCAL_AGENT_ENABLED) {
    const localAgent = makeLocalAgent();
    localAgent.http_rules_count = (storage.loadRulesForAgent(LOCAL_AGENT_ID) || []).length;
    localAgent.l4_rules_count = (storage.loadL4RulesForAgent(LOCAL_AGENT_ID) || []).length;
    enriched.push(sanitizeAgent(localAgent));
  }

  for (const agent of registered) {
    agent.http_rules_count = (storage.loadRulesForAgent(agent.id) || []).length;
    agent.l4_rules_count = (storage.loadL4RulesForAgent(agent.id) || []).length;
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
  const certs = storage.loadManagedCertificates();
  const index = certs.findIndex((item) => Number(item.id) === Number(certId));
  if (index === -1) throw new Error("certificate not found");
  certs[index] = updater({ ...certs[index] });
  storage.saveManagedCertificates(certs);
  return certs[index];
}

function runManagedCertificateHelper(domain, command = "issue") {
  const targetDir = getCertStoreDir(domain);
  runChecked(
    "sh",
    [MANAGED_CERT_HELPER_SCRIPT, command, domain, targetDir],
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

async function syncManagedCertificateTargets(cert, options = {}) {
  const targetIds = Array.isArray(cert?.target_agent_ids) ? cert.target_agent_ids : [];
  return syncManagedCertificateAgentIds(targetIds, options);
}

async function syncManagedCertificateAgentIds(agentIds, options = {}) {
  const { applyNow = AUTO_APPLY } = options;
  const targetIds = [...new Set((Array.isArray(agentIds) ? agentIds : []).filter(Boolean))];
  for (const agentId of targetIds) {
    const agent = getAgentById(agentId);
    if (!agent) continue;
    if (!agentHasCapability(agent, "cert_install")) continue;
    if (agentId === LOCAL_AGENT_ID) {
      persistManagedCertificateBundleForAgent(agentId);
      persistManagedCertificatePolicyForAgent(agentId);
    }
    if (applyNow) {
      await applyAgent(agentId);
      if (agentId === LOCAL_AGENT_ID) {
        reconcileLocalHttp01CertificatesForAgent(getAgentById(agentId));
      }
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
  if (cert.issuer_mode === "master_cf_dns") {
    if (cert.certificate_type !== "acme") {
      throw new Error("master_cf_dns certificates must use certificate_type=acme");
    }
    const targets = Array.isArray(cert.target_agent_ids) ? cert.target_agent_ids : [];
    if (!LOCAL_AGENT_ENABLED || targets.length !== 1 || targets[0] !== LOCAL_AGENT_ID) {
      throw new Error("master_cf_dns certificates must target only the local master agent");
    }
  }

  for (const agentId of cert.target_agent_ids || []) {
    const agent = getAgentById(agentId);
    if (!agent) {
      throw new Error(`target agent not found: ${agentId}`);
    }
    if (!agentHasCapability(agent, "cert_install")) {
      throw new Error(`target agent does not support certificate install: ${agent.name || agentId}`);
    }
    if (cert.issuer_mode === "local_http01" && !agentHasCapability(agent, "local_acme")) {
      throw new Error(`target agent does not support local ACME issuance: ${agent.name || agentId}`);
    }
  }
}

function shouldResetManagedCertificateStatus(previousCert, nextCert) {
  if (!previousCert) return nextCert.enabled !== false;
  return (
    previousCert.domain !== nextCert.domain ||
    previousCert.scope !== nextCert.scope ||
    previousCert.issuer_mode !== nextCert.issuer_mode ||
    previousCert.enabled !== nextCert.enabled ||
    JSON.stringify(previousCert.target_agent_ids || []) !==
      JSON.stringify(nextCert.target_agent_ids || [])
  );
}

function prepareManagedCertificateForSave(previousCert, nextCert) {
  if (!shouldResetManagedCertificateStatus(previousCert, nextCert)) {
    return nextCert;
  }
  return {
    ...nextCert,
    status: nextCert.enabled === false ? nextCert.status : "pending",
    last_error: nextCert.enabled === false ? nextCert.last_error : "",
  };
}

function hasMatchingHttpsRuleForCertificate(agentId, cert) {
  return hasMatchingHttpsRuleForCertificateInRules(storage.loadRulesForAgent(agentId), cert);
}

function updateManagedCertificateAgentReportSnapshot(cert, agentId, snapshot) {
  const normalizedAgentId = String(agentId || "").trim();
  if (!normalizedAgentId) return cert;
  const nextSnapshot = normalizeManagedCertificateAgentReport(snapshot || {});
  return {
    ...cert,
    agent_reports: {
      ...(cert?.agent_reports && typeof cert.agent_reports === "object" && !Array.isArray(cert.agent_reports)
        ? cert.agent_reports
        : {}),
      [normalizedAgentId]: nextSnapshot,
    },
  };
}

function findManagedCertificateReportForAgent(cert, reportsById, reportsByDomain) {
  const certId = Number(cert?.id);
  if (Number.isFinite(certId) && reportsById.has(certId)) {
    return reportsById.get(certId);
  }
  const domainKey = normalizeHost(cert?.domain || "").toLowerCase();
  return domainKey ? reportsByDomain.get(domainKey) || null : null;
}

function applyAgentManagedCertificateReports(agent, reports) {
  if (!agent || !agent.id || !Array.isArray(reports) || !reports.length) {
    return new Set();
  }

  const reportsById = new Map();
  const reportsByDomain = new Map();
  for (const report of reports) {
    if (!report) continue;
    if (Number.isFinite(Number(report.id)) && Number(report.id) > 0) {
      reportsById.set(Number(report.id), report);
    }
    const domain = normalizeHost(report.domain || "").toLowerCase();
    if (domain) {
      reportsByDomain.set(domain, report);
    }
  }

  const certs = storage.loadManagedCertificates();
  const updatedCertIds = new Set();
  let changed = false;
  const nextCerts = certs.map((cert) => {
    if (
      !cert ||
      cert.issuer_mode !== "local_http01" ||
      !Array.isArray(cert.target_agent_ids) ||
      !cert.target_agent_ids.includes(agent.id)
    ) {
      return cert;
    }

    const report = findManagedCertificateReportForAgent(cert, reportsById, reportsByDomain);
    if (!report) return cert;

    updatedCertIds.add(Number(cert.id));

    let nextCert = updateManagedCertificateAgentReportSnapshot(cert, agent.id, {
      status: report.status,
      last_issue_at: report.last_issue_at,
      last_error: report.last_error,
      material_hash: report.material_hash,
      acme_info: report.acme_info,
      updated_at: report.updated_at || nowIso(),
    });

    if ((cert.target_agent_ids || []).length === 1 && cert.target_agent_ids[0] === agent.id) {
      nextCert = {
        ...nextCert,
        status: report.status || cert.status,
        last_issue_at: report.last_issue_at,
        last_error: report.last_error,
        material_hash: report.material_hash,
        acme_info: normalizeManagedCertificateAcmeInfo(report.acme_info || {}),
      };
    }

    if (JSON.stringify(nextCert) === JSON.stringify(cert)) {
      return cert;
    }

    changed = true;
    return nextCert;
  });

  if (changed) {
    storage.saveManagedCertificates(nextCerts);
  }

  return updatedCertIds;
}

function reconcileLocalHttp01CertificatesForAgent(agent, options = {}) {
  if (!agent || !agent.id) return;
  if (!agentHasCapability(agent, "cert_install") || !agentHasCapability(agent, "local_acme")) {
    return;
  }

  const reportedCertIds = new Set(
    Array.isArray(options.reported_cert_ids) ? options.reported_cert_ids : [],
  );

  const applyRevision = normalizeRevision(agent.last_apply_revision);
  if (applyRevision <= 0) return;
  if (!["success", "error"].includes(String(agent.last_apply_status || "").trim().toLowerCase())) {
    return;
  }

  const certs = storage.loadManagedCertificates();
  const appliedAt = nowIso();
  let changed = false;
  const nextCerts = certs.map((cert) => {
    if (
      !cert ||
      cert.enabled === false ||
      cert.issuer_mode !== "local_http01" ||
      !Array.isArray(cert.target_agent_ids) ||
      !cert.target_agent_ids.includes(agent.id) ||
      reportedCertIds.has(Number(cert.id)) ||
      normalizeRevision(cert.revision) > applyRevision ||
      !hasMatchingHttpsRuleForCertificate(agent.id, cert)
    ) {
      return cert;
    }

    if (agent.last_apply_status === "success") {
      const nextWithReport = updateManagedCertificateAgentReportSnapshot(cert, agent.id, {
        status: "active",
        last_issue_at: appliedAt,
        last_error: "",
        material_hash: cert.material_hash,
        acme_info: cert.acme_info,
        updated_at: appliedAt,
      });
      const nextCert = {
        ...nextWithReport,
        status: "active",
        last_issue_at: appliedAt,
        last_error: "",
      };
      if (JSON.stringify(nextCert) === JSON.stringify(cert)) {
        return cert;
      }
      changed = true;
      return nextCert;
    }

    if (cert.status !== "pending") {
      return cert;
    }

    changed = true;
    return updateManagedCertificateAgentReportSnapshot({
      ...cert,
      status: "error",
      last_error: String(agent.last_apply_message || "agent apply failed"),
    }, agent.id, {
      status: "error",
      last_issue_at: cert.last_issue_at || null,
      last_error: String(agent.last_apply_message || "agent apply failed"),
      material_hash: cert.material_hash,
      acme_info: cert.acme_info,
      updated_at: appliedAt,
    });
  });

  if (changed) {
    storage.saveManagedCertificates(nextCerts);
  }
}

async function issueManagedCertificateById(certId, options = {}) {
  const { bumpRevision = true, applyNow = AUTO_APPLY } = options;
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
    runManagedCertificateHelper(cert.domain, "issue");
    const materialHash = getManagedCertificateMaterialHash(cert.domain);
    const acmeInfo = readManagedCertificateAcmeInfo(cert.domain);
    cert = updateManagedCertificate(certId, (current) => ({
      ...current,
      status: "active",
      last_issue_at: nowIso(),
      last_error: "",
      material_hash: materialHash || String(current.material_hash || ""),
      acme_info: acmeInfo,
      revision: bumpRevision ? storage.getNextGlobalRevision() : normalizeRevision(current.revision),
    }));
  } catch (err) {
    cert = updateManagedCertificate(certId, (current) => ({
      ...current,
      status: "error",
      last_error: String(err.message || err),
      revision: bumpRevision ? storage.getNextGlobalRevision() : normalizeRevision(current.revision),
    }));
    throw new Error(cert.last_error);
  }

  await syncManagedCertificateTargets(cert, { applyNow });
  return cert;
}

async function renewManagedCertificateById(certId, options = {}) {
  const { applyNow = AUTO_APPLY } = options;
  assertManagedCertificateEnabled();

  let cert = getManagedCertificateById(certId);
  if (!cert) throw new Error("certificate not found");
  if (cert.scope !== "domain") {
    throw new Error("only domain certificates can be managed by master");
  }
  if (cert.issuer_mode !== "master_cf_dns") {
    throw new Error("certificate is not configured for master_cf_dns");
  }
  if (!cert.enabled) {
    throw new Error("certificate is disabled");
  }

  const previousHash = String(cert.material_hash || "") || getManagedCertificateMaterialHash(cert.domain);

  try {
    runManagedCertificateHelper(cert.domain, "renew");
  } catch (err) {
    cert = updateManagedCertificate(certId, (current) => ({
      ...current,
      status: "error",
      last_error: String(err.message || err),
    }));
    throw new Error(cert.last_error);
  }

  const nextHash = getManagedCertificateMaterialHash(cert.domain);
  const changed = !!nextHash && nextHash !== previousHash;
  const acmeInfo = readManagedCertificateAcmeInfo(cert.domain);

  cert = updateManagedCertificate(certId, (current) => ({
    ...current,
    status: "active",
    last_issue_at: changed ? nowIso() : current.last_issue_at || null,
    last_error: "",
    material_hash: nextHash || previousHash || String(current.material_hash || ""),
    acme_info: acmeInfo,
    revision: changed ? storage.getNextGlobalRevision() : normalizeRevision(current.revision),
  }));

  if (changed) {
    await syncManagedCertificateTargets(cert, { applyNow });
  }

  return { certificate: cert, changed };
}

async function requestLocalHttp01CertificateById(certId, options = {}) {
  const { agentId = null } = options;
  let cert = getManagedCertificateById(certId);
  if (!cert) throw new Error("certificate not found");
  if (!cert.enabled) throw new Error("certificate is disabled");
  if (cert.issuer_mode !== "local_http01") {
    throw new Error("certificate is not configured for local_http01");
  }

  const requestedAgentIds = agentId
    ? (Array.isArray(cert.target_agent_ids) ? cert.target_agent_ids : []).filter((id) => id === agentId)
    : Array.isArray(cert.target_agent_ids)
      ? cert.target_agent_ids
      : [];

  if (!requestedAgentIds.length) {
    throw new Error("certificate is not assigned to the requested agent");
  }

  if (!agentId && requestedAgentIds.length > 1) {
    throw new Error("local_http01 certificates must be issued from the per-agent endpoint");
  }

  for (const targetAgentId of requestedAgentIds) {
    const agent = getAgentById(targetAgentId);
    if (!agent) throw new Error(`target agent not found: ${targetAgentId}`);
    if (!agentHasCapability(agent, "cert_install")) {
      throw new Error(`target agent does not support certificate install: ${agent.name || targetAgentId}`);
    }
    if (!agentHasCapability(agent, "local_acme")) {
      throw new Error(`target agent does not support local ACME issuance: ${agent.name || targetAgentId}`);
    }
    if (!hasMatchingHttpsRuleForCertificate(targetAgentId, cert)) {
      throw new Error(
        `no enabled HTTPS HTTP rule found for ${cert.domain} on agent ${agent.name || targetAgentId}`,
      );
    }
  }

  cert = updateManagedCertificate(certId, (current) => ({
    ...requestedAgentIds.reduce(
      (next, targetAgentId) =>
        updateManagedCertificateAgentReportSnapshot(next, targetAgentId, {
          status: "pending",
          last_issue_at: getManagedCertificateAgentReport(current, targetAgentId)?.last_issue_at || null,
          last_error: "",
          material_hash: "",
          acme_info: {},
          updated_at: nowIso(),
        }),
      {
        ...current,
        status: "pending",
        last_error: "",
        revision: storage.getNextGlobalRevision(),
      },
    ),
  }));

  for (const targetAgentId of requestedAgentIds) {
    try {
      await applyAgent(targetAgentId);
    } catch (err) {
      cert = updateManagedCertificate(certId, (current) => ({
        ...updateManagedCertificateAgentReportSnapshot(current, targetAgentId, {
          status: "error",
          last_issue_at: getManagedCertificateAgentReport(current, targetAgentId)?.last_issue_at || null,
          last_error: String(err.message || err),
          material_hash: getManagedCertificateAgentReport(current, targetAgentId)?.material_hash || "",
          acme_info: getManagedCertificateAgentReport(current, targetAgentId)?.acme_info || {},
          updated_at: nowIso(),
        }),
        status: "error",
        last_error: String(err.message || err),
      }));
      throw err;
    }
  }

  if (requestedAgentIds.includes(LOCAL_AGENT_ID)) {
    cert = updateManagedCertificate(certId, (current) => ({
      ...updateManagedCertificateAgentReportSnapshot(current, LOCAL_AGENT_ID, {
        status: "active",
        last_issue_at: nowIso(),
        last_error: "",
        material_hash: String(current.material_hash || ""),
        acme_info: current.acme_info || {},
        updated_at: nowIso(),
      }),
      status: "active",
      last_issue_at: nowIso(),
      last_error: "",
    }));
  } else {
    cert = getManagedCertificateById(certId);
  }

  return cert;
}

async function syncAgentRules(agentId) {
  const agent = getAgentById(agentId);
  if (!agent) throw new Error("agent not found");
  const rules = storage.loadRulesForAgent(agentId);
  if (agentId === LOCAL_AGENT_ID) {
    const desiredRevision = getDesiredRevisionForSync(agent, agentId, rules);
    if (desiredRevision > agent.desired_revision) {
      storage.saveLocalAgentState({
        ...storage.loadLocalAgentState(),
        desired_revision: desiredRevision,
      });
    }
    return { ok: true, rules, desired_revision: desiredRevision };
  }
  const agents = storage.loadRegisteredAgents();
  const index = agents.findIndex((item) => item.id === agentId);
  if (index === -1) throw new Error("agent not found");
  agents[index] = ensureAgentState(agents[index]);
  agents[index].desired_revision = getDesiredRevisionForSync(agents[index], agentId, rules, {
    force: true,
  });
  agents[index].updated_at = nowIso();
  storage.saveRegisteredAgents(agents);
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
    const rules = storage.loadRulesForAgent(agentId);
    const desiredRevision = getDesiredRevisionForSync(agent, agentId, rules, { force: true });
    const nextState = {
      ...storage.loadLocalAgentState(),
      desired_revision: desiredRevision,
      last_apply_revision: desiredRevision,
    };
    try {
      applyNginxConfig();
      nextState.current_revision = desiredRevision;
      nextState.last_apply_status = "success";
      nextState.last_apply_message = "";
      storage.saveLocalAgentState(nextState);
      return { ok: true, message: "applied", desired_revision: desiredRevision };
    } catch (err) {
      nextState.current_revision = agent.current_revision;
      nextState.last_apply_status = "error";
      nextState.last_apply_message = String(err.message || err);
      storage.saveLocalAgentState(nextState);
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

function listAutoRenewManagedCertificates() {
  return storage.loadManagedCertificates().filter(
    (cert) =>
      cert &&
      cert.enabled !== false &&
      cert.scope === "domain" &&
      cert.issuer_mode === "master_cf_dns",
  );
}

function cleanupManagedCertificateArtifacts(domain) {
  try {
    runManagedCertificateHelper(domain, "remove");
  } catch (err) {
    console.error(
      `[cert] failed to remove ACME artifacts for ${domain}:`,
      String(err.message || err),
    );
  }
  removePath(getCertStoreDir(domain));
}

async function runManagedCertificateAutoRenewCycle() {
  if (ROLE !== "master" || !MANAGED_CERTS_ENABLED) return;
  if (!Number.isFinite(MANAGED_CERT_RENEW_INTERVAL_MS) || MANAGED_CERT_RENEW_INTERVAL_MS < 1) {
    return;
  }
  if (isManagedCertificateRenewRunning) return;

  isManagedCertificateRenewRunning = true;
  try {
    const certs = listAutoRenewManagedCertificates();
    for (const cert of certs) {
      try {
        const result = await renewManagedCertificateById(cert.id, { applyNow: AUTO_APPLY });
        if (result.changed) {
          console.log(
            `[cert] renewed master managed certificate for ${cert.domain} and synced assigned agents`,
          );
        }
      } catch (err) {
        console.error(
          `[cert] auto renew failed for ${cert.domain}:`,
          String(err.message || err),
        );
      }
    }
  } finally {
    isManagedCertificateRenewRunning = false;
  }
}

function startManagedCertificateAutoRenewLoop() {
  if (ROLE !== "master" || !MANAGED_CERTS_ENABLED) return;
  if (!Number.isFinite(MANAGED_CERT_RENEW_INTERVAL_MS) || MANAGED_CERT_RENEW_INTERVAL_MS < 1) {
    return;
  }

  setTimeout(() => {
    runManagedCertificateAutoRenewCycle().catch((err) => {
      console.error("[cert] initial auto renew cycle failed:", String(err.message || err));
    });
  }, 10000);

  setInterval(() => {
    runManagedCertificateAutoRenewCycle().catch((err) => {
      console.error("[cert] managed certificate auto renew cycle failed:", String(err.message || err));
    });
  }, MANAGED_CERT_RENEW_INTERVAL_MS);
}

function findRegisteredAgentByToken(token) {
  if (!token) return null;
  return storage.loadRegisteredAgents().find((agent) => agent.agent_token === token) || null;
}

function resolveVersionPackageForAgent(agent) {
  const desiredVersion = String(agent?.desired_version || "").trim();
  const platform = String(agent?.platform || "").trim();
  if (!desiredVersion || !platform) {
    return null;
  }
  const policies = storage
    .loadVersionPolicies()
    .slice()
    .sort((left, right) => String(left?.id || "").localeCompare(String(right?.id || "")));
  // Deterministic minimal rule for Go agents:
  // scan matching desired_version policies in stable sorted order and return
  // the first package across all of them matching the agent platform.
  for (const policy of policies) {
    if (String(policy?.desired_version || "").trim() !== desiredVersion) {
      continue;
    }
    const match = Array.isArray(policy.packages)
      ? policy.packages.find(
          (pkg) => String(pkg?.platform || "").trim() === platform,
        ) || null
      : null;
    if (match) {
      return match;
    }
  }
  return null;
}

function getAgentHeartbeatResponse(agent) {
  const rules = loadNormalizedRulesForAgent(agent.id);
  const l4Rules = storage.loadL4RulesForAgent(agent.id);
  const relayListeners = loadRelayListenersForSync(agent.id, rules, l4Rules);
  const certificates = buildManagedCertificateBundleForAgent(agent.id);
  const certificatePolicies = buildManagedCertificatePolicyForAgent(agent.id);
  const versionPackage = resolveVersionPackageForAgent(agent);
  const versionPackageMeta = versionPackage ? { ...versionPackage } : null;
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
      relay_listeners: relayListeners,
      desired_version: agent.desired_version || null,
      version_package: versionPackage ? versionPackage.url : null,
      version_package_meta: versionPackageMeta,
      version_sha256: versionPackageMeta ? versionPackageMeta.sha256 : null,
      certificates: hasUpdate ? certificates : undefined,
      certificate_policies: hasUpdate ? certificatePolicies : undefined,
    },
  };
}

function loadRelayListenersForSync(agentId, rules = [], l4Rules = []) {
  const allRelayListeners = listAllRelayListenersById();
  const localRelayListeners = storage.loadRelayListenersForAgent(agentId);
  const included = new Set();
  const ordered = [];

  const pushListener = (listener) => {
    const listenerId = Number(listener?.id);
    if (!Number.isInteger(listenerId) || listenerId <= 0 || included.has(listenerId)) {
      return;
    }
    included.add(listenerId);
    ordered.push(listener);
  };

  for (const listener of Array.isArray(localRelayListeners) ? localRelayListeners : []) {
    pushListener(listener);
  }

  const addReferencedListeners = (relayChain) => {
    for (const listenerId of Array.isArray(relayChain) ? relayChain : []) {
      pushListener(allRelayListeners.get(Number(listenerId)));
    }
  };

  for (const rule of Array.isArray(rules) ? rules : []) {
    addReferencedListeners(rule?.relay_chain);
  }
  for (const rule of Array.isArray(l4Rules) ? l4Rules : []) {
    addReferencedListeners(rule?.relay_chain);
  }

  return ordered;
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
  const rules = storage.loadRulesForAgent(agentId);
  return Array.isArray(rules) ? rules : [];
}

function loadNormalizedRulesForAgent(agentId) {
  return loadOrInitRules(agentId).map((rule, index) =>
    normalizeStoredRule(rule, index + 1),
  );
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
    sendJson(res, 200, { ok: true, rules: loadNormalizedRulesForAgent(LOCAL_AGENT_ID) });
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
      storage.saveRulesForAgent(LOCAL_AGENT_ID, rules);
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
    sendJson(res, 200, { ok: true, rules: loadNormalizedRulesForAgent(LOCAL_AGENT_ID) });
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
      const nextRules = [...rules, newRule];
      try {
        await ensureManagedCertificateForRule(LOCAL_AGENT_ID, newRule, { applyNow: false });
        await cleanupUnusedManagedCertificatesForAgent(LOCAL_AGENT_ID, nextRules, { applyNow: false });
      } catch (err) {
        sendJson(
          res,
          400,
          errorPayload(
            "rule validation failed during unified certificate preparation",
            String(err.message || err),
          ),
        );
        return;
      }
      storage.saveRulesForAgent(LOCAL_AGENT_ID, nextRules);
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
      const nextRule = normalizeRulePayload(body, rules[index], ruleId);
      nextRule.revision = getNextPendingRevision(agent);
      const nextRules = rules.slice();
      nextRules[index] = nextRule;
      try {
        await ensureManagedCertificateForRule(LOCAL_AGENT_ID, nextRule, { applyNow: false });
        await cleanupUnusedManagedCertificatesForAgent(LOCAL_AGENT_ID, nextRules, { applyNow: false });
      } catch (err) {
        sendJson(
          res,
          400,
          errorPayload(
            "rule validation failed during unified certificate preparation",
            String(err.message || err),
          ),
        );
        return;
      }
      storage.saveRulesForAgent(LOCAL_AGENT_ID, nextRules);
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
      sendJson(res, 200, { ok: true, rule: nextRule });
    } catch (err) {
      sendJson(res, 400, errorPayload(String(err.message || err)));
    }
    return;
  }

  if (req.method === "DELETE" && /^\/api\/rules\/\d+$/.test(urlPath)) {
    try {
      const ruleId = Number(urlPath.split("/").pop());
      const rules = loadOrInitRules(LOCAL_AGENT_ID);
      const index = rules.findIndex((rule) => Number(rule.id) === ruleId);
      if (index === -1) {
        sendJson(res, 404, errorPayload("rule id not found"));
        return;
      }
      const deleted = rules[index];
      const nextRules = rules.filter((_, ruleIndex) => ruleIndex !== index);
      try {
        await cleanupUnusedManagedCertificatesForAgent(LOCAL_AGENT_ID, nextRules, {
          applyNow: false,
        });
      } catch (err) {
        sendJson(
          res,
          400,
          errorPayload(
            "rule validation failed during unified certificate cleanup",
            String(err.message || err),
          ),
        );
        return;
      }
      storage.saveRulesForAgent(LOCAL_AGENT_ID, nextRules);
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
    } catch (err) {
      sendJson(res, 400, errorPayload(String(err.message || err)));
    }
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

  if ((req.method === "GET" || req.method === "HEAD") && urlPath === "/api/health") {
    sendJson(res, 200, { ok: true, role: ROLE });
    return;
  }

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
    sendBody(res, 200, asset.body, asset.contentType, {
      "Cache-Control": "public, max-age=300",
    });
    return;
  }

  if (req.method === "GET" && urlPath === "/api/auth/verify") {
    const authorized = isPanelAuthorized(req);
    sendJson(res, authorized ? 200 : 401, { ok: authorized, role: ROLE });
    return;
  }

  if (req.method === "GET" && urlPath === "/api/info") {
    const info = {
      ok: true,
      role: ROLE,
      local_apply_runtime: "go-agent",
      local_agent_enabled: LOCAL_AGENT_ENABLED,
      default_agent_id: getDefaultAgentId(),
      managed_certificates_enabled: MANAGED_CERTS_ENABLED,
      proxy_headers_globally_disabled: isProxyHeadersGloballyDisabled(),
      cf_token_configured: !!CF_TOKEN,
      acme_dns_provider: ACME_DNS_PROVIDER || null,
    };
    if (isPanelAuthorized(req)) {
      info.master_register_token = MASTER_REGISTER_TOKEN || null;
    }
    sendJson(res, 200, info);
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

      const agents = storage.loadRegisteredAgents();
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

      storage.saveRegisteredAgents(agents);
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

      const agents = storage.loadRegisteredAgents();
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
      agent.version = String(
        body.version !== undefined ? body.version : agent.version || "",
      ).trim();
      agent.platform = String(
        body.platform !== undefined ? body.platform : agent.platform || "",
      ).trim();
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
      storage.saveRegisteredAgents(agents);
      const managedCertificateReports = Array.isArray(body.managed_certificate_reports)
        ? body.managed_certificate_reports
            .map((report) => normalizeAgentManagedCertificateReportPayload(report))
            .filter((report) => report.id || report.domain)
        : [];
      const reportedCertIds = applyAgentManagedCertificateReports(agent, managedCertificateReports);
      reconcileLocalHttp01CertificatesForAgent(agent, {
        reported_cert_ids: [...reportedCertIds],
      });
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
      sendJson(res, 400, errorPayload("本地 Agent 不允许修改"));
      return;
    }

    try {
      const body = await parseJsonBody(req);
      const agents = storage.loadRegisteredAgents();
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

      storage.saveRegisteredAgents(agents);
      sendJson(res, 200, { ok: true, agent: sanitizeAgent(agents[index]) });
    } catch (err) {
      sendJson(res, 400, errorPayload(String(err.message || err)));
    }
    return;
  }

  if (req.method === "PATCH" && /^\/api\/agents\/[^/]+$/.test(urlPath)) {
    const agentId = extractAgentId(urlPath);
    if (agentId === LOCAL_AGENT_ID) {
      sendJson(res, 400, errorPayload("本地 Agent 不允许修改"));
      return;
    }
    try {
      const body = await parseJsonBody(req);
      const agents = storage.loadRegisteredAgents();
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
      storage.saveRegisteredAgents(agents);
      sendJson(res, 200, { ok: true, agent: sanitizeAgent(agents[index]) });
    } catch (err) {
      sendJson(res, 400, errorPayload(String(err.message || err)));
    }
    return;
  }

  if (req.method === "DELETE" && /^\/api\/agents\/[^/]+$/.test(urlPath)) {
    const agentId = extractAgentId(urlPath);
    if (agentId === LOCAL_AGENT_ID) {
      sendJson(res, 400, errorPayload("本地 Agent 不允许删除"));
      return;
    }
    const agents = storage.loadRegisteredAgents();
    const index = agents.findIndex((agent) => agent.id === agentId);
    if (index === -1) {
      sendJson(res, 404, errorPayload("agent not found"));
      return;
    }
    const listeners = storage.loadRelayListenersForAgent(agentId);
    for (const listener of listeners) {
      const listenerId = Number(listener?.id);
      if (!Number.isInteger(listenerId) || listenerId <= 0) {
        continue;
      }
      const relayReference = findRelayListenerReferenceAcrossAgents(listenerId, {
        excludeAgentIds: [agentId],
      });
      if (relayReference) {
        const ruleType = relayReference.protocol === "http" ? "HTTP" : "L4";
        sendJson(
          res,
          400,
          errorPayload(
            `cannot delete agent ${agentId}: relay listener ${listenerId} is referenced by ${ruleType} rule #${relayReference.ruleId} on agent ${relayReference.agentId}`,
          ),
        );
        return;
      }
    }
    const deleted = agents.splice(index, 1)[0];
    storage.saveRegisteredAgents(agents);
    storage.deleteRulesForAgent(agentId);
    storage.deleteL4RulesForAgent(agentId);
    storage.deleteRelayListenersForAgent(agentId);
    removePath(getManagedCertBundleFileForAgent(agentId));
    removePath(getManagedCertPolicyFileForAgent(agentId));
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
    sendJson(res, 200, { ok: true, rules: loadNormalizedRulesForAgent(agentId) });
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
      const nextRules = [...rules, newRule];
      try {
        await ensureManagedCertificateForRule(agentId, newRule, { applyNow: false });
        await cleanupUnusedManagedCertificatesForAgent(agentId, nextRules, { applyNow: false });
      } catch (err) {
        sendJson(
          res,
          400,
          errorPayload(
            "rule validation failed during unified certificate preparation",
            String(err.message || err),
          ),
        );
        return;
      }
      storage.saveRulesForAgent(agentId, nextRules);

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
    sendJson(res, 200, { ok: true, rules: storage.loadL4RulesForAgent(agentId) });
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
      const rules = storage.loadL4RulesForAgent(agentId);
      const maxId = rules.reduce((max, rule) => Math.max(max, Number(rule.id) || 0), 0);
      const newRule = normalizeL4RulePayload(body, {}, maxId + 1);
      ensureUniqueL4Listen(rules, newRule);
      newRule.revision = getNextPendingRevision(agent);
      rules.push(newRule);
      storage.saveL4RulesForAgent(agentId, rules);

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
      const nextRule = normalizeRulePayload(body, rules[index], ruleId);
      nextRule.revision = getNextPendingRevision(agent);
      const nextRules = rules.slice();
      nextRules[index] = nextRule;
      try {
        await ensureManagedCertificateForRule(agentId, nextRule, { applyNow: false });
        await cleanupUnusedManagedCertificatesForAgent(agentId, nextRules, { applyNow: false });
      } catch (err) {
        sendJson(
          res,
          400,
          errorPayload(
            "rule validation failed during unified certificate preparation",
            String(err.message || err),
          ),
        );
        return;
      }
      storage.saveRulesForAgent(agentId, nextRules);

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

      sendJson(res, 200, { ok: true, rule: nextRule });
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
      const deleted = rules[index];
      const nextRules = rules.filter((_, ruleIndex) => ruleIndex !== index);
      try {
        await cleanupUnusedManagedCertificatesForAgent(agentId, nextRules, { applyNow: false });
      } catch (err) {
        sendJson(
          res,
          400,
          errorPayload(
            "rule validation failed during unified certificate cleanup",
            String(err.message || err),
          ),
        );
        return;
      }
      storage.saveRulesForAgent(agentId, nextRules);

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
      const rules = storage.loadL4RulesForAgent(agentId);
      const index = rules.findIndex((rule) => Number(rule.id) === ruleId);
      if (index === -1) {
        sendJson(res, 404, errorPayload("rule id not found"));
        return;
      }
      const nextRule = normalizeL4RulePayload(body, rules[index], ruleId);
      ensureUniqueL4Listen(rules, nextRule, ruleId);
      nextRule.revision = getNextPendingRevision(agent);
      rules[index] = nextRule;
      storage.saveL4RulesForAgent(agentId, rules);

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
      const rules = storage.loadL4RulesForAgent(agentId);
      const index = rules.findIndex((rule) => Number(rule.id) === ruleId);
      if (index === -1) {
        sendJson(res, 404, errorPayload("rule id not found"));
        return;
      }
      const deleted = rules.splice(index, 1)[0];
      storage.saveL4RulesForAgent(agentId, rules);

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

  if (req.method === "GET" && /^\/api\/agents\/[^/]+\/relay-listeners$/.test(urlPath)) {
    const agentId = extractAgentId(urlPath);
    const agent = getAgentById(agentId);
    if (!agent) {
      sendJson(res, 404, errorPayload("agent not found"));
      return;
    }
    sendJson(res, 200, { ok: true, listeners: storage.loadRelayListenersForAgent(agentId) });
    return;
  }

  if (req.method === "POST" && /^\/api\/agents\/[^/]+\/relay-listeners$/.test(urlPath)) {
    try {
      const agentId = extractAgentId(urlPath);
      const agent = getAgentById(agentId);
      if (!agent) {
        sendJson(res, 404, errorPayload("agent not found"));
        return;
      }
      const body = await parseJsonBody(req);
      const listeners = storage.loadRelayListenersForAgent(agentId);
      const nextListener = normalizeRelayListenerPayload({
        ...(body || {}),
        id: getNextRelayListenerId(),
        agent_id: agentId,
        revision: getNextPendingRevision(agent),
      });
      const nextListeners = [...listeners, nextListener];
      storage.saveRelayListenersForAgent(agentId, nextListeners);

      if (AUTO_APPLY) {
        try {
          await applyAgent(agentId);
        } catch (err) {
          sendJson(
            res,
            400,
            errorPayload(
              "relay listener saved but failed to sync/apply agent config",
              String(err.message || err),
            ),
          );
          return;
        }
      }

      sendJson(res, 201, { ok: true, listener: nextListener });
    } catch (err) {
      sendJson(res, 400, errorPayload(String(err.message || err)));
    }
    return;
  }

  if (req.method === "PUT" && /^\/api\/agents\/[^/]+\/relay-listeners\/\d+$/.test(urlPath)) {
    try {
      const agentId = extractAgentId(urlPath);
      const agent = getAgentById(agentId);
      if (!agent) {
        sendJson(res, 404, errorPayload("agent not found"));
        return;
      }
      const listenerId = extractTrailingId(urlPath);
      const body = await parseJsonBody(req);
      const listeners = storage.loadRelayListenersForAgent(agentId);
      const index = listeners.findIndex((listener) => Number(listener.id) === listenerId);
      if (index === -1) {
        sendJson(res, 404, errorPayload("relay listener not found"));
        return;
      }
      const nextListener = normalizeRelayListenerPayload({
        ...listeners[index],
        ...(body || {}),
        id: listenerId,
        agent_id: agentId,
        revision: getNextPendingRevision(agent),
      });
      if (nextListener.enabled === false) {
        const relayReference = findRelayListenerReferenceAcrossAgents(listenerId);
        if (relayReference) {
          const ruleType = relayReference.protocol === "http" ? "HTTP" : "L4";
          sendJson(
            res,
            400,
            errorPayload(
              `relay listener ${listenerId} is referenced by ${ruleType} rule #${relayReference.ruleId} on agent ${relayReference.agentId}; disable is not allowed`,
            ),
          );
          return;
        }
      }
      const nextListeners = listeners.slice();
      nextListeners[index] = nextListener;
      storage.saveRelayListenersForAgent(agentId, nextListeners);

      if (AUTO_APPLY) {
        try {
          await applyAgent(agentId);
        } catch (err) {
          sendJson(
            res,
            400,
            errorPayload(
              "relay listener updated but failed to sync/apply agent config",
              String(err.message || err),
            ),
          );
          return;
        }
      }

      sendJson(res, 200, { ok: true, listener: nextListener });
    } catch (err) {
      sendJson(res, 400, errorPayload(String(err.message || err)));
    }
    return;
  }

  if (req.method === "DELETE" && /^\/api\/agents\/[^/]+\/relay-listeners\/\d+$/.test(urlPath)) {
    try {
      const agentId = extractAgentId(urlPath);
      const agent = getAgentById(agentId);
      if (!agent) {
        sendJson(res, 404, errorPayload("agent not found"));
        return;
      }
      const listenerId = extractTrailingId(urlPath);
      const listeners = storage.loadRelayListenersForAgent(agentId);
      const index = listeners.findIndex((listener) => Number(listener.id) === listenerId);
      if (index === -1) {
        sendJson(res, 404, errorPayload("relay listener not found"));
        return;
      }

      const relayReference = findRelayListenerReferenceAcrossAgents(listenerId);
      if (relayReference) {
        const ruleType = relayReference.protocol === "http" ? "HTTP" : "L4";
        sendJson(
          res,
          400,
          errorPayload(
            `relay listener ${listenerId} is referenced by ${ruleType} rule #${relayReference.ruleId} on agent ${relayReference.agentId}`,
          ),
        );
        return;
      }

      const deleted = listeners[index];
      const nextListeners = listeners.filter((_, itemIndex) => itemIndex !== index);
      storage.saveRelayListenersForAgent(agentId, nextListeners);

      if (AUTO_APPLY) {
        try {
          await applyAgent(agentId);
        } catch (err) {
          sendJson(
            res,
            400,
            errorPayload(
              "relay listener deleted but failed to sync/apply agent config",
              String(err.message || err),
            ),
          );
          return;
        }
      }

      sendJson(res, 200, { ok: true, listener: deleted });
    } catch (err) {
      sendJson(res, 400, errorPayload(String(err.message || err)));
    }
    return;
  }

  if (req.method === "GET" && urlPath === "/api/version-policies") {
    sendJson(res, 200, { ok: true, policies: storage.loadVersionPolicies() });
    return;
  }

  if (req.method === "POST" && urlPath === "/api/version-policies") {
    try {
      const body = await parseJsonBody(req);
      const policies = storage.loadVersionPolicies();
      const candidateId = String(
        body?.id !== undefined
          ? body.id
          : body?.channel !== undefined
            ? body.channel
            : `policy-${Date.now()}`,
      ).trim();
      const policy = normalizeVersionPolicyPayload({
        ...(body || {}),
        id: candidateId || `policy-${Date.now()}`,
      });
      if (policies.some((item) => String(item.id) === String(policy.id))) {
        sendJson(res, 400, errorPayload(`version policy id already exists: ${policy.id}`));
        return;
      }
      const nextPolicies = [...policies, policy];
      storage.saveVersionPolicies(nextPolicies);
      sendJson(res, 201, { ok: true, policy });
    } catch (err) {
      sendJson(res, 400, errorPayload(String(err.message || err)));
    }
    return;
  }

  if (req.method === "PUT" && /^\/api\/version-policies\/[^/]+$/.test(urlPath)) {
    try {
      const policyIdMatch = urlPath.match(/^\/api\/version-policies\/([^/]+)$/);
      const policyId = policyIdMatch ? decodeURIComponent(policyIdMatch[1]) : null;
      const body = await parseJsonBody(req);
      const policies = storage.loadVersionPolicies();
      const index = policies.findIndex((item) => String(item.id) === String(policyId));
      if (index === -1) {
        sendJson(res, 404, errorPayload("version policy not found"));
        return;
      }
      const nextPolicy = normalizeVersionPolicyPayload({
        ...policies[index],
        ...(body || {}),
        id: policies[index].id,
      });
      const nextPolicies = policies.slice();
      nextPolicies[index] = nextPolicy;
      storage.saveVersionPolicies(nextPolicies);
      sendJson(res, 200, { ok: true, policy: nextPolicy });
    } catch (err) {
      sendJson(res, 400, errorPayload(String(err.message || err)));
    }
    return;
  }

  if (req.method === "DELETE" && /^\/api\/version-policies\/[^/]+$/.test(urlPath)) {
    try {
      const policyIdMatch = urlPath.match(/^\/api\/version-policies\/([^/]+)$/);
      const policyId = policyIdMatch ? decodeURIComponent(policyIdMatch[1]) : null;
      const policies = storage.loadVersionPolicies();
      const index = policies.findIndex((item) => String(item.id) === String(policyId));
      if (index === -1) {
        sendJson(res, 404, errorPayload("version policy not found"));
        return;
      }
      const deleted = policies[index];
      const nextPolicies = policies.filter((_, itemIndex) => itemIndex !== index);
      storage.saveVersionPolicies(nextPolicies);
      sendJson(res, 200, { ok: true, policy: deleted });
    } catch (err) {
      sendJson(res, 400, errorPayload(String(err.message || err)));
    }
    return;
  }

  if (req.method === "GET" && /^\/api\/agents\/[^/]+\/certificates$/.test(urlPath)) {
    const agentId = extractAgentId(urlPath);
    const agent = getAgentById(agentId);
    if (!agent) {
      sendJson(res, 404, errorPayload("agent not found"));
      return;
    }
    sendJson(res, 200, { ok: true, certificates: getManagedCertificatesForAgent(agentId) });
    return;
  }

  if (req.method === "POST" && /^\/api\/agents\/[^/]+\/certificates$/.test(urlPath)) {
    try {
      const agentId = extractAgentId(urlPath);
      const agent = getAgentById(agentId);
      if (!agent) {
        sendJson(res, 404, errorPayload("agent not found"));
        return;
      }
      const body = await parseJsonBody(req);
      const certs = storage.loadManagedCertificates();
      const maxId = certs.reduce((max, cert) => Math.max(max, Number(cert.id) || 0), 0);
      const nextCert = normalizeManagedCertificatePayload(
        {
          ...body,
          target_agent_ids:
            body.target_agent_ids !== undefined ? body.target_agent_ids : [agentId],
        },
        {},
        maxId + 1,
      );
      validateManagedCertificateTargets(nextCert);
      const preparedCert = prepareManagedCertificateForSave(null, nextCert);
      if (preparedCert.scope === "domain" && preparedCert.issuer_mode === "master_cf_dns") {
        assertManagedCertificateEnabled();
      }
      certs.push({ ...preparedCert, revision: storage.getNextGlobalRevision() });
      storage.saveManagedCertificates(certs);

      let savedCert = getManagedCertificateById(preparedCert.id);
      if (savedCert.enabled && savedCert.scope === "domain" && savedCert.issuer_mode === "master_cf_dns") {
        savedCert = await issueManagedCertificateById(savedCert.id, { bumpRevision: false });
      } else {
        await syncManagedCertificateAgentIds(savedCert.target_agent_ids || []);
      }

      sendJson(res, 201, { ok: true, certificate: savedCert });
    } catch (err) {
      sendJson(res, 400, errorPayload(String(err.message || err)));
    }
    return;
  }

  if (req.method === "PUT" && /^\/api\/agents\/[^/]+\/certificates\/\d+$/.test(urlPath)) {
    try {
      const agentId = extractAgentId(urlPath);
      const agent = getAgentById(agentId);
      if (!agent) {
        sendJson(res, 404, errorPayload("agent not found"));
        return;
      }
      const certId = extractTrailingId(urlPath);
      const body = await parseJsonBody(req);
      const certs = storage.loadManagedCertificates();
      const index = certs.findIndex(
        (cert) =>
          Number(cert.id) === certId &&
          Array.isArray(cert.target_agent_ids) &&
          cert.target_agent_ids.includes(agentId),
      );
      if (index === -1) {
        sendJson(res, 404, errorPayload("certificate not found"));
        return;
      }
      const previousCert = { ...certs[index] };
      const nextCert = normalizeManagedCertificatePayload(
        {
          ...body,
          target_agent_ids:
            body.target_agent_ids !== undefined ? body.target_agent_ids : previousCert.target_agent_ids,
        },
        certs[index],
        certId,
      );
      validateManagedCertificateTargets(nextCert);
      const preparedCert = prepareManagedCertificateForSave(previousCert, nextCert);
      if (preparedCert.scope === "domain" && preparedCert.issuer_mode === "master_cf_dns") {
        assertManagedCertificateEnabled();
      }
      preparedCert.revision = storage.getNextGlobalRevision();
      certs[index] = preparedCert;
      storage.saveManagedCertificates(certs);

      let savedCert = preparedCert;
      const affectedAgentIds = getManagedCertificateAffectedAgentIds(previousCert, preparedCert);
      const removedAgentIds = getManagedCertificateRemovedAgentIds(previousCert, preparedCert);
      if (
        preparedCert.enabled &&
        preparedCert.scope === "domain" &&
        preparedCert.issuer_mode === "master_cf_dns"
      ) {
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

  if (req.method === "DELETE" && /^\/api\/agents\/[^/]+\/certificates\/\d+$/.test(urlPath)) {
    try {
      const agentId = extractAgentId(urlPath);
      const agent = getAgentById(agentId);
      if (!agent) {
        sendJson(res, 404, errorPayload("agent not found"));
        return;
      }
      const certId = extractTrailingId(urlPath);
      const certs = storage.loadManagedCertificates();
      const index = certs.findIndex(
        (cert) =>
          Number(cert.id) === certId &&
          Array.isArray(cert.target_agent_ids) &&
          cert.target_agent_ids.includes(agentId),
      );
      if (index === -1) {
        sendJson(res, 404, errorPayload("certificate not found"));
        return;
      }

      const existing = certs[index];
      const remainingTargets = (existing.target_agent_ids || []).filter((id) => id !== agentId);
      if (remainingTargets.length > 0) {
        const normalizedCert = normalizeManagedCertificatePayload(
          { ...existing, target_agent_ids: remainingTargets },
          existing,
          certId,
        );
        const nextCert = prepareManagedCertificateForSave(existing, normalizedCert);
        nextCert.revision = storage.getNextGlobalRevision();
        certs[index] = nextCert;
        storage.saveManagedCertificates(certs);
        await syncManagedCertificateAgentIds(
          getManagedCertificateAffectedAgentIds(existing, nextCert),
        );
        sendJson(res, 200, { ok: true, certificate: { ...existing, target_agent_ids: [agentId] } });
        return;
      }

      const deleted = certs.splice(index, 1)[0];
      storage.saveManagedCertificates(certs);
      cleanupManagedCertificateArtifacts(deleted.domain);
      for (const targetAgentId of deleted.target_agent_ids || []) {
        if (targetAgentId === LOCAL_AGENT_ID) {
          persistManagedCertificateBundleForAgent(targetAgentId);
          persistManagedCertificatePolicyForAgent(targetAgentId);
        }
        if (AUTO_APPLY) {
          await applyAgent(targetAgentId);
        }
      }
      sendJson(res, 200, { ok: true, certificate: deleted });
    } catch (err) {
      sendJson(res, 400, errorPayload(String(err.message || err)));
    }
    return;
  }

  if (req.method === "POST" && /^\/api\/agents\/[^/]+\/certificates\/\d+\/issue$/.test(urlPath)) {
    try {
      const agentId = extractAgentId(urlPath);
      const agent = getAgentById(agentId);
      if (!agent) {
        sendJson(res, 404, errorPayload("agent not found"));
        return;
      }
      const certId = extractTrailingId(urlPath.replace(/\/issue$/, ""));
      const cert = getManagedCertificatesForAgent(agentId).find((item) => Number(item.id) === certId);
      if (!cert) {
        sendJson(res, 404, errorPayload("certificate not found"));
        return;
      }
      const issued =
        cert.issuer_mode === "local_http01"
          ? await requestLocalHttp01CertificateById(certId, { agentId })
          : await issueManagedCertificateById(certId, { bumpRevision: true });
      sendJson(res, 200, { ok: true, certificate: issued });
    } catch (err) {
      sendJson(res, 400, errorPayload(String(err.message || err)));
    }
    return;
  }

  if (req.method === "GET" && urlPath === "/api/certificates") {
    sendJson(res, 200, { ok: true, certificates: storage.loadManagedCertificates() });
    return;
  }

  if (req.method === "POST" && urlPath === "/api/certificates") {
    try {
      const body = await parseJsonBody(req);
      const certs = storage.loadManagedCertificates();
      const maxId = certs.reduce((max, cert) => Math.max(max, Number(cert.id) || 0), 0);
      const nextCert = normalizeManagedCertificatePayload(body, {}, maxId + 1);
      validateManagedCertificateTargets(nextCert);
      const preparedCert = prepareManagedCertificateForSave(null, nextCert);
      if (preparedCert.scope === "domain" && preparedCert.issuer_mode === "master_cf_dns") {
        assertManagedCertificateEnabled();
      }
      preparedCert.revision = storage.getNextGlobalRevision();
      certs.push(preparedCert);
      storage.saveManagedCertificates(certs);

      let savedCert = preparedCert;
      if (
        preparedCert.enabled &&
        preparedCert.scope === "domain" &&
        preparedCert.issuer_mode === "master_cf_dns"
      ) {
        savedCert = await issueManagedCertificateById(preparedCert.id, { bumpRevision: false });
      } else {
        await syncManagedCertificateAgentIds(preparedCert.target_agent_ids || []);
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
      const certs = storage.loadManagedCertificates();
      const index = certs.findIndex((cert) => Number(cert.id) === certId);
      if (index === -1) {
        sendJson(res, 404, errorPayload("certificate not found"));
        return;
      }
      const previousCert = { ...certs[index] };
      const nextCert = normalizeManagedCertificatePayload(body, certs[index], certId);
      validateManagedCertificateTargets(nextCert);
      const preparedCert = prepareManagedCertificateForSave(previousCert, nextCert);
      if (preparedCert.scope === "domain" && preparedCert.issuer_mode === "master_cf_dns") {
        assertManagedCertificateEnabled();
      }
      preparedCert.revision = storage.getNextGlobalRevision();
      certs[index] = preparedCert;
      storage.saveManagedCertificates(certs);

      let savedCert = preparedCert;
      const affectedAgentIds = getManagedCertificateAffectedAgentIds(previousCert, preparedCert);
      const removedAgentIds = getManagedCertificateRemovedAgentIds(previousCert, preparedCert);
      if (
        preparedCert.enabled &&
        preparedCert.scope === "domain" &&
        preparedCert.issuer_mode === "master_cf_dns"
      ) {
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
      const certs = storage.loadManagedCertificates();
      const index = certs.findIndex((cert) => Number(cert.id) === certId);
      if (index === -1) {
        sendJson(res, 404, errorPayload("certificate not found"));
        return;
      }
      const deleted = certs.splice(index, 1)[0];
      storage.saveManagedCertificates(certs);
      cleanupManagedCertificateArtifacts(deleted.domain);
      for (const agentId of deleted.target_agent_ids || []) {
        if (agentId === LOCAL_AGENT_ID) {
          persistManagedCertificateBundleForAgent(agentId);
          persistManagedCertificatePolicyForAgent(agentId);
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
      const existing = getManagedCertificateById(certId);
      if (!existing) {
        sendJson(res, 404, errorPayload("certificate not found"));
        return;
      }
      const cert =
        existing.issuer_mode === "local_http01"
          ? await requestLocalHttp01CertificateById(certId)
          : await issueManagedCertificateById(certId, { bumpRevision: true });
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
  const [rawPath = "/", rawQuery = ""] = String(req.url || "").split("?");
  if (rawPath === "/panel-api" || rawPath.startsWith("/panel-api/")) {
    const apiPath = `/api${rawPath.slice("/panel-api".length) || ""}`;
    req.url = rawQuery ? `${apiPath}?${rawQuery}` : apiPath;
  }

  const urlPath = (req.url || "").split("?")[0];

  if (urlPath.startsWith("/agent-api/")) {
    await handleAgentApi(req, res);
    return;
  }

  if (ROLE !== "agent" && !urlPath.startsWith("/api/")) {
    if (tryServeFrontend(req, res, urlPath)) {
      return;
    }
  }

  if (ROLE === "agent") {
    if (req.method === "GET" && urlPath === "/api/auth/verify") {
      const authorized = isPanelAuthorized(req);
      sendJson(res, authorized ? 200 : 401, { ok: authorized, role: ROLE });
      return;
    }

    if ((req.method === "GET" || req.method === "HEAD") && urlPath === "/api/health") {
      sendJson(res, 200, { ok: true, role: ROLE });
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

    if (req.method === "GET" && urlPath === "/api/info") {
      sendJson(res, 200, {
        ok: true,
        role: ROLE,
        agent_name: AGENT_NAME,
        agent_url: AGENT_PUBLIC_URL,
        proxy_headers_globally_disabled: isProxyHeadersGloballyDisabled(),
      });
      return;
    }

    sendJson(res, 404, errorPayload("agent mode does not expose panel management APIs"));
    return;
  }

  await handleMasterApi(req, res);
}

ensureDataDir();
storage.init(DATA_ROOT);
storage.migrateFromJson(DATA_ROOT);
startManagedCertificateAutoRenewLoop();

const server = http.createServer((req, res) => {
  handleRequest(req, res).catch((err) => {
    sendJson(
      res,
      500,
      errorPayload("internal server error", String(err.message || err)),
    );
  });
});

server.listen(PORT, HOST, () => {
  console.log(`Panel backend listening on ${HOST}:${PORT} (storage: ${process.env.PANEL_STORAGE_BACKEND || "sqlite"})`);
});

function gracefulShutdown(signal) {
  console.log(`Received ${signal}, shutting down...`);
  server.close(() => {
    storage.close();
    process.exit(0);
  });
  setTimeout(() => {
    storage.close();
    process.exit(1);
  }, 5000);
}

process.on("SIGTERM", () => gracefulShutdown("SIGTERM"));
process.on("SIGINT", () => gracefulShutdown("SIGINT"));
