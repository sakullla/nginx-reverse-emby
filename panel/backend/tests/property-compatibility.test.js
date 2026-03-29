"use strict";

/**
 * Property 4: Backend compatibility (semantic consistency)
 *
 * For any valid storage operation sequence S (containing any combination of
 * save and load calls), executing S on both SQLite_Adapter and JSON_Adapter
 * produces semantically equivalent results from all load methods.
 *
 * **Validates: Requirement 1.4**
 */

const { describe, it, beforeEach, afterEach } = require("node:test");
const assert = require("node:assert/strict");
const fc = require("fast-check");
const fs = require("fs");
const os = require("os");
const path = require("path");
const {
  SQLITE_TARGET,
  canRunSqlite,
  safeString,
  nonEmptyString,
  loadFreshStorage,
  closeQuietly,
  dedupById,
  getNumRuns,
} = require("./helpers");

const NUM_RUNS = getNumRuns("compatibility", 20);

const ruleArb = fc.record({
  id: fc.integer({ min: 1, max: 10000 }),
  frontend_url: fc.webUrl(),
  backend_url: fc.webUrl(),
  enabled: fc.boolean(),
  tags: fc.array(safeString, { maxLength: 5 }),
  proxy_redirect: fc.boolean(),
  revision: fc.integer({ min: 1, max: 1000 }),
});

const l4RuleArb = fc.record({
  id: fc.integer({ min: 1, max: 10000 }),
  name: nonEmptyString,
  protocol: fc.constantFrom("tcp", "udp"),
  listen_host: fc.constant("0.0.0.0"),
  listen_port: fc.integer({ min: 1, max: 65535 }),
  upstream_host: nonEmptyString,
  upstream_port: fc.integer({ min: 1, max: 65535 }),
  backends: fc.array(
    fc.record({
      host: nonEmptyString,
      port: fc.integer({ min: 1, max: 65535 }),
    }),
    { maxLength: 3 }
  ),
  load_balancing: fc.record({
    method: fc.constantFrom("round_robin", "least_conn", "ip_hash"),
  }),
  tuning: fc.record({ timeout: fc.integer({ min: 1, max: 300 }) }),
  enabled: fc.boolean(),
  tags: fc.array(safeString, { maxLength: 5 }),
  revision: fc.integer({ min: 1, max: 1000 }),
});

const agentArb = fc.record({
  id: fc.uuid(),
  name: fc.string({ minLength: 1, maxLength: 50 }),
  agent_url: fc.webUrl(),
  agent_token: nonEmptyString,
  version: nonEmptyString,
  tags: fc.array(safeString, { maxLength: 5 }),
  capabilities: fc.array(safeString, { maxLength: 5 }),
  mode: fc.constantFrom("pull", "push"),
  desired_revision: fc.integer({ min: 1, max: 1000 }),
  current_revision: fc.integer({ min: 1, max: 1000 }),
  last_apply_revision: fc.integer({ min: 1, max: 1000 }),
  last_apply_status: fc.constantFrom("success", "error"),
  last_apply_message: nonEmptyString,
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
  error: fc.option(nonEmptyString, { nil: null }),
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
  last_error: nonEmptyString,
  material_hash: nonEmptyString,
  agent_reports: fc.constant({}),
  acme_info: fc.constant({}),
  tags: fc.array(safeString, { maxLength: 5 }),
  revision: fc.integer({ min: 1, max: 1000 }),
});

const localStateArb = fc.record({
  desired_revision: fc.integer({ min: 1, max: 1000 }),
  current_revision: fc.integer({ min: 1, max: 1000 }),
  last_apply_revision: fc.integer({ min: 1, max: 1000 }),
  last_apply_status: fc.constantFrom("success", "error"),
  last_apply_message: nonEmptyString,
});

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/**
 * Normalize results for cross-backend comparison.
 *
 * Key semantic differences handled:
 * 1. SQLite adds `agent_id` to rules/l4_rules rows; JSON does not -> strip it.
 * 2. JSON round-trip normalizes types (JSON.stringify -> JSON.parse).
 * 3. Sort arrays by id for stable ordering.
 */
