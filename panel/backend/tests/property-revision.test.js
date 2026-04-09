"use strict";

/**
 * Property 5: Global revision number monotonic increase
 *
 * For any persisted state, getNextGlobalRevision() must be strictly greater
 * than the maximum revision already present in agents, local agent state, and
 * managed certificates, and must always be at least 1.
 */

const { describe, it, beforeEach, afterEach } = require("node:test");
const assert = require("node:assert/strict");
const fc = require("fast-check");
const {
  SQLITE_TARGET,
  canRunSqlite,
  nonEmptyString,
  loadFreshStorage,
  closeQuietly,
  dedupById,
  getNumRuns,
} = require("./helpers");

const NUM_RUNS = getNumRuns("revision", 30);

const agentWithRevArb = fc.record({
  id: fc.uuid(),
  name: nonEmptyString,
  version: nonEmptyString,
  platform: nonEmptyString,
  desired_version: nonEmptyString,
  desired_revision: fc.integer({ min: 0, max: 10000 }),
  current_revision: fc.integer({ min: 0, max: 10000 }),
  last_apply_revision: fc.integer({ min: 0, max: 10000 }),
});

const localStateWithRevArb = fc.record({
  desired_revision: fc.integer({ min: 0, max: 10000 }),
  current_revision: fc.integer({ min: 0, max: 10000 }),
  last_apply_revision: fc.integer({ min: 0, max: 10000 }),
  last_apply_status: fc.constantFrom("success", "error"),
  last_apply_message: fc.string({ maxLength: 100 }).map((s) => s.replace(/\0/g, "")),
});

const certWithRevArb = fc.record({
  id: fc.integer({ min: 1, max: 10000 }),
  domain: fc.domain(),
  enabled: fc.boolean(),
  revision: fc.integer({ min: 0, max: 10000 }),
});

describe("Property 5: Global revision number monotonic increase", { skip: !canRunSqlite && "Prisma-backed SQLite adapter not available" }, () => {
  let storage;

  beforeEach(() => {
    storage = loadFreshStorage("../storage-sqlite", SQLITE_TARGET);
  });

  afterEach(() => {
    closeQuietly(storage);
  });

  it("getNextGlobalRevision() is greater than every persisted revision", () => {
    fc.assert(
      fc.property(
        fc.array(agentWithRevArb, { maxLength: 5 }),
        localStateWithRevArb,
        fc.array(certWithRevArb, { maxLength: 5 }),
        (agents, localState, certs) => {
          const uniqueAgents = dedupById(agents);
          const uniqueCerts = dedupById(certs);

          storage.saveRegisteredAgents(uniqueAgents);
          storage.saveLocalAgentState(localState);
          storage.saveManagedCertificates(uniqueCerts);

          let maxRev = 0;
          for (const agent of uniqueAgents) {
            maxRev = Math.max(
              maxRev,
              agent.desired_revision,
              agent.current_revision,
              agent.last_apply_revision,
            );
          }
          maxRev = Math.max(
            maxRev,
            localState.desired_revision,
            localState.current_revision,
            localState.last_apply_revision,
          );
          for (const cert of uniqueCerts) {
            maxRev = Math.max(maxRev, cert.revision);
          }

          const nextRev = storage.getNextGlobalRevision();
          assert.ok(nextRev > maxRev, `expected ${nextRev} > ${maxRev}`);
          assert.ok(nextRev >= 1, `expected ${nextRev} >= 1`);
        },
      ),
      { numRuns: NUM_RUNS },
    );
  });

  it("with empty tables, getNextGlobalRevision() >= 1", () => {
    const nextRev = storage.getNextGlobalRevision();
    assert.ok(nextRev >= 1, `expected ${nextRev} >= 1`);
  });

  it("after saving known revisions, getNextGlobalRevision() returns max + 1", () => {
    fc.assert(
      fc.property(
        fc.integer({ min: 0, max: 10000 }),
        fc.integer({ min: 0, max: 10000 }),
        fc.integer({ min: 0, max: 10000 }),
        (agentRev, localRev, certRev) => {
          storage.saveRegisteredAgents([
            {
              id: "known-agent",
              name: "test-agent",
              desired_revision: agentRev,
              current_revision: 0,
              last_apply_revision: 0,
            },
          ]);

          storage.saveLocalAgentState({
            desired_revision: localRev,
            current_revision: 0,
            last_apply_revision: 0,
            last_apply_status: "success",
            last_apply_message: "",
          });

          storage.saveManagedCertificates([
            {
              id: 1,
              domain: "example.com",
              enabled: true,
              revision: certRev,
            },
          ]);

          const maxRev = Math.max(agentRev, localRev, certRev);
          assert.strictEqual(storage.getNextGlobalRevision(), maxRev + 1);
        },
      ),
      { numRuns: NUM_RUNS },
    );
  });
});
