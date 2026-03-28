"use strict";

/**
 * Property 2: Agent data isolation
 *
 * For any two different agentIds A1 ≠ A2, performing saveRulesForAgent,
 * saveL4RulesForAgent, or deleteRulesForAgent on A1 does not affect
 * loadRulesForAgent(A2) or loadL4RulesForAgent(A2).
 *
 * **Validates: Requirements 6.1, 6.2, 6.3**
 */

const { describe, it, beforeEach, afterEach } = require("node:test");
const assert = require("node:assert/strict");
const fc = require("fast-check");
const fs = require("fs");
const os = require("os");
const path = require("path");

// Guard: better-sqlite3 requires native compilation; skip gracefully if unavailable.
let canRunSqlite = true;
try {
  const Database = require("better-sqlite3");
  const testDb = new Database(":memory:");
  testDb.close();
} catch (_) {
  canRunSqlite = false;
}

// ---------------------------------------------------------------------------
// Arbitraries
// ---------------------------------------------------------------------------

const safeString = fc.string({ maxLength: 50 }).map((s) => s.replace(/\0/g, ""));
const sqliteTarget = ":memory:";

const ruleArb = fc.record({
  id: fc.integer({ min: 1, max: 10000 }),
  frontend_url: fc.webUrl(),
  backend_url: fc.webUrl(),
  enabled: fc.boolean(),
  tags: fc.array(safeString, { maxLength: 5 }),
  proxy_redirect: fc.boolean(),
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

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Deduplicate by id (last wins) so the PRIMARY KEY constraint is met. */
function dedup(arr) {
  const map = new Map();
  for (const item of arr) map.set(item.id, item);
  return [...map.values()];
}

// ---------------------------------------------------------------------------
// Test suite
// ---------------------------------------------------------------------------

describe("Property 2: Agent data isolation", { skip: !canRunSqlite && "better-sqlite3 native bindings not available" }, () => {
  let storage;

  beforeEach(() => {
    // Re-require storage-sqlite fresh for each test to reset module-level db
    const modPath = require.resolve("../storage-sqlite");
    delete require.cache[modPath];
    storage = require("../storage-sqlite");
    storage.init(sqliteTarget);
  });

  afterEach(() => {
    try { storage.close(); } catch (_) { /* ignore */ }
  });

  it("saveRulesForAgent(A1) does not affect loadRulesForAgent(A2)", () => {
    fc.assert(
      fc.property(
        fc.array(ruleArb, { maxLength: 10 }),
        fc.array(ruleArb, { maxLength: 10 }),
        (rules1, rules2) => {
          const a1 = "agent-a";
          const a2 = "agent-b";
          const unique1 = dedup(rules1);
          const unique2 = dedup(rules2);

          // Save rules for A2 first
          storage.saveRulesForAgent(a2, unique2);
          const a2Before = storage.loadRulesForAgent(a2);

          // Save rules for A1 — should not affect A2
          storage.saveRulesForAgent(a1, unique1);
          const a2After = storage.loadRulesForAgent(a1);
          const a2Check = storage.loadRulesForAgent(a2);

          assert.deepStrictEqual(a2Check, a2Before);
        }
      ),
      { numRuns: 50 }
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
          const unique1 = dedup(rules1);
          const unique2 = dedup(rules2);

          // Save L4 rules for A2 first
          storage.saveL4RulesForAgent(a2, unique2);
          const a2Before = storage.loadL4RulesForAgent(a2);

          // Save L4 rules for A1 — should not affect A2
          storage.saveL4RulesForAgent(a1, unique1);
          const a2Check = storage.loadL4RulesForAgent(a2);

          assert.deepStrictEqual(a2Check, a2Before);
        }
      ),
      { numRuns: 50 }
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
          const unique1 = dedup(rules1);
          const unique2 = dedup(rules2);

          // Save rules for both agents
          storage.saveRulesForAgent(a1, unique1);
          storage.saveRulesForAgent(a2, unique2);
          const a2Before = storage.loadRulesForAgent(a2);

          // Delete A1's rules — should not affect A2
          storage.deleteRulesForAgent(a1);
          const a2Check = storage.loadRulesForAgent(a2);

          assert.deepStrictEqual(a2Check, a2Before);

          // Verify A1 is actually empty
          const a1Check = storage.loadRulesForAgent(a1);
          assert.deepStrictEqual(a1Check, []);
        }
      ),
      { numRuns: 50 }
    );
  });
});