function normalize(obj) {
  // Deep clone via JSON round-trip - normalizes types consistently
  return JSON.parse(JSON.stringify(obj));
}

/** Strip agent_id from rule/l4_rule objects (SQLite adds it, JSON does not). */
function stripAgentId(arr) {
  return arr.map((item) => {
    const { agent_id, ...rest } = item;
    return rest;
  });
}

/** Sort array of objects by id for stable comparison. */
function sortById(arr) {
  return [...arr].sort((a, b) => {
    if (typeof a.id === "string") return a.id.localeCompare(b.id);
    return a.id - b.id;
  });
}

/**
 * Compare two adapter results after normalization.
 * Strips agent_id, normalizes via JSON round-trip, sorts by id.
 */
function assertEquivalent(sqliteResult, jsonResult, opts = {}) {
  let s = normalize(sqliteResult);
  let j = normalize(jsonResult);

  if (opts.stripAgentId) {
    s = stripAgentId(s);
    j = stripAgentId(j);
  }

  if (Array.isArray(s)) {
    s = sortById(s);
    j = sortById(j);
  }

  assert.deepStrictEqual(s, j);
}

/**
 * Compare local agent state results.
 * SQLite returns specific fields only; JSON returns the raw object.
 * Compare only the common fields.
 */
function assertLocalStateEquivalent(sqliteResult, jsonResult) {
  const fields = [
    "desired_revision",
    "current_revision",
    "last_apply_revision",
    "last_apply_status",
    "last_apply_message",
  ];
  const s = {};
  const j = {};
  for (const f of fields) {
    s[f] = sqliteResult[f];
    j[f] = jsonResult[f];
  }
  assert.deepStrictEqual(normalize(s), normalize(j));
}

// ---------------------------------------------------------------------------
// Test suite
// ---------------------------------------------------------------------------

