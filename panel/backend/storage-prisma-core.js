"use strict";

const fs = require("fs");
const path = require("path");
const { pathToFileURL } = require("url");
const { PrismaClient } = require("@prisma/client");
const { PrismaLibSql } = require("@prisma/adapter-libsql");
const { normalizeCustomHeaders } = require("./http-rule-request-headers");
const { normalizeRelayListenerPayload } = require("./relay-listener-normalize");
const { normalizeVersionPolicyPayload } = require("./version-policy-normalize");

const DEFAULT_LOCAL_AGENT_STATE = Object.freeze({
  desired_revision: 0,
  current_revision: 0,
  last_apply_revision: 0,
  last_apply_status: "success",
  last_apply_message: "",
  desired_version: "",
});
const CURRENT_SCHEMA_VERSION = "4";
const MIGRATIONS_DIR = path.join(__dirname, "prisma", "migrations");
const REQUEST_HEADERS_SCHEMA_VERSION = 2;
const RELAY_VERSION_POLICY_SCHEMA_VERSION = 3;
const AGENT_PLATFORM_SCHEMA_VERSION = 4;
const CLIENT_STATE = {
  client: null,
  dataRoot: null,
  initialized: false,
};

const SCHEMA_STATEMENTS = [
  `CREATE TABLE IF NOT EXISTS agents (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    agent_url TEXT DEFAULT '',
    agent_token TEXT DEFAULT '',
    version TEXT DEFAULT '',
    platform TEXT DEFAULT '',
    desired_version TEXT DEFAULT '',
    tags TEXT DEFAULT '[]',
    capabilities TEXT DEFAULT '[]',
    mode TEXT DEFAULT 'pull',
    desired_revision INTEGER DEFAULT 0,
    current_revision INTEGER DEFAULT 0,
    last_apply_revision INTEGER DEFAULT 0,
    last_apply_status TEXT,
    last_apply_message TEXT DEFAULT '',
    last_reported_stats TEXT,
    last_seen_at TEXT,
    last_seen_ip TEXT,
    created_at TEXT,
    updated_at TEXT,
    error TEXT,
    is_local INTEGER DEFAULT 0
  )`,
  `CREATE TABLE IF NOT EXISTS rules (
    id INTEGER NOT NULL,
    agent_id TEXT NOT NULL,
    frontend_url TEXT NOT NULL,
    backend_url TEXT NOT NULL,
    enabled INTEGER DEFAULT 1,
    tags TEXT DEFAULT '[]',
    proxy_redirect INTEGER DEFAULT 1,
    revision INTEGER DEFAULT 0,
    PRIMARY KEY (agent_id, id)
  )`,
  "CREATE INDEX IF NOT EXISTS idx_rules_agent ON rules(agent_id)",
  `CREATE TABLE IF NOT EXISTS l4_rules (
    id INTEGER NOT NULL,
    agent_id TEXT NOT NULL,
    name TEXT DEFAULT '',
    protocol TEXT DEFAULT 'tcp',
    listen_host TEXT DEFAULT '0.0.0.0',
    listen_port INTEGER NOT NULL,
    upstream_host TEXT DEFAULT '',
    upstream_port INTEGER DEFAULT 0,
    backends TEXT DEFAULT '[]',
    load_balancing TEXT DEFAULT '{}',
    tuning TEXT DEFAULT '{}',
    enabled INTEGER DEFAULT 1,
    tags TEXT DEFAULT '[]',
    revision INTEGER DEFAULT 0,
    PRIMARY KEY (agent_id, id)
  )`,
  "CREATE INDEX IF NOT EXISTS idx_l4_rules_agent ON l4_rules(agent_id)",
  `CREATE TABLE IF NOT EXISTS relay_listeners (
    id INTEGER PRIMARY KEY,
    agent_id TEXT NOT NULL,
    name TEXT DEFAULT '',
    listen_host TEXT DEFAULT '0.0.0.0',
    listen_port INTEGER NOT NULL,
    enabled INTEGER DEFAULT 1,
    certificate_id INTEGER,
    tls_mode TEXT DEFAULT 'pin_or_ca',
    pin_set TEXT DEFAULT '[]',
    trusted_ca_certificate_ids TEXT DEFAULT '[]',
    allow_self_signed INTEGER DEFAULT 0,
    tags TEXT DEFAULT '[]',
    revision INTEGER DEFAULT 0
  )`,
  "CREATE INDEX IF NOT EXISTS idx_relay_listeners_agent ON relay_listeners(agent_id)",
  `CREATE TABLE IF NOT EXISTS managed_certificates (
    id INTEGER PRIMARY KEY,
    domain TEXT NOT NULL,
    enabled INTEGER DEFAULT 1,
    scope TEXT DEFAULT 'domain',
    issuer_mode TEXT DEFAULT 'master_cf_dns',
    target_agent_ids TEXT DEFAULT '[]',
    status TEXT DEFAULT 'pending',
    last_issue_at TEXT,
    last_error TEXT DEFAULT '',
    material_hash TEXT DEFAULT '',
    agent_reports TEXT DEFAULT '{}',
    acme_info TEXT DEFAULT '{}',
    tags TEXT DEFAULT '[]',
    revision INTEGER DEFAULT 0
  )`,
  `CREATE TABLE IF NOT EXISTS local_agent_state (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    desired_revision INTEGER DEFAULT 0,
    current_revision INTEGER DEFAULT 0,
    last_apply_revision INTEGER DEFAULT 0,
    last_apply_status TEXT DEFAULT 'success',
    last_apply_message TEXT DEFAULT '',
    desired_version TEXT DEFAULT ''
  )`,
  `CREATE TABLE IF NOT EXISTS version_policy (
    id TEXT PRIMARY KEY,
    channel TEXT DEFAULT 'stable',
    desired_version TEXT DEFAULT '',
    packages TEXT DEFAULT '[]',
    tags TEXT DEFAULT '[]'
  )`,
  `CREATE TABLE IF NOT EXISTS meta (
    key TEXT PRIMARY KEY,
    value TEXT
  )`,
];

