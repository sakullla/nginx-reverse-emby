"use strict";

/**
 * Property 5: Global revision number monotonic increase
 *
 * For any database state (containing any agents, local_agent_state,
 * managed_certificates data), getNextGlobalRevision() returns a value
 * strictly greater than the max revision across all tables, and >= 1.
 *
 * **Validates: Requirements 7.1, 7.2, 7.3**
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

const agentWithRevArb = fc.record({
  id: fc.uuid(),
  name: fc.constant("test"),
  desired_revision: fc.integer({ min: 0, max: 10000 }),
  current_revision: fc.integer({ min: 0, max: 10000 }),
  last_apply_revision: fc.integer({ min: 0, max: 10000 }),
});

const localStateWithRevArb = fc.record({
  desired_revision: fc.integer({ min: 0, max: 10000 }),
  current_revision: fc.integer({ min: 0, max: 10000 }),
  last_apply_revision: fc.integer({ min: 0, max: 10000 }),
  last_apply_status: fc.constant("success"),
  last_apply_message: fc.constant(""),
});

const certWithRevArb = fc.record({
  id: fc.integer({ min: 1, max: 10000 }),
  domain: fc.constant("example.com"),
  enabled: fc.constant(true),
  revision: fc.integer({ min: 0, max: 10000 }),
});
const sqliteTarget = ":memory:";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Deduplicate by key (last wins) so PRIMARY KEY constraints are met. */
function dedup(arr, key = "id") {
  const map = new Map();
  for (const item of arr) map.set(item[key], item);
  return [...map.values()];
}

// ---------------------------------------------------------------------------
// Test suite
// ---------------------------------------------------------------------------

describe("Property 5: Global revision number monotonic increase", { skip: !canRunSqlite && "better-sqlite3 native bindings not available" }, () => {
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

  it("getNextGlobalRevision() > max of all revision values across agents, local state, and certs", () => {
    fc.assert(
      fc.property(
        fc.array(agentWithRevArb, { maxLength: 5 }),
        localStateWithRevArb,
        fc.array(certWithRevArb, { maxLength: 5 }),
        (agents, localState, certs) => {
          const uniqueAgents = dedup(agents);
          const uniqueCerts = dedup(certs);

          storage.saveRegisteredAgents(uniqueAgents);
          storage.saveLocalAgentState(localState);
          storage.saveManagedCertificates(uniqueCerts);

          const nextRev = storage.getNextGlobalRevision();

          // Compute expected max revision across all tables
          let maxRev = 0;
          for (const a of uniqueAgents) {
            maxRev = Math.max(maxRev, a.desired_revision, a.current_revision, a.last_apply_revision);
          }
          maxRev = Math.max(maxRev, localState.desired_revision, localState.current_revision, localState.last_apply_revision);
          for (const c of uniqueCerts) {
            maxRev = Math.max(maxRev, c.revision);
          }

          assert.ok(nextRev > maxRev, `expected nextRev (${nextRev}) > maxRev (${maxRev})`);
          assert.ok(nextRev >= 1, `expected nextRev (${nextRev}) >= 1`);
        }
      ),
      { numRuns: 50 }
    );
  });

  it("with empty tables, getNextGlobalRevision() >= 1", () => {
    const nextRev = storage.getNextGlobalRevision();
    assert.ok(nextRev >= 1, `expected nextRev (${nextRev}) >= 1`);
  });

  it("after saving data with known revision values, getNextGlobalRevision() returns max + 1", () => {
    fc.assert(
      fc.property(
        fc.integer({ min: 1, max: 10000 }),
        fc.integer({ min: 1, max: 10000 }),
        fc.integer({ min: 1, max: 10000 }),
        (agentRev, localRev, certRev) => {
          storage.saveRegisteredAgents([{
            id: "known-agent",
            name: "test",
            desired_revision: agentRev,
            current_revision: 0,
            last_apply_revision: 0,
          }]);
          storage.saveLocalAgentState({
            desired_revision: localRev,
            current_revision: 0,
            last_apply_revision: 0,
            last_apply_status: "success",
            last_apply_message: "",
          });
          storage.saveManagedCertificates([{
            id: 1,
            domain: "example.com",
            enabled: true,
            revision: certRev,
          }]);

          const maxRev = Math.max(agentRev, localRev, certRev);
          const nextRev = storage.getNextGlobalRevision();

          assert.strictEqual(nextRev, maxRev + 1, `expected nextRev (${nextRev}) === maxRev + 1 (${maxRev + 1})`);
        }
      ),
      { numRuns: 50 }
    );
  });
});
