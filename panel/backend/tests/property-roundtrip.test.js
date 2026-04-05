"use strict";

const { describe, it, beforeEach, afterEach } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("fs");
const os = require("os");
const path = require("path");
const { pathToFileURL } = require("url");
const fc = require("fast-check");
const { PrismaClient } = require("@prisma/client");
const { PrismaLibSql } = require("@prisma/adapter-libsql");
const { normalizeCustomHeaders } = require("../http-rule-request-headers");
const {
  SQLITE_TARGET,
  canRunSqlite,
  safeString,
  loadFreshStorage,
  closeQuietly,
  dedupById,
  getNumRuns,
} = require("./helpers");

const NUM_RUNS = getNumRuns("roundtrip", 25);

const customHeaderArb = fc.record({
  name: fc.constantFrom("Referer", "X-Test", "Host", "X-Forwarded-For"),
  value: safeString,
});

const ruleArb = fc.record({
  id: fc.integer({ min: 1, max: 10000 }),
  frontend_url: fc.webUrl(),
  backend_url: fc.webUrl(),
  enabled: fc.boolean(),
  tags: fc.array(safeString, { maxLength: 5 }),
  proxy_redirect: fc.boolean(),
  pass_proxy_headers: fc.boolean(),
  user_agent: safeString,
  custom_headers: fc.array(customHeaderArb, { maxLength: 4 }),
  revision: fc.integer({ min: 0, max: 1000 }),
});

const l4RuleArb = fc.record({
  id: fc.integer({ min: 1, max: 10000 }),
  name: safeString,
  protocol: fc.constantFrom("tcp", "udp"),
  listen_host: fc.constant("0.0.0.0"),
  listen_port: fc.integer({ min: 1, max: 65535 }),
  upstream_host: safeString,
  upstream_port: fc.integer({ min: 0, max: 65535 }),
  backends: fc.array(
    fc.record({
      host: safeString,
      port: fc.integer({ min: 1, max: 65535 }),
    }),
    { maxLength: 3 }
  ),
  load_balancing: fc.record({
    method: fc.constantFrom("round_robin", "least_conn", "ip_hash"),
  }),
  tuning: fc.record({ timeout: fc.integer({ min: 0, max: 300 }) }),
  enabled: fc.boolean(),
  tags: fc.array(safeString, { maxLength: 5 }),
  revision: fc.integer({ min: 0, max: 1000 }),
});

const agentArb = fc.record({
  id: fc.uuid(),
  name: fc.string({ minLength: 1, maxLength: 50 }),
  agent_url: fc.webUrl(),
  agent_token: safeString,
  version: safeString,
  platform: safeString,
  desired_version: safeString,
  tags: fc.array(safeString, { maxLength: 5 }),
  capabilities: fc.array(safeString, { maxLength: 5 }),
  mode: fc.constantFrom("pull", "push"),
  desired_revision: fc.integer({ min: 0, max: 1000 }),
  current_revision: fc.integer({ min: 0, max: 1000 }),
  last_apply_revision: fc.integer({ min: 0, max: 1000 }),
  last_apply_status: fc.constantFrom("success", "error", null),
  last_apply_message: safeString,
  last_reported_stats: fc.option(
    fc.record({ cpu: fc.integer({ min: 0, max: 100 }), mem: fc.integer({ min: 0, max: 100 }) }),
    { nil: null }
  ),
  last_seen_at: fc.option(
    fc.date({ min: new Date("2020-01-01"), max: new Date("2030-01-01") }).map((d) => d.toISOString()),
    { nil: null }
  ),
  last_seen_ip: fc.option(fc.ipV4(), { nil: null }),
  created_at: fc.option(
    fc.date({ min: new Date("2020-01-01"), max: new Date("2030-01-01") }).map((d) => d.toISOString()),
    { nil: null }
  ),
  updated_at: fc.option(
    fc.date({ min: new Date("2020-01-01"), max: new Date("2030-01-01") }).map((d) => d.toISOString()),
    { nil: null }
  ),
  error: fc.option(safeString, { nil: null }),
  is_local: fc.boolean(),
});

const certArb = fc.record({
  id: fc.integer({ min: 1, max: 10000 }),
  domain: fc.domain(),
  enabled: fc.boolean(),
  scope: fc.constantFrom("domain", "ip"),
  issuer_mode: fc.constantFrom("master_cf_dns", "local_http01"),
  target_agent_ids: fc.array(fc.uuid(), { maxLength: 3 }),
  status: fc.constantFrom("pending", "active", "error"),
  last_issue_at: fc.option(
    fc.date({ min: new Date("2020-01-01"), max: new Date("2030-01-01") }).map((d) => d.toISOString()),
    { nil: null }
  ),
  last_error: safeString,
  material_hash: safeString,
  agent_reports: fc.constant({}),
  acme_info: fc.constant({}),
  tags: fc.array(safeString, { maxLength: 5 }),
  revision: fc.integer({ min: 0, max: 1000 }),
});