function parseSchemaVersion(value) {
  const parsed = Number.parseInt(String(value ?? ""), 10);
  return Number.isInteger(parsed) && parsed >= 0 ? parsed : null;
}

function splitSqlStatements(sqlText) {
  return String(sqlText || "")
    .split(";")
    .map((statement) => statement.trim())
    .filter(Boolean);
}

function loadSqlMigrations() {
  if (!fs.existsSync(MIGRATIONS_DIR)) {
    return [];
  }

  const files = fs.readdirSync(MIGRATIONS_DIR, { withFileTypes: true })
    .filter((entry) => entry.isFile())
    .map((entry) => {
      const match = entry.name.match(/^(\d+)_([a-z0-9_\-]+)\.sql$/i);
      if (!match) {
        return null;
      }
      return {
        version: Number.parseInt(match[1], 10),
        fileName: entry.name,
        fullPath: path.join(MIGRATIONS_DIR, entry.name),
      };
    })
    .filter(Boolean)
    .sort((a, b) => a.version - b.version);

  const seenVersions = new Set();
  const migrations = [];
  for (const file of files) {
    if (!Number.isInteger(file.version)) {
      continue;
    }
    if (seenVersions.has(file.version)) {
      throw new Error(`Duplicate Prisma SQL migration version: ${file.version}`);
    }
    seenVersions.add(file.version);
    migrations.push({
      version: file.version,
      fileName: file.fileName,
      sql: fs.readFileSync(file.fullPath, "utf8"),
    });
  }

  return migrations;
}

async function readTableColumnNames(client, tableName) {
  const rows = await client.$queryRawUnsafe(`PRAGMA table_info('${String(tableName)}')`);
  return new Set(
    (Array.isArray(rows) ? rows : [])
      .map((row) => {
        if (!row || typeof row !== "object") {
          return "";
        }
        return String(row.name || row.column_name || "");
      })
      .filter(Boolean),
  );
}

async function inferSchemaVersionWithoutMeta(client) {
  const ruleColumns = await readTableColumnNames(client, "rules");
  const agentColumns = await readTableColumnNames(client, "agents");
  const localAgentStateColumns = await readTableColumnNames(client, "local_agent_state");
  const hasRequestHeaderColumns = ["pass_proxy_headers", "user_agent", "custom_headers"]
    .every((column) => ruleColumns.has(column));
  const hasVersionPolicyColumns = agentColumns.has("desired_version") && localAgentStateColumns.has("desired_version");
  const hasAgentPlatformColumn = agentColumns.has("platform");
  if (hasRequestHeaderColumns && hasVersionPolicyColumns && hasAgentPlatformColumn) {
    return AGENT_PLATFORM_SCHEMA_VERSION;
  }
  if (hasRequestHeaderColumns && hasVersionPolicyColumns) {
    return RELAY_VERSION_POLICY_SCHEMA_VERSION;
  }
  return hasRequestHeaderColumns ? REQUEST_HEADERS_SCHEMA_VERSION : 1;
}

