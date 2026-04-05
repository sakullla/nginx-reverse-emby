"use strict";

const { describe, it, beforeEach, afterEach } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { normalizeVersionPolicyPayload } = require("../version-policy-normalize");
const { loadFreshStorage, closeQuietly, SQLITE_TARGET, canRunSqlite } = require("./helpers");

describe("version policy normalization", () => {
  it("normalizes a valid version policy payload", () => {
    const policy = normalizeVersionPolicyPayload({
      channel: "stable",
      desired_version: "1.2.3",
      packages: [{ platform: "windows-amd64", url: "https://example.com/agent.zip", sha256: "abc" }],
    });

    assert.strictEqual(policy.desired_version, "1.2.3");
    assert.strictEqual(policy.id, "default");
    assert.deepStrictEqual(policy.tags, []);
  });
});

describe("version policy storage", () => {
  let jsonStorage;
  let jsonTmpDir;

  beforeEach(() => {
    jsonTmpDir = fs.mkdtempSync(path.join(os.tmpdir(), "version-policy-json-"));
    jsonStorage = loadFreshStorage("../storage-json", jsonTmpDir);
  });

  afterEach(() => {
    closeQuietly(jsonStorage);
    try {
      fs.rmSync(jsonTmpDir, { recursive: true, force: true });
    } catch (_) {
      // ignore teardown noise
    }
  });

  it("round-trips version policies in the JSON backend via plural APIs", () => {
    const policies = [
      normalizeVersionPolicyPayload({
        id: "global",
        channel: "stable",
        desired_version: "1.2.3",
        packages: [{ platform: "linux-amd64", url: "https://example.com/linux.tar.gz", sha256: "def" }],
        tags: ["rollout"],
      }),
      normalizeVersionPolicyPayload({
        id: "beta",
        channel: "beta",
        desired_version: "1.3.0-beta.1",
        packages: [{ platform: "windows-amd64", url: "https://example.com/windows.zip", sha256: "abc" }],
      }),
    ];

    jsonStorage.saveVersionPolicies(policies);

    assert.deepStrictEqual(jsonStorage.loadVersionPolicies(), [
      policies[1],
      policies[0],
    ]);
  });

  it("round-trips version policies in the SQLite backend via plural APIs", { skip: !canRunSqlite && "Prisma-backed SQLite adapter not available" }, () => {
    const sqliteStorage = loadFreshStorage("../storage-sqlite", SQLITE_TARGET);

    try {
      const policies = [
        normalizeVersionPolicyPayload({
          channel: "stable",
          desired_version: "2.0.0",
          packages: [{ platform: "darwin-arm64", url: "https://example.com/darwin.zip", sha256: "ghi" }],
        }),
        normalizeVersionPolicyPayload({
          id: "edge",
          channel: "edge",
          desired_version: "2.1.0-dev",
          packages: [{ platform: "linux-amd64", url: "https://example.com/linux-dev.tar.gz", sha256: "xyz" }],
        }),
      ];

      sqliteStorage.saveVersionPolicies(policies);

      assert.deepStrictEqual(sqliteStorage.loadVersionPolicies(), policies);
    } finally {
      closeQuietly(sqliteStorage);
    }
  });

  it("keeps singular version policy helpers backward-compatible with the first stored policy", () => {
    const policies = [
      normalizeVersionPolicyPayload({
        id: "global",
        channel: "stable",
        desired_version: "1.2.3",
        packages: [{ platform: "linux-amd64", url: "https://example.com/linux.tar.gz", sha256: "def" }],
        tags: ["rollout"],
      }),
      normalizeVersionPolicyPayload({
        id: "beta",
        channel: "beta",
        desired_version: "1.3.0-beta.1",
        packages: [{ platform: "windows-amd64", url: "https://example.com/windows.zip", sha256: "abc" }],
      }),
    ];

    jsonStorage.saveVersionPolicies(policies);

    assert.deepStrictEqual(
      jsonStorage.loadVersionPolicy(),
      jsonStorage.loadVersionPolicies()[0],
    );
  });

  it("preserves desired_version fields for agent and local agent state storage", { skip: !canRunSqlite && "Prisma-backed SQLite adapter not available" }, () => {
    const sqliteStorage = loadFreshStorage("../storage-sqlite", SQLITE_TARGET);

    try {
      sqliteStorage.saveRegisteredAgents([{
        id: "agent-1",
        name: "Agent 1",
        desired_version: "3.1.4",
      }]);
      sqliteStorage.saveLocalAgentState({
        desired_revision: 5,
        current_revision: 4,
        last_apply_revision: 4,
        last_apply_status: "success",
        last_apply_message: "",
        desired_version: "3.1.4",
      });

      const agents = sqliteStorage.loadRegisteredAgents();
      const localState = sqliteStorage.loadLocalAgentState();

      assert.strictEqual(agents[0].desired_version, "3.1.4");
      assert.strictEqual(localState.desired_version, "3.1.4");
    } finally {
      closeQuietly(sqliteStorage);
    }
  });

  it("rejects invalid version policy payloads in the JSON backend without overwriting stored policies", () => {
    const validPolicies = [
      normalizeVersionPolicyPayload({
        id: "stable",
        channel: "stable",
        desired_version: "1.2.3",
        packages: [{ platform: "linux-amd64", url: "https://example.com/linux.tar.gz", sha256: "def" }],
      }),
    ];

    jsonStorage.saveVersionPolicies(validPolicies);

    assert.throws(
      () => jsonStorage.saveVersionPolicies([{
        id: "broken",
        channel: "stable",
        desired_version: "",
        packages: [{ platform: "linux-amd64", url: "https://example.com/linux.tar.gz", sha256: "def" }],
      }]),
      /desired_version/i,
    );

    assert.deepStrictEqual(
      jsonStorage.loadVersionPolicies(),
      validPolicies,
    );
  });
});