describe("Property 4: Backend compatibility (semantic consistency)", { skip: !canRunSqlite && "Prisma-backed SQLite adapter not available" }, () => {
  let sqliteStorage;
  let jsonStorage;
  let jsonTmpDir;

  beforeEach(() => {
    jsonTmpDir = fs.mkdtempSync(path.join(os.tmpdir(), "compat-json-"));

    sqliteStorage = loadFreshStorage("../storage-sqlite", SQLITE_TARGET);
    jsonStorage = loadFreshStorage("../storage-json", jsonTmpDir);
  });

  afterEach(() => {
    closeQuietly(sqliteStorage);
    closeQuietly(jsonStorage);
    try { fs.rmSync(jsonTmpDir, { recursive: true, force: true }); } catch (_) { /* ignore */ }
  });

  it("agents: save then load returns equivalent results from both backends", () => {
    fc.assert(
      fc.property(
        fc.array(agentArb, { maxLength: 8 }),
        (agents) => {
          const unique = dedupById(agents);

          sqliteStorage.saveRegisteredAgents(unique);
          jsonStorage.saveRegisteredAgents(unique);

          const sqliteResult = sqliteStorage.loadRegisteredAgents();
          const jsonResult = jsonStorage.loadRegisteredAgents();

          assertEquivalent(sqliteResult, jsonResult);
        }
      ),
      { numRuns: NUM_RUNS }
    );
  });

  it("rules: save then load returns equivalent results from both backends", () => {
    const agentId = "compat-test-agent";

    fc.assert(
      fc.property(
        fc.array(ruleArb, { maxLength: 8 }),
        (rules) => {
          const unique = dedupById(rules);

          sqliteStorage.saveRulesForAgent(agentId, unique);
          jsonStorage.saveRulesForAgent(agentId, unique);

          const sqliteResult = sqliteStorage.loadRulesForAgent(agentId);
          const jsonResult = jsonStorage.loadRulesForAgent(agentId);

          // SQLite adds agent_id to each row; JSON does not
          assertEquivalent(sqliteResult, jsonResult, { stripAgentId: true });
        }
      ),
      { numRuns: NUM_RUNS }
    );
  });

  it("L4 rules: save then load returns equivalent results from both backends", () => {
    const agentId = "compat-test-l4";

    fc.assert(
      fc.property(
        fc.array(l4RuleArb, { maxLength: 8 }),
        (rules) => {
          const unique = dedupById(rules);

          sqliteStorage.saveL4RulesForAgent(agentId, unique);
          jsonStorage.saveL4RulesForAgent(agentId, unique);

          const sqliteResult = sqliteStorage.loadL4RulesForAgent(agentId);
          const jsonResult = jsonStorage.loadL4RulesForAgent(agentId);

          // SQLite adds agent_id to each row; JSON does not
          assertEquivalent(sqliteResult, jsonResult, { stripAgentId: true });
        }
      ),
      { numRuns: NUM_RUNS }
    );
  });

  it("managed certificates: save then load returns equivalent results from both backends", () => {
    fc.assert(
      fc.property(
        fc.array(certArb, { maxLength: 8 }),
        (certs) => {
          const unique = dedupById(certs);

          sqliteStorage.saveManagedCertificates(unique);
          jsonStorage.saveManagedCertificates(unique);

          const sqliteResult = sqliteStorage.loadManagedCertificates();
          const jsonResult = jsonStorage.loadManagedCertificates();

          assertEquivalent(sqliteResult, jsonResult);
        }
      ),
      { numRuns: NUM_RUNS }
    );
  });

  it("local agent state: save then load returns equivalent results from both backends", () => {
    fc.assert(
      fc.property(localStateArb, (state) => {
        sqliteStorage.saveLocalAgentState(state);
        jsonStorage.saveLocalAgentState(state);

        const sqliteResult = sqliteStorage.loadLocalAgentState();
        const jsonResult = jsonStorage.loadLocalAgentState();

        assertLocalStateEquivalent(sqliteResult, jsonResult);
      }),
      { numRuns: NUM_RUNS }
    );
  });

  it("operation sequence: mixed saves then loads return equivalent results", () => {
    fc.assert(
      fc.property(
        fc.array(agentArb, { minLength: 1, maxLength: 5 }),
        fc.array(ruleArb, { maxLength: 5 }),
        fc.array(l4RuleArb, { maxLength: 5 }),
        fc.array(certArb, { maxLength: 5 }),
        localStateArb,
        (agents, rules, l4Rules, certs, localState) => {
          const uniqueAgents = dedupById(agents);
          const uniqueRules = dedupById(rules);
          const uniqueL4Rules = dedupById(l4Rules);
          const uniqueCerts = dedupById(certs);
          const agentId = uniqueAgents[0].id;

          // Execute same operation sequence on both adapters
          sqliteStorage.saveRegisteredAgents(uniqueAgents);
          jsonStorage.saveRegisteredAgents(uniqueAgents);

          sqliteStorage.saveRulesForAgent(agentId, uniqueRules);
          jsonStorage.saveRulesForAgent(agentId, uniqueRules);

          sqliteStorage.saveL4RulesForAgent(agentId, uniqueL4Rules);
          jsonStorage.saveL4RulesForAgent(agentId, uniqueL4Rules);

          sqliteStorage.saveManagedCertificates(uniqueCerts);
          jsonStorage.saveManagedCertificates(uniqueCerts);

          sqliteStorage.saveLocalAgentState(localState);
          jsonStorage.saveLocalAgentState(localState);

          // Compare all load results
          assertEquivalent(
            sqliteStorage.loadRegisteredAgents(),
            jsonStorage.loadRegisteredAgents()
          );
          assertEquivalent(
            sqliteStorage.loadRulesForAgent(agentId),
            jsonStorage.loadRulesForAgent(agentId),
            { stripAgentId: true }
          );
          assertEquivalent(
            sqliteStorage.loadL4RulesForAgent(agentId),
            jsonStorage.loadL4RulesForAgent(agentId),
            { stripAgentId: true }
          );
          assertEquivalent(
            sqliteStorage.loadManagedCertificates(),
            jsonStorage.loadManagedCertificates()
          );
          assertLocalStateEquivalent(
            sqliteStorage.loadLocalAgentState(),
            jsonStorage.loadLocalAgentState()
          );
        }
      ),
      { numRuns: NUM_RUNS }
    );
  });
});