const localStateArb = fc.record({
  desired_revision: fc.integer({ min: 0, max: 1000 }),
  current_revision: fc.integer({ min: 0, max: 1000 }),
  last_apply_revision: fc.integer({ min: 0, max: 1000 }),
  last_apply_status: fc.constantFrom("success", "error"),
  last_apply_message: safeString,
  desired_version: safeString,
});

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/**
 * Normalize a value the same way storage-sqlite does on save:
 * - falsy strings become ""
 * - falsy numbers become 0
 * - null/undefined stay null when the column is nullable
 */
function normStr(v) { return v || ""; }
function normInt(v) { return v || 0; }

function sanitizeStoredCustomHeaders(value) {
  if (!Array.isArray(value)) {
    return [];
  }
  const seen = new Set();
  const sanitized = [];
  for (const header of value) {
    try {
      const normalized = normalizeCustomHeaders([header])[0];
      const key = normalized.name.toLowerCase();
      if (seen.has(key)) {
        continue;
      }
      seen.add(key);
      sanitized.push(normalized);
    } catch (_) {
      // drop malformed rows
    }
  }
  return sanitized;
}

/** Deduplicate rules by id (last wins) so the PRIMARY KEY constraint is met. */
/**
 * Build the expected rule object after a round-trip through SQLite.
 * Mirrors the transformations in saveRulesForAgent + loadRulesForAgent.
 */
function expectedRule(r, agentId) {
  return {
    id: r.id,
    agent_id: agentId,
    frontend_url: r.frontend_url,
    backend_url: r.backend_url,
    enabled: !!r.enabled,
    tags: r.tags || [],
    proxy_redirect: !!r.proxy_redirect,
    pass_proxy_headers: r.pass_proxy_headers !== false,
    user_agent: r.user_agent || "",
    custom_headers: sanitizeStoredCustomHeaders(r.custom_headers),
    revision: normInt(r.revision),
  };
}

function expectedL4Rule(r, agentId) {
  return {
    id: r.id,
    agent_id: agentId,
    name: normStr(r.name),
    protocol: normStr(r.protocol) || "tcp",
    listen_host: normStr(r.listen_host) || "0.0.0.0",
    listen_port: r.listen_port,
    upstream_host: normStr(r.upstream_host),
    upstream_port: normInt(r.upstream_port),
    backends: r.backends || [],
    load_balancing: r.load_balancing || {},
    tuning: r.tuning || {},
    enabled: !!r.enabled,
    tags: r.tags || [],
    revision: normInt(r.revision),
  };
}

function expectedAgent(a) {
  return {
    id: a.id,
    name: a.name,
    agent_url: normStr(a.agent_url),
    agent_token: normStr(a.agent_token),
    version: normStr(a.version),
    platform: normStr(a.platform),
    desired_version: normStr(a.desired_version),
    tags: a.tags || [],
    capabilities: a.capabilities || [],
    mode: normStr(a.mode) || "pull",
    desired_revision: normInt(a.desired_revision),
    current_revision: normInt(a.current_revision),
    last_apply_revision: normInt(a.last_apply_revision),
    last_apply_status: a.last_apply_status || null,
    last_apply_message: normStr(a.last_apply_message),
    last_reported_stats: a.last_reported_stats != null ? a.last_reported_stats : null,
    last_seen_at: a.last_seen_at || null,
    last_seen_ip: a.last_seen_ip || null,
    created_at: a.created_at || null,
    updated_at: a.updated_at || null,
    error: a.error || null,
    is_local: !!a.is_local,
  };
}

function expectedCert(c) {
  return {
    id: c.id,
    domain: c.domain,
    enabled: !!c.enabled,
    scope: normStr(c.scope) || "domain",
    issuer_mode: normStr(c.issuer_mode) || "master_cf_dns",
    target_agent_ids: c.target_agent_ids || [],
    status: normStr(c.status) || "pending",
    last_issue_at: c.last_issue_at || null,
    last_error: normStr(c.last_error),
    material_hash: normStr(c.material_hash),
    agent_reports: c.agent_reports || {},
    acme_info: c.acme_info || {},
    tags: c.tags || [],
    revision: normInt(c.revision),
  };
}

function expectedLocalState(s) {
  return {
    desired_revision: normInt(s.desired_revision),
    current_revision: normInt(s.current_revision),
    last_apply_revision: normInt(s.last_apply_revision),
    last_apply_status: normStr(s.last_apply_status) || "success",
    last_apply_message: normStr(s.last_apply_message),
    desired_version: normStr(s.desired_version),
  };
}