function isIgnorableSqlMigrationError(error) {
  const message = String(error && error.message ? error.message : error);
  return /duplicate column name/i.test(message) || /already exists/i.test(message);
}

async function applySqlMigration(client, migration) {
  const statements = splitSqlStatements(migration.sql);
  for (const statement of statements) {
    try {
      await client.$executeRawUnsafe(statement);
    } catch (error) {
      if (!isIgnorableSqlMigrationError(error)) {
        throw error;
      }
    }
  }

  await client.meta.upsert({
    where: { key: "schema_version" },
    update: { value: String(migration.version) },
    create: { key: "schema_version", value: String(migration.version) },
  });
}

async function applyPendingSchemaMigrations(client) {
  const migrations = loadSqlMigrations();
  const targetVersion = parseSchemaVersion(CURRENT_SCHEMA_VERSION) || 0;
  const latestMigrationVersion = migrations.length > 0
    ? migrations[migrations.length - 1].version
    : null;

  const schemaVersionRow = await client.meta.findUnique({ where: { key: "schema_version" } });
  let currentVersion = parseSchemaVersion(schemaVersionRow && schemaVersionRow.value);
  if (currentVersion === null) {
    currentVersion = await inferSchemaVersionWithoutMeta(client);
  }

  for (const migration of migrations) {
    if (migration.version <= currentVersion) {
      continue;
    }
    await applySqlMigration(client, migration);
    currentVersion = migration.version;
  }

  const maxReachableVersion = latestMigrationVersion == null
    ? currentVersion
    : Math.max(currentVersion, latestMigrationVersion);
  if (maxReachableVersion < targetVersion) {
    throw new Error(`Missing Prisma SQL migration files for schema version ${targetVersion}`);
  }

  const finalVersion = Math.max(currentVersion, targetVersion);
  await client.meta.upsert({
    where: { key: "schema_version" },
    update: { value: String(finalVersion) },
    create: { key: "schema_version", value: String(finalVersion) },
  });
}

function defaultLocalAgentState() {
  return { ...DEFAULT_LOCAL_AGENT_STATE };
}

function normalizeRoot(dataRoot) {
  if (!dataRoot || dataRoot === ":memory:") {
    throw new Error("Prisma storage requires a writable directory path");
  }
  return dataRoot;
}

function resolveDatabasePath(dataRoot) {
  return path.join(normalizeRoot(dataRoot), "panel.db");
}

function createPrismaClient(dataRoot) {
  const normalizedRoot = normalizeRoot(dataRoot);
  fs.mkdirSync(normalizedRoot, { recursive: true });
  const adapter = new PrismaLibSql({
    url: pathToFileURL(resolveDatabasePath(normalizedRoot)).href,
  });
  return new PrismaClient({ adapter });
}

async function closeClient() {
  if (!CLIENT_STATE.client) {
    return;
  }
  await CLIENT_STATE.client.$disconnect();
  CLIENT_STATE.client = null;
  CLIENT_STATE.dataRoot = null;
  CLIENT_STATE.initialized = false;
}

async function getClient(dataRoot) {
  const normalizedRoot = normalizeRoot(dataRoot);
  if (CLIENT_STATE.client && CLIENT_STATE.dataRoot === normalizedRoot) {
    return CLIENT_STATE.client;
  }

  await closeClient();
  CLIENT_STATE.client = createPrismaClient(normalizedRoot);
  CLIENT_STATE.dataRoot = normalizedRoot;
  CLIENT_STATE.initialized = false;
  return CLIENT_STATE.client;
}

async function withClient(dataRoot, handler) {
  const client = await getClient(dataRoot);
  if (!CLIENT_STATE.initialized) {
    await initializeDatabase(client);
    CLIENT_STATE.initialized = true;
  }
  return handler(client);
}

