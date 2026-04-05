"use strict";

/**
 * Property 2: Agent data isolation
 *
 * For any two different agentIds A1 != A2, performing writes or deletes on A1
 * must not change the persisted data for A2.
 */

const { describe, it, beforeEach, afterEach } = require("node:test");
const assert = require("node:assert/strict");
const fc = require("fast-check");
const {
  SQLITE_TARGET,
  canRunSqlite,
  safeString,
  loadFreshStorage,
  closeQuietly,
  dedupById,
  getNumRuns,
} = require("./helpers");

const NUM_RUNS = getNumRuns("isolation", 30);

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
    { maxLength: 3 },
  ),
  load_balancing: fc.record({
    method: fc.constantFrom("round_robin", "least_conn", "ip_hash"),
  }),
  tuning: fc.record({
    timeout: fc.integer({ min: 0, max: 300 }),
  }),
  enabled: fc.boolean(),
  tags: fc.array(safeString, { maxLength: 5 }),
  revision: fc.integer({ min: 0, max: 1000 }),
});

describe("Property 2: Agent data isolation", { skip: !canRunSqlite && "Prisma-backed SQLite adapter not available" }, () => {
  let storage;

  beforeEach(() => {
    storage = loadFreshStorage("../storage-sqlite", SQLITE_TARGET);
  });

  afterEach(() => {
    closeQuietly(storage);
  });

  it("saveRulesForAgent(A1) does not affect loadRulesForAgent(A2)", () => {
    fc.assert(
      fc.property(
        fc.array(ruleArb, { maxLength: 10 }),
        fc.array(ruleArb, { maxLength: 10 }),
        (rules1, rules2) => {
          const a1 = "agent-a";
          const a2 = "agent-b";
          const unique1 = dedupById(rules1);
          const unique2 = dedupById(rules2);

          storage.saveRulesForAgent(a2, unique2);
          const a2Before = storage.loadRulesForAgent(a2);

          storage.saveRulesForAgent(a1, unique1);

          assert.deepStrictEqual(storage.loadRulesForAgent(a2), a2Before);
        },
      ),
      { numRuns: NUM_RUNS },
    );
  });

  it("saveL4RulesForAgent(A1) does not affect loadL4RulesForAgent(A2)", () => {
    fc.assert(
      fc.property(
        fc.array(l4RuleArb, { maxLength: 10 }),
        fc.array(l4RuleArb, { maxLength: 10 }),
        (rules1, rules2) => {
          const a1 = "agent-a";
          const a2 = "agent-b";
          const unique1 = dedupById(rules1);
          const unique2 = dedupById(rules2);

          storage.saveL4RulesForAgent(a2, unique2);
          const a2Before = storage.loadL4RulesForAgent(a2);

          storage.saveL4RulesForAgent(a1, unique1);

          assert.deepStrictEqual(storage.loadL4RulesForAgent(a2), a2Before);
        },
      ),
      { numRuns: NUM_RUNS },
    );
  });

  it("deleteRulesForAgent(A1) does not affect loadRulesForAgent(A2)", () => {
    fc.assert(
      fc.property(
        fc.array(ruleArb, { maxLength: 10 }),
        fc.array(ruleArb, { maxLength: 10 }),
        (rules1, rules2) => {
          const a1 = "agent-a";
          const a2 = "agent-b";
          const unique1 = dedupById(rules1);
          const unique2 = dedupById(rules2);

          storage.saveRulesForAgent(a1, unique1);
          storage.saveRulesForAgent(a2, unique2);
          const a2Before = storage.loadRulesForAgent(a2);

          storage.deleteRulesForAgent(a1);

          assert.deepStrictEqual(storage.loadRulesForAgent(a2), a2Before);
          assert.deepStrictEqual(storage.loadRulesForAgent(a1), []);
        },
      ),
      { numRuns: NUM_RUNS },
    );
  });

  it("deleteL4RulesForAgent(A1) does not affect loadL4RulesForAgent(A2)", () => {
    fc.assert(
      fc.property(
        fc.array(l4RuleArb, { maxLength: 10 }),
        fc.array(l4RuleArb, { maxLength: 10 }),
        (rules1, rules2) => {
          const a1 = "agent-a";
          const a2 = "agent-b";
          const unique1 = dedupById(rules1);
          const unique2 = dedupById(rules2);

          storage.saveL4RulesForAgent(a1, unique1);
          storage.saveL4RulesForAgent(a2, unique2);
          const a2Before = storage.loadL4RulesForAgent(a2);

          storage.deleteL4RulesForAgent(a1);

          assert.deepStrictEqual(storage.loadL4RulesForAgent(a2), a2Before);
          assert.deepStrictEqual(storage.loadL4RulesForAgent(a1), []);
        },
      ),
      { numRuns: NUM_RUNS },
    );
  });
});