async function seedLegacyRulesSchema(dataRoot) {
  fs.mkdirSync(dataRoot, { recursive: true });
  const adapter = new PrismaLibSql({
    url: pathToFileURL(path.join(dataRoot, "panel.db")).href,
  });
  const client = new PrismaClient({ adapter });
  try {
    await client.$executeRawUnsafe(`CREATE TABLE IF NOT EXISTS rules (
      id INTEGER NOT NULL,
      agent_id TEXT NOT NULL,
      frontend_url TEXT NOT NULL,
      backend_url TEXT NOT NULL,
      enabled INTEGER DEFAULT 1,
      tags TEXT DEFAULT '[]',
      proxy_redirect INTEGER DEFAULT 1,
      revision INTEGER DEFAULT 0,
      PRIMARY KEY (agent_id, id)
    )`);
    await client.$executeRawUnsafe(`CREATE TABLE IF NOT EXISTS meta (
      key TEXT PRIMARY KEY,
      value TEXT
    )`);
    await client.$executeRawUnsafe(
      "INSERT OR REPLACE INTO meta (key, value) VALUES ('schema_version', '1')",
    );
    await client.$executeRawUnsafe(
      "INSERT INTO rules (id, agent_id, frontend_url, backend_url, enabled, tags, proxy_redirect, revision) VALUES (1, 'legacy-agent', 'http://legacy.frontend', 'http://legacy.backend', 1, '[]', 1, 7)",
    );
  } finally {
    await client.$disconnect();
  }
}

// ---------------------------------------------------------------------------
// Test suite
// ---------------------------------------------------------------------------