async function initializeDatabase(client) {
  for (const statement of SCHEMA_STATEMENTS) {
    await client.$executeRawUnsafe(statement);
  }
  await applyPendingSchemaMigrations(client);

  await client.localAgentState.upsert({
    where: { id: 1 },
    update: {},
    create: {
      id: 1,
      desiredRevision: 0,
      currentRevision: 0,
      lastApplyRevision: 0,
      lastApplyStatus: "success",
      lastApplyMessage: "",
      desiredVersion: "",
    },
  });
}

function parseJsonValue(value, fallback) {
  if (value === null || value === undefined || value === "") {
    return fallback;
  }
  try {
    return JSON.parse(value);
  } catch {
    return fallback;
  }
}

function stringifyJsonValue(value, fallback) {
  return JSON.stringify(value == null ? fallback : value);
}

function ensureArray(value) {
  return Array.isArray(value) ? value : [];
}

function sanitizeStoredCustomHeaders(value) {
  const headers = ensureArray(value);
  const seen = new Set();
  const sanitized = [];
  for (const header of headers) {
    try {
      const normalized = normalizeCustomHeaders([header])[0];
      const key = normalized.name.toLowerCase();
      if (seen.has(key)) {
        continue;
      }
      seen.add(key);
      sanitized.push(normalized);
    } catch (_) {
      // drop malformed custom header rows at storage boundary
    }
  }
  return sanitized;
}

function mapAgentFromDb(row) {
  return {
    id: row.id,
    name: row.name,
    agent_url: row.agentUrl || "",
    agent_token: row.agentToken || "",
    version: row.version || "",
    platform: row.platform || "",
    desired_version: row.desiredVersion || "",
    tags: parseJsonValue(row.tags, []),
    capabilities: parseJsonValue(row.capabilities, []),
    mode: row.mode || "pull",
    desired_revision: row.desiredRevision || 0,
    current_revision: row.currentRevision || 0,
    last_apply_revision: row.lastApplyRevision || 0,
    last_apply_status: row.lastApplyStatus || null,
    last_apply_message: row.lastApplyMessage || "",
    last_reported_stats: row.lastReportedStats ? parseJsonValue(row.lastReportedStats, null) : null,
    last_seen_at: row.lastSeenAt || null,
    last_seen_ip: row.lastSeenIp || null,
    created_at: row.createdAt || null,
    updated_at: row.updatedAt || null,
    error: row.error || null,
    is_local: !!row.isLocal,
  };
}

function mapRuleFromDb(row) {
  return {
    id: row.id,
    agent_id: row.agentId,
    frontend_url: row.frontendUrl,
    backend_url: row.backendUrl,
    enabled: !!row.enabled,
    tags: parseJsonValue(row.tags, []),
    proxy_redirect: !!row.proxyRedirect,
    pass_proxy_headers: row.passProxyHeaders !== false,
    user_agent: row.userAgent || "",
    custom_headers: sanitizeStoredCustomHeaders(parseJsonValue(row.customHeaders, [])),
    revision: row.revision || 0,
  };
}

function mapL4RuleFromDb(row) {
  return {
    id: row.id,
    agent_id: row.agentId,
    name: row.name || "",
    protocol: row.protocol || "tcp",
    listen_host: row.listenHost || "0.0.0.0",
    listen_port: row.listenPort,
    upstream_host: row.upstreamHost || "",
    upstream_port: row.upstreamPort || 0,
    backends: parseJsonValue(row.backends, []),
    load_balancing: parseJsonValue(row.loadBalancing, {}),
    tuning: parseJsonValue(row.tuning, {}),
    enabled: !!row.enabled,
    tags: parseJsonValue(row.tags, []),
    revision: row.revision || 0,
  };
}

function mapManagedCertificateFromDb(row) {
  return {
    id: row.id,
    domain: row.domain,
    enabled: !!row.enabled,
    scope: row.scope || "domain",
    issuer_mode: row.issuerMode || "master_cf_dns",
    target_agent_ids: parseJsonValue(row.targetAgentIds, []),
    status: row.status || "pending",
    last_issue_at: row.lastIssueAt || null,
    last_error: row.lastError || "",
    material_hash: row.materialHash || "",
    agent_reports: parseJsonValue(row.agentReports, {}),
    acme_info: parseJsonValue(row.acmeInfo, {}),
    tags: parseJsonValue(row.tags, []),
    revision: row.revision || 0,
  };
}

