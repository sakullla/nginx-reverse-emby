"use strict";

const { describe, it } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { pathToFileURL } = require("node:url");
const { PrismaClient } = require("@prisma/client");
const { PrismaLibSql } = require("@prisma/adapter-libsql");
const { loadFreshStorage, closeQuietly } = require("./helpers");

const BACKEND_ROOT = path.resolve(__dirname, "..");
const REPO_ROOT = path.resolve(BACKEND_ROOT, "..", "..");
const MIGRATIONS_DIR = path.join(BACKEND_ROOT, "prisma", "migrations");
const CORE_FILE = path.join(BACKEND_ROOT, "storage-prisma-core.js");
const DOCKERFILE = path.join(REPO_ROOT, "Dockerfile");

async function withPrismaClient(databasePath, fn) {
  const adapter = new PrismaLibSql({ url: pathToFileURL(databasePath).href });
  const client = new PrismaClient({ adapter });
  try {
    await fn(client);
  } finally {
    await client.$disconnect();
  }
}

function cleanupDirWithRetries(dirPath) {
  for (let attempt = 0; attempt < 20; attempt += 1) {
    try {
      fs.rmSync(dirPath, { recursive: true, force: true });
      return;
    } catch (error) {
      if (attempt === 19) {
        return;
      }
      Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 50);
    }
  }
}

describe("Prisma SQL migration flow", () => {
  it("stores schema migrations as versioned SQL files", () => {
    const migrationFiles = fs.readdirSync(MIGRATIONS_DIR)
      .filter((file) => /^\d+_.+\.sql$/i.test(file))
      .sort();

    assert.ok(migrationFiles.length > 0, "expected at least one versioned SQL migration file");
    assert.ok(
      migrationFiles.some((file) => /^0002_.+\.sql$/i.test(file)),
      "expected a schema version 2 migration file for request-header columns",
    );
    assert.ok(
      migrationFiles.some((file) => /^0003_.+\.sql$/i.test(file)),
      "expected a schema version 3 migration file for relay listeners and version policy",
    );
  });

  it("copies Prisma runtime migration files into the production image", () => {
    const source = fs.readFileSync(DOCKERFILE, "utf8");

    assert.match(
      source,
      /^COPY\s+panel\/backend\/prisma\/\s+\/opt\/nginx-reverse-emby\/panel\/backend\/prisma\/$/m,
      "expected the runtime image to copy panel/backend/prisma/ for storage-prisma-core.js migrations",
    );
  });

  it("does not embed request-header ALTER TABLE statements inline in storage-prisma-core.js", () => {
    const source = fs.readFileSync(CORE_FILE, "utf8");
    assert.doesNotMatch(source, /ALTER TABLE\s+rules\s+ADD\s+COLUMN\s+pass_proxy_headers/i);
    assert.doesNotMatch(source, /ALTER TABLE\s+rules\s+ADD\s+COLUMN\s+user_agent/i);
    assert.doesNotMatch(source, /ALTER TABLE\s+rules\s+ADD\s+COLUMN\s+custom_headers/i);
  });

  it("migrates legacy schema_version=1 rules tables to schema version 4", async () => {
    const tempDir = fs.mkdtempSync(path.join(os.tmpdir(), "panel-prisma-migration-"));
    const databasePath = path.join(tempDir, "panel.db");

    try {
      await withPrismaClient(databasePath, async (client) => {
        await client.$executeRawUnsafe(`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT)`);
        await client.$executeRawUnsafe(`INSERT INTO meta (key, value) VALUES ('schema_version', '1')`);
        await client.$executeRawUnsafe(`
          CREATE TABLE rules (
            id INTEGER NOT NULL,
            agent_id TEXT NOT NULL,
            frontend_url TEXT NOT NULL,
            backend_url TEXT NOT NULL,
            enabled INTEGER DEFAULT 1,
            tags TEXT DEFAULT '[]',
            proxy_redirect INTEGER DEFAULT 1,
            revision INTEGER DEFAULT 0,
            PRIMARY KEY (agent_id, id)
          )
        `);
        await client.$executeRawUnsafe(`
          INSERT INTO rules (id, agent_id, frontend_url, backend_url, enabled, tags, proxy_redirect, revision)
          VALUES (7, 'legacy-agent', 'https://frontend.example.com', 'http://backend.internal:8096', 1, '[]', 1, 42)
        `);
      });

      const storage = loadFreshStorage("../storage-sqlite");
      try {
        storage.init(tempDir);
        const rules = storage.loadRulesForAgent("legacy-agent");
        assert.equal(rules.length, 1);
        assert.equal(rules[0].pass_proxy_headers, true);
        assert.equal(rules[0].user_agent, "");
        assert.deepEqual(rules[0].custom_headers, []);
      } finally {
        closeQuietly(storage);
      }

      await withPrismaClient(databasePath, async (client) => {
        const schemaVersionRows = await client.$queryRawUnsafe(`
          SELECT value FROM meta WHERE key = 'schema_version'
        `);
        assert.equal(schemaVersionRows[0].value, "4");

        const columns = await client.$queryRawUnsafe(`PRAGMA table_info('rules')`);
        const names = new Set(columns.map((column) => String(column.name || "")));
        assert.ok(names.has("pass_proxy_headers"));
        assert.ok(names.has("user_agent"));
        assert.ok(names.has("custom_headers"));

        const agentColumns = await client.$queryRawUnsafe(`PRAGMA table_info('agents')`);
        const agentColumnNames = new Set(agentColumns.map((column) => String(column.name || "")));
        assert.ok(agentColumnNames.has("desired_version"));
        assert.ok(agentColumnNames.has("platform"));

        const localStateColumns = await client.$queryRawUnsafe(`PRAGMA table_info('local_agent_state')`);
        const localStateColumnNames = new Set(localStateColumns.map((column) => String(column.name || "")));
        assert.ok(localStateColumnNames.has("desired_version"));

        const relayListenerTables = await client.$queryRawUnsafe(`
          SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'relay_listeners'
        `);
        assert.equal(relayListenerTables.length, 1);

        const relayListenerColumns = await client.$queryRawUnsafe(`PRAGMA table_info('relay_listeners')`);
        const relayIdColumn = relayListenerColumns.find((column) => String(column.name || "") === "id");
        assert.equal(Number(relayIdColumn.pk), 1);

        const versionPolicyTables = await client.$queryRawUnsafe(`
          SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'version_policy'
        `);
        assert.equal(versionPolicyTables.length, 1);
      });
    } finally {
      cleanupDirWithRetries(tempDir);
    }
  });
});