describe("Property 1: Data round-trip consistency", { skip: !canRunSqlite && "Prisma-backed SQLite adapter not available" }, () => {
  let storage;

  beforeEach(() => {
    storage = loadFreshStorage("../storage-sqlite", SQLITE_TARGET);
  });

  afterEach(() => {
    closeQuietly(storage);
  });

  it("rules round-trip: save then load returns semantically equivalent data", () => {
    const agentId = "test-agent-rules";
    fc.assert(
      fc.property(
        fc.array(ruleArb, { maxLength: 10 }),
        (rules) => {
          const unique = dedupById(rules);
          storage.saveRulesForAgent(agentId, unique);
          const loaded = storage.loadRulesForAgent(agentId);
          const expected = unique.map((r) => expectedRule(r, agentId));
          assert.deepStrictEqual(loaded, expected);
        }
      ),
      { numRuns: NUM_RUNS }
    );
  });

  it("L4 rules round-trip: save then load returns semantically equivalent data", () => {
    const agentId = "test-agent-l4";
    fc.assert(
      fc.property(
        fc.array(l4RuleArb, { maxLength: 10 }),
        (rules) => {
          const unique = dedupById(rules);
          storage.saveL4RulesForAgent(agentId, unique);
          const loaded = storage.loadL4RulesForAgent(agentId);
          const expected = unique.map((r) => expectedL4Rule(r, agentId));
          assert.deepStrictEqual(loaded, expected);
        }
      ),
      { numRuns: NUM_RUNS }
    );
  });

  it("agents round-trip: save then load returns semantically equivalent data", () => {
    fc.assert(
      fc.property(
        fc.array(agentArb, { maxLength: 10 }),
        (agents) => {
          const unique = dedupById(agents);
          storage.saveRegisteredAgents(unique);
          const loaded = storage.loadRegisteredAgents();
          const expected = unique.map((a) => expectedAgent(a));
          // Sort both by id for stable comparison (SELECT * has no ORDER BY)
          loaded.sort((a, b) => a.id.localeCompare(b.id));
          expected.sort((a, b) => a.id.localeCompare(b.id));
          assert.deepStrictEqual(loaded, expected);
        }
      ),
      { numRuns: NUM_RUNS }
    );
  });

  it("managed certificates round-trip: save then load returns semantically equivalent data", () => {
    fc.assert(
      fc.property(
        fc.array(certArb, { maxLength: 10 }),
        (certs) => {
          const unique = dedupById(certs);
          storage.saveManagedCertificates(unique);
          const loaded = storage.loadManagedCertificates();
          const expected = unique.map((c) => expectedCert(c));
          // Sort by id for stable comparison
          loaded.sort((a, b) => a.id - b.id);
          expected.sort((a, b) => a.id - b.id);
          assert.deepStrictEqual(loaded, expected);
        }
      ),
      { numRuns: NUM_RUNS }
    );
  });

  it("local agent state round-trip: save then load returns semantically equivalent data", () => {
    fc.assert(
      fc.property(localStateArb, (state) => {
        storage.saveLocalAgentState(state);
        const loaded = storage.loadLocalAgentState();
        const expected = expectedLocalState(state);
        assert.deepStrictEqual(loaded, expected);
      }),
      { numRuns: NUM_RUNS }
    );
  });

  it("migrates legacy rules table and preserves request-header fields", async () => {
    const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), "legacy-rules-schema-"));
    let migratedStorage = null;
    try {
      await seedLegacyRulesSchema(tmpDir);
      migratedStorage = loadFreshStorage("../storage-sqlite", tmpDir);

      const legacyRules = migratedStorage.loadRulesForAgent("legacy-agent");
      assert.deepStrictEqual(legacyRules, [
        {
          id: 1,
          agent_id: "legacy-agent",
          frontend_url: "http://legacy.frontend",
          backend_url: "http://legacy.backend",
          enabled: true,
          tags: [],
          proxy_redirect: true,
          pass_proxy_headers: true,
          user_agent: "",
          custom_headers: [],
          revision: 7,
        },
      ]);

      migratedStorage.saveRulesForAgent("legacy-agent", [{
        id: 1,
        frontend_url: "http://legacy.frontend",
        backend_url: "http://legacy.backend",
        enabled: true,
        tags: [],
        proxy_redirect: true,
        pass_proxy_headers: false,
        user_agent: "LegacyAgent/1.0",
        custom_headers: [{ name: "X-Test", value: "migrated" }],
        revision: 8,
      }]);

      const updatedRules = migratedStorage.loadRulesForAgent("legacy-agent");
      assert.deepStrictEqual(updatedRules, [
        {
          id: 1,
          agent_id: "legacy-agent",
          frontend_url: "http://legacy.frontend",
          backend_url: "http://legacy.backend",
          enabled: true,
          tags: [],
          proxy_redirect: true,
          pass_proxy_headers: false,
          user_agent: "LegacyAgent/1.0",
          custom_headers: [{ name: "X-Test", value: "migrated" }],
          revision: 8,
        },
      ]);
    } finally {
      closeQuietly(migratedStorage);
      try { fs.rmSync(tmpDir, { recursive: true, force: true }); } catch (_) { /* ignore */ }
    }
  });

  it("normalizes malformed custom_headers across save, restart, and reload", () => {
    const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), "rules-custom-headers-"));
    let firstStorage = null;
    let restartedStorage = null;
    try {
      firstStorage = loadFreshStorage("../storage-sqlite", tmpDir);
      firstStorage.saveRulesForAgent("agent-a", [{
        id: 1,
        frontend_url: "http://frontend.local",
        backend_url: "http://backend.local",
        enabled: true,
        tags: [],
        proxy_redirect: true,
        pass_proxy_headers: true,
        user_agent: "",
        custom_headers: { bad: "shape" },
        revision: 1,
      }]);

      const beforeRestart = firstStorage.loadRulesForAgent("agent-a");
      assert.deepStrictEqual(beforeRestart[0].custom_headers, []);

      closeQuietly(firstStorage);
      firstStorage = null;

      restartedStorage = loadFreshStorage("../storage-sqlite", tmpDir);
      const afterRestart = restartedStorage.loadRulesForAgent("agent-a");
      assert.deepStrictEqual(afterRestart[0].custom_headers, []);
    } finally {
      closeQuietly(firstStorage);
      closeQuietly(restartedStorage);
      try { fs.rmSync(tmpDir, { recursive: true, force: true }); } catch (_) { /* ignore */ }
    }
  });

  it("sanitizes malformed custom_headers entries across save, restart, and reload", () => {
    const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), "rules-custom-headers-entries-"));
    let firstStorage = null;
    let restartedStorage = null;
    try {
      firstStorage = loadFreshStorage("../storage-sqlite", tmpDir);
      firstStorage.saveRulesForAgent("agent-a", [{
        id: 1,
        frontend_url: "http://frontend.local",
        backend_url: "http://backend.local",
        enabled: true,
        tags: [],
        proxy_redirect: true,
        pass_proxy_headers: true,
        user_agent: "",
        custom_headers: [
          {},
          { name: "X-Test", value: "one" },
          { name: "x-test", value: "two" },
          { name: "User-Agent", value: "bad" },
          { name: 123, value: "bad" },
          { name: "X-Bad", value: 42 },
        ],
        revision: 1,
      }]);

      const beforeRestart = firstStorage.loadRulesForAgent("agent-a");
      assert.deepStrictEqual(beforeRestart[0].custom_headers, [{ name: "X-Test", value: "one" }]);

      closeQuietly(firstStorage);
      firstStorage = null;

      restartedStorage = loadFreshStorage("../storage-sqlite", tmpDir);
      const afterRestart = restartedStorage.loadRulesForAgent("agent-a");
      assert.deepStrictEqual(afterRestart[0].custom_headers, [{ name: "X-Test", value: "one" }]);
    } finally {
      closeQuietly(firstStorage);
      closeQuietly(restartedStorage);
      try { fs.rmSync(tmpDir, { recursive: true, force: true }); } catch (_) { /* ignore */ }
    }
  });
});