function mapRelayListenerFromDb(row) {
  return normalizeRelayListenerPayload({
    id: row.id,
    agent_id: row.agentId,
    name: row.name || "",
    listen_host: row.listenHost || "0.0.0.0",
    listen_port: row.listenPort,
    enabled: !!row.enabled,
    certificate_id: row.certificateId == null ? null : row.certificateId,
    tls_mode: row.tlsMode || "pin_or_ca",
    pin_set: parseJsonValue(row.pinSet, []),
    trusted_ca_certificate_ids: parseJsonValue(row.trustedCaCertificateIds, []),
    allow_self_signed: !!row.allowSelfSigned,
    tags: parseJsonValue(row.tags, []),
    revision: row.revision || 0,
  });
}

function mapLocalAgentStateFromDb(row) {
  if (!row) {
    return defaultLocalAgentState();
  }
  return {
    desired_revision: row.desiredRevision || 0,
    current_revision: row.currentRevision || 0,
    last_apply_revision: row.lastApplyRevision || 0,
    last_apply_status: row.lastApplyStatus || "success",
    last_apply_message: row.lastApplyMessage || "",
    desired_version: row.desiredVersion || "",
  };
}

function mapVersionPolicyFromDb(row) {
  if (!row) {
    return null;
  }
  return normalizeVersionPolicyPayload({
    id: row.id,
    channel: row.channel || "stable",
    desired_version: row.desiredVersion || "",
    packages: parseJsonValue(row.packages, []),
    tags: parseJsonValue(row.tags, []),
  });
}

function mapVersionPoliciesFromDb(rows) {
  const normalized = [];
  const seen = new Set();
  for (const row of Array.isArray(rows) ? rows : []) {
    const policy = mapVersionPolicyFromDb(row);
    if (!policy || seen.has(policy.id)) {
      continue;
    }
    seen.add(policy.id);
    normalized.push(policy);
  }
  return normalized;
}

function mapAgentToDb(agent) {
  return {
    id: String(agent.id),
    name: String(agent.name),
    agentUrl: String(agent.agent_url || ""),
    agentToken: String(agent.agent_token || ""),
    version: String(agent.version || ""),
    platform: String(agent.platform || ""),
    desiredVersion: String(agent.desired_version || ""),
    tags: stringifyJsonValue(agent.tags, []),
    capabilities: stringifyJsonValue(agent.capabilities, []),
    mode: String(agent.mode || "pull"),
    desiredRevision: Number(agent.desired_revision || 0),
    currentRevision: Number(agent.current_revision || 0),
    lastApplyRevision: Number(agent.last_apply_revision || 0),
    lastApplyStatus: agent.last_apply_status != null ? String(agent.last_apply_status) : null,
    lastApplyMessage: String(agent.last_apply_message || ""),
    lastReportedStats:
      agent.last_reported_stats != null
        ? stringifyJsonValue(agent.last_reported_stats, null)
        : null,
    lastSeenAt: agent.last_seen_at || null,
    lastSeenIp: agent.last_seen_ip || null,
    createdAt: agent.created_at || null,
    updatedAt: agent.updated_at || null,
    error: agent.error || null,
    isLocal: !!agent.is_local,
  };
}

function mapRuleToDb(agentId, rule) {
  return {
    id: Number(rule.id),
    agentId: String(agentId),
    frontendUrl: String(rule.frontend_url),
    backendUrl: String(rule.backend_url),
    enabled: rule.enabled !== false,
    tags: stringifyJsonValue(rule.tags, []),
    proxyRedirect: rule.proxy_redirect !== false,
    passProxyHeaders: rule.pass_proxy_headers !== false,
    userAgent: String(rule.user_agent || ""),
    customHeaders: stringifyJsonValue(sanitizeStoredCustomHeaders(rule.custom_headers), []),
    revision: Number(rule.revision || 0),
  };
}

function mapL4RuleToDb(agentId, rule) {
  return {
    id: Number(rule.id),
    agentId: String(agentId),
    name: String(rule.name || ""),
    protocol: String(rule.protocol || "tcp"),
    listenHost: String(rule.listen_host || "0.0.0.0"),
    listenPort: Number(rule.listen_port),
    upstreamHost: String(rule.upstream_host || ""),
    upstreamPort: Number(rule.upstream_port || 0),
    backends: stringifyJsonValue(rule.backends, []),
    loadBalancing: stringifyJsonValue(rule.load_balancing, {}),
    tuning: stringifyJsonValue(rule.tuning, {}),
    enabled: rule.enabled !== false,
    tags: stringifyJsonValue(rule.tags, []),
    revision: Number(rule.revision || 0),
  };
}

