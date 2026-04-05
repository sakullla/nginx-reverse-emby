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

  it("round-trips version policy in the JSON backend", () => {
    const policy = normalizeVersionPolicyPayload({
      id: "global",
      channel: "stable",
      desired_version: "1.2.3",
      packages: [{ platform: "linux-amd64", url: "https://example.com/linux.tar.gz", sha256: "def" }],
      tags: ["rollout"],
    });

    jsonStorage.saveVersionPolicy(policy);

    assert.deepStrictEqual(jsonStorage.loadVersionPolicy(), policy);
  });

  it("round-trips version policy in the SQLite backend", { skip: !canRunSqlite && "Prisma-backed SQLite adapter not available" }, () => {
    const sqliteStorage = loadFreshStorage("../storage-sqlite", SQLITE_TARGET);

    try {
      const policy = normalizeVersionPolicyPayload({
        channel: "stable",
        desired_version: "2.0.0",
        packages: [{ platform: "darwin-arm64", url: "https://example.com/darwin.zip", sha256: "ghi" }],
      });

      sqliteStorage.saveVersionPolicy(policy);

      assert.deepStrictEqual(sqliteStorage.loadVersionPolicy(), policy);
    } finally {
      closeQuietly(sqliteStorage);
    }
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
});