function mapManagedCertificateToDb(cert) {
  return {
    id: Number(cert.id),
    domain: String(cert.domain),
    enabled: cert.enabled !== false,
    scope: String(cert.scope || "domain"),
    issuerMode: String(cert.issuer_mode || "master_cf_dns"),
    targetAgentIds: stringifyJsonValue(cert.target_agent_ids, []),
    status: String(cert.status || "pending"),
    lastIssueAt: cert.last_issue_at || null,
    lastError: String(cert.last_error || ""),
    materialHash: String(cert.material_hash || ""),
    agentReports: stringifyJsonValue(cert.agent_reports, {}),
    acmeInfo: stringifyJsonValue(cert.acme_info, {}),
    tags: stringifyJsonValue(cert.tags, []),
    revision: Number(cert.revision || 0),
  };
}

function mapRelayListenerToDb(agentId, listener) {
  const normalized = normalizeRelayListenerPayload({
    ...listener,
    agent_id: String(agentId),
  });
  if (!Number.isInteger(normalized.id)) {
    throw new TypeError("relay listener id is required for persistence");
  }
  return {
    id: Number(normalized.id),
    agentId: String(agentId),
    name: String(normalized.name || ""),
    listenHost: String(normalized.listen_host || "0.0.0.0"),
    listenPort: Number(normalized.listen_port),
    enabled: normalized.enabled !== false,
    certificateId: normalized.certificate_id == null ? null : Number(normalized.certificate_id),
    tlsMode: String(normalized.tls_mode || "pin_or_ca"),
    pinSet: stringifyJsonValue(normalized.pin_set, []),
    trustedCaCertificateIds: stringifyJsonValue(normalized.trusted_ca_certificate_ids, []),
    allowSelfSigned: !!normalized.allow_self_signed,
    tags: stringifyJsonValue(normalized.tags, []),
    revision: Number(normalized.revision || 0),
  };
}

function mapVersionPolicyToDb(policy) {
  const normalized = normalizeVersionPolicyPayload(policy);
  return {
    id: String(normalized.id),
    channel: String(normalized.channel || "stable"),
    desiredVersion: String(normalized.desired_version || ""),
    packages: stringifyJsonValue(normalized.packages, []),
    tags: stringifyJsonValue(normalized.tags, []),
  };
}

function groupByAgent(rows, mapper) {
  const grouped = {};
  for (const row of rows) {
    const mapped = mapper(row);
    if (!grouped[mapped.agent_id]) {
      grouped[mapped.agent_id] = [];
    }
    grouped[mapped.agent_id].push(mapped);
  }
  return grouped;
}

async function loadSnapshotFromClient(client) {
  const [agents, rules, l4Rules, relayListeners, managedCertificates, localAgentState, versionPolicies, metaRows] = await Promise.all([
    client.agent.findMany({ orderBy: { id: "asc" } }),
    client.rule.findMany({ orderBy: [{ agentId: "asc" }, { id: "asc" }] }),
    client.l4Rule.findMany({ orderBy: [{ agentId: "asc" }, { id: "asc" }] }),
    client.relayListener.findMany({ orderBy: [{ agentId: "asc" }, { id: "asc" }] }),
    client.managedCertificate.findMany({ orderBy: { id: "asc" } }),
    client.localAgentState.findUnique({ where: { id: 1 } }),
    client.versionPolicy.findMany({ orderBy: { id: "asc" } }),
    client.meta.findMany(),
  ]);

  return {
    agents: agents.map(mapAgentFromDb),
    rulesByAgent: groupByAgent(rules, mapRuleFromDb),
    l4RulesByAgent: groupByAgent(l4Rules, mapL4RuleFromDb),
    relayListenersByAgent: groupByAgent(relayListeners, mapRelayListenerFromDb),
    managedCertificates: managedCertificates.map(mapManagedCertificateFromDb),
    localAgentState: mapLocalAgentStateFromDb(localAgentState),
    versionPolicies: mapVersionPoliciesFromDb(versionPolicies),
    meta: Object.fromEntries(metaRows.map((row) => [row.key, row.value])),
  };
}

async function loadSnapshot(dataRoot) {
  return withClient(dataRoot, async (client) => loadSnapshotFromClient(client));
}

async function saveRegisteredAgents(dataRoot, agents) {
  return withClient(dataRoot, async (client) => {
    await client.$transaction(async (tx) => {
      await tx.agent.deleteMany();
      for (const agent of Array.isArray(agents) ? agents : []) {
        await tx.agent.create({ data: mapAgentToDb(agent) });
      }
    });
  });
}

async function saveRulesForAgent(dataRoot, agentId, rules) {
  return withClient(dataRoot, async (client) => {
    await client.$transaction(async (tx) => {
      await tx.rule.deleteMany({ where: { agentId: String(agentId) } });
      for (const rule of Array.isArray(rules) ? rules : []) {
        await tx.rule.create({ data: mapRuleToDb(agentId, rule) });
      }
    });
  });
}

async function deleteRulesForAgent(dataRoot, agentId) {
  return withClient(dataRoot, async (client) => {
    await client.rule.deleteMany({ where: { agentId: String(agentId) } });
  });
}

async function saveL4RulesForAgent(dataRoot, agentId, rules) {
  return withClient(dataRoot, async (client) => {
    await client.$transaction(async (tx) => {
      await tx.l4Rule.deleteMany({ where: { agentId: String(agentId) } });
      for (const rule of Array.isArray(rules) ? rules : []) {
        await tx.l4Rule.create({ data: mapL4RuleToDb(agentId, rule) });
      }
    });
  });
}

async function deleteL4RulesForAgent(dataRoot, agentId) {
  return withClient(dataRoot, async (client) => {
    await client.l4Rule.deleteMany({ where: { agentId: String(agentId) } });
  });
}

async function saveRelayListenersForAgent(dataRoot, agentId, listeners) {
  return withClient(dataRoot, async (client) => {
    await client.$transaction(async (tx) => {
      await tx.relayListener.deleteMany({ where: { agentId: String(agentId) } });
      for (const listener of Array.isArray(listeners) ? listeners : []) {
        await tx.relayListener.create({ data: mapRelayListenerToDb(agentId, listener) });
      }
    });
  });
}

async function deleteRelayListenersForAgent(dataRoot, agentId) {
  return withClient(dataRoot, async (client) => {
    await client.relayListener.deleteMany({ where: { agentId: String(agentId) } });
  });
}

async function saveManagedCertificates(dataRoot, certs) {
  return withClient(dataRoot, async (client) => {
    await client.$transaction(async (tx) => {
      await tx.managedCertificate.deleteMany();
      for (const cert of Array.isArray(certs) ? certs : []) {
        await tx.managedCertificate.create({ data: mapManagedCertificateToDb(cert) });
      }
    });
  });
}

async function saveLocalAgentState(dataRoot, state) {
  return withClient(dataRoot, async (client) => {
    const next = state && typeof state === "object" ? state : defaultLocalAgentState();
    await client.localAgentState.upsert({
      where: { id: 1 },
      update: {
        desiredRevision: Number(next.desired_revision || 0),
        currentRevision: Number(next.current_revision || 0),
        lastApplyRevision: Number(next.last_apply_revision || 0),
        lastApplyStatus: String(next.last_apply_status || "success"),
        lastApplyMessage: String(next.last_apply_message || ""),
        desiredVersion: String(next.desired_version || ""),
      },
      create: {
        id: 1,
        desiredRevision: Number(next.desired_revision || 0),
        currentRevision: Number(next.current_revision || 0),
        lastApplyRevision: Number(next.last_apply_revision || 0),
        lastApplyStatus: String(next.last_apply_status || "success"),
        lastApplyMessage: String(next.last_apply_message || ""),
        desiredVersion: String(next.desired_version || ""),
      },
    });
  });
}

async function saveVersionPolicies(dataRoot, policies) {
  return withClient(dataRoot, async (client) => {
    await client.$transaction(async (tx) => {
      await tx.versionPolicy.deleteMany();
      for (const policy of Array.isArray(policies) ? policies : []) {
        await tx.versionPolicy.create({ data: mapVersionPolicyToDb(policy) });
      }
    });
  });
}

async function migrateFromJsonPayload(dataRoot, payload) {
  return withClient(dataRoot, async (client) => {
    const alreadyMigrated = await client.meta.findUnique({ where: { key: "migrated_from_json" } });
    if (alreadyMigrated) {
      return {
        migrated: false,
        snapshot: await loadSnapshotFromClient(client),
      };
    }

    await client.$transaction(async (tx) => {
      await tx.rule.deleteMany();
      await tx.l4Rule.deleteMany();
      await tx.relayListener.deleteMany();
      await tx.managedCertificate.deleteMany();
      await tx.versionPolicy.deleteMany();
      await tx.agent.deleteMany();

      for (const agent of Array.isArray(payload?.agents) ? payload.agents : []) {
        await tx.agent.create({ data: mapAgentToDb(agent) });
      }

      const rulesByAgent = payload?.rulesByAgent && typeof payload.rulesByAgent === "object"
        ? payload.rulesByAgent
        : {};
      for (const [agentId, rules] of Object.entries(rulesByAgent)) {
        for (const rule of Array.isArray(rules) ? rules : []) {
          await tx.rule.create({ data: mapRuleToDb(agentId, rule) });
        }
      }

      const l4RulesByAgent = payload?.l4RulesByAgent && typeof payload.l4RulesByAgent === "object"
        ? payload.l4RulesByAgent
        : {};
      for (const [agentId, rules] of Object.entries(l4RulesByAgent)) {
        for (const rule of Array.isArray(rules) ? rules : []) {
          await tx.l4Rule.create({ data: mapL4RuleToDb(agentId, rule) });
        }
      }

      const relayListenersByAgent = payload?.relayListenersByAgent && typeof payload.relayListenersByAgent === "object"
        ? payload.relayListenersByAgent
        : {};
      for (const [agentId, listeners] of Object.entries(relayListenersByAgent)) {
        for (const listener of Array.isArray(listeners) ? listeners : []) {
          await tx.relayListener.create({ data: mapRelayListenerToDb(agentId, listener) });
        }
      }

      for (const cert of Array.isArray(payload?.managedCertificates) ? payload.managedCertificates : []) {
        await tx.managedCertificate.create({ data: mapManagedCertificateToDb(cert) });
      }

      const localState = payload?.localAgentState && typeof payload.localAgentState === "object"
        ? payload.localAgentState
        : defaultLocalAgentState();
      await tx.localAgentState.upsert({
        where: { id: 1 },
        update: {
          desiredRevision: Number(localState.desired_revision || 0),
          currentRevision: Number(localState.current_revision || 0),
          lastApplyRevision: Number(localState.last_apply_revision || 0),
          lastApplyStatus: String(localState.last_apply_status || "success"),
          lastApplyMessage: String(localState.last_apply_message || ""),
          desiredVersion: String(localState.desired_version || ""),
        },
        create: {
          id: 1,
          desiredRevision: Number(localState.desired_revision || 0),
          currentRevision: Number(localState.current_revision || 0),
          lastApplyRevision: Number(localState.last_apply_revision || 0),
          lastApplyStatus: String(localState.last_apply_status || "success"),
          lastApplyMessage: String(localState.last_apply_message || ""),
          desiredVersion: String(localState.desired_version || ""),
        },
      });

      const versionPolicies = Array.isArray(payload?.versionPolicies)
        ? payload.versionPolicies
        : payload?.versionPolicy && typeof payload.versionPolicy === "object"
          ? [payload.versionPolicy]
          : [];
      for (const policy of versionPolicies) {
        await tx.versionPolicy.create({ data: mapVersionPolicyToDb(policy) });
      }

      await tx.meta.upsert({
        where: { key: "migrated_from_json" },
        update: { value: new Date().toISOString() },
        create: { key: "migrated_from_json", value: new Date().toISOString() },
      });
    });

    return {
      migrated: true,
      snapshot: await loadSnapshotFromClient(client),
    };
  });
}

module.exports = {
  defaultLocalAgentState,
  loadSnapshot,
  saveRegisteredAgents,
  saveRulesForAgent,
  deleteRulesForAgent,
  saveL4RulesForAgent,
  deleteL4RulesForAgent,
  saveRelayListenersForAgent,
  deleteRelayListenersForAgent,
  saveManagedCertificates,
  saveLocalAgentState,
  saveVersionPolicies,
  migrateFromJsonPayload,
  closeClient,
};
