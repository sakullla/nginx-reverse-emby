"use strict";

const { describe, it, beforeEach, afterEach } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { normalizeRelayListenerPayload } = require("../relay-listener-normalize");
const { loadFreshStorage, closeQuietly, SQLITE_TARGET, canRunSqlite } = require("./helpers");

describe("relay listener normalization", () => {
  it("normalizes a valid relay listener payload", () => {
    const listener = normalizeRelayListenerPayload({
      id: 1,
      agent_id: "local",
      name: "relay-a",
      listen_host: "0.0.0.0",
      listen_port: 18443,
      certificate_id: 12,
      tls_mode: "pin_or_ca",
      pin_set: [{ type: "spki_sha256", value: "abc" }],
    });

    assert.strictEqual(listener.listen_port, 18443);
    assert.deepStrictEqual(listener.bind_hosts, ["0.0.0.0"]);
    assert.strictEqual(listener.public_host, "0.0.0.0");
    assert.strictEqual(listener.public_port, 18443);
    assert.strictEqual(listener.listen_host, "0.0.0.0");
    assert.strictEqual(listener.enabled, true);
    assert.deepStrictEqual(listener.trusted_ca_certificate_ids, []);
    assert.deepStrictEqual(listener.tags, []);
    assert.strictEqual(listener.revision, 0);
  });

  it("expands legacy listen_host/listen_port into bind/public fields", () => {
    const listener = normalizeRelayListenerPayload({
      id: 3,
      agent_id: "local",
      name: "relay-legacy",
      listen_host: "127.0.0.1",
      listen_port: 1443,
      certificate_id: 12,
      pin_set: [{ type: "spki_sha256", value: "abc" }],
    });

    assert.deepStrictEqual(listener.bind_hosts, ["127.0.0.1"]);
    assert.strictEqual(listener.listen_host, "127.0.0.1");
    assert.strictEqual(listener.public_host, "127.0.0.1");
    assert.strictEqual(listener.public_port, 1443);
  });

  it("rejects listeners without an id", () => {
    assert.throws(
      () => normalizeRelayListenerPayload({
        agent_id: "local",
        name: "relay-a",
        listen_host: "0.0.0.0",
        listen_port: 18443,
        certificate_id: 12,
        pin_set: [{ type: "spki_sha256", value: "abc" }],
      }),
      /id is required|id/i,
    );
  });

  it("rejects enabled listeners without a certificate_id", () => {
    assert.throws(
      () => normalizeRelayListenerPayload({
        id: 2,
        agent_id: "local",
        name: "relay-a",
        listen_host: "0.0.0.0",
        listen_port: 18443,
        pin_set: [{ type: "spki_sha256", value: "abc" }],
      }),
      /certificate_id is required when relay listener is enabled/i,
    );
  });

  it("rejects listeners when both pin_set and trusted_ca_certificate_ids are empty", () => {
    assert.throws(
      () => normalizeRelayListenerPayload({
        id: 2,
        agent_id: "local",
        name: "relay-a",
        listen_host: "0.0.0.0",
        listen_port: 18443,
        certificate_id: 12,
        pin_set: [],
        trusted_ca_certificate_ids: [],
      }),
      /pin_set.*trusted_ca_certificate_ids/i,
    );
  });
});

describe("relay listener storage", () => {
  let jsonStorage;
  let jsonTmpDir;

  beforeEach(() => {
    jsonTmpDir = fs.mkdtempSync(path.join(os.tmpdir(), "relay-json-"));
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

  it("round-trips relay listeners in the JSON backend", () => {
    const listeners = [
      normalizeRelayListenerPayload({
        id: 7,
        agent_id: "agent-json",
        name: "relay-a",
        bind_hosts: ["0.0.0.0", "::"],
        listen_host: "0.0.0.0",
        listen_port: 18443,
        public_host: "relay.example.com",
        public_port: 443,
        certificate_id: 12,
        tls_mode: "pin_or_ca",
        pin_set: [{ type: "spki_sha256", value: "abc" }],
        tags: ["prod"],
        revision: 11,
      }),
    ];

    jsonStorage.saveRelayListenersForAgent("agent-json", listeners);

    assert.deepStrictEqual(
      jsonStorage.loadRelayListenersForAgent("agent-json"),
      listeners,
    );
  });

  it("round-trips relay listeners in the SQLite backend", { skip: !canRunSqlite && "Prisma-backed SQLite adapter not available" }, () => {
    const sqliteStorage = loadFreshStorage("../storage-sqlite", SQLITE_TARGET);

    try {
      const listeners = [
        normalizeRelayListenerPayload({
        id: 8,
        agent_id: "agent-sqlite",
        name: "relay-b",
        listen_host: "127.0.0.1",
        listen_port: 9443,
        certificate_id: 22,
        tls_mode: "pin_or_ca",
        trusted_ca_certificate_ids: [22],
        allow_self_signed: false,
        tags: ["edge"],
          revision: 4,
        }),
      ];

      sqliteStorage.saveRelayListenersForAgent("agent-sqlite", listeners);

      assert.deepStrictEqual(
        sqliteStorage.loadRelayListenersForAgent("agent-sqlite"),
        listeners,
      );
    } finally {
      closeQuietly(sqliteStorage);
    }
  });

  it("binds saved relay listeners to the method agentId in the JSON backend", () => {
    jsonStorage.saveRelayListenersForAgent("agent-json", [
      normalizeRelayListenerPayload({
        id: 17,
        agent_id: "wrong-agent",
        name: "relay-a",
        listen_host: "0.0.0.0",
        listen_port: 18443,
        certificate_id: 12,
        pin_set: [{ type: "spki_sha256", value: "abc" }],
      }),
    ]);

    assert.deepStrictEqual(
      jsonStorage.loadRelayListenersForAgent("agent-json"),
      [
        {
          id: 17,
          agent_id: "agent-json",
          name: "relay-a",
          bind_hosts: ["0.0.0.0"],
          listen_host: "0.0.0.0",
          listen_port: 18443,
          public_host: "0.0.0.0",
          public_port: 18443,
          enabled: true,
          certificate_id: 12,
          tls_mode: "pin_or_ca",
          pin_set: [{ type: "spki_sha256", value: "abc" }],
          trusted_ca_certificate_ids: [],
          allow_self_signed: false,
          tags: [],
          revision: 0,
        },
      ],
    );
  });

  it("enforces globally unique relay listener ids across agents in the JSON backend", () => {
    jsonStorage.saveRelayListenersForAgent("agent-a", [
      normalizeRelayListenerPayload({
        id: 99,
        agent_id: "agent-a",
        name: "relay-a",
        listen_host: "0.0.0.0",
        listen_port: 18443,
        certificate_id: 12,
        pin_set: [{ type: "spki_sha256", value: "abc" }],
      }),
    ]);

    assert.throws(
      () => jsonStorage.saveRelayListenersForAgent("agent-b", [
        normalizeRelayListenerPayload({
          id: 99,
          agent_id: "agent-b",
          name: "relay-b",
          listen_host: "127.0.0.1",
          listen_port: 19443,
          certificate_id: 13,
          trusted_ca_certificate_ids: [12],
        }),
      ]),
      /relay listener id.*99/i,
    );
  });

  it("enforces globally unique relay listener ids across agents in the SQLite backend", { skip: !canRunSqlite && "Prisma-backed SQLite adapter not available" }, () => {
    const sqliteStorage = loadFreshStorage("../storage-sqlite", SQLITE_TARGET);

    try {
      sqliteStorage.saveRelayListenersForAgent("agent-a", [
        normalizeRelayListenerPayload({
          id: 101,
          agent_id: "agent-a",
          name: "relay-a",
          listen_host: "0.0.0.0",
          listen_port: 18443,
          certificate_id: 12,
          pin_set: [{ type: "spki_sha256", value: "abc" }],
        }),
      ]);

      assert.throws(
        () => sqliteStorage.saveRelayListenersForAgent("agent-b", [
          normalizeRelayListenerPayload({
          id: 101,
          agent_id: "agent-b",
          name: "relay-b",
          listen_host: "127.0.0.1",
          listen_port: 19443,
          certificate_id: 13,
          trusted_ca_certificate_ids: [12],
        }),
      ]),
        /unique|constraint|relay listener id/i,
      );
    } finally {
      closeQuietly(sqliteStorage);
    }
  });

  it("rejects invalid relay listener payloads in the JSON backend without overwriting stored listeners", () => {
    const validListeners = [
      normalizeRelayListenerPayload({
        id: 7,
        agent_id: "agent-json",
        name: "relay-a",
        listen_host: "0.0.0.0",
        listen_port: 18443,
        certificate_id: 12,
        pin_set: [{ type: "spki_sha256", value: "abc" }],
      }),
    ];

    jsonStorage.saveRelayListenersForAgent("agent-json", validListeners);

    assert.throws(
      () => jsonStorage.saveRelayListenersForAgent("agent-json", [{
        id: 8,
        agent_id: "agent-json",
        name: "relay-b",
        listen_host: "0.0.0.0",
        listen_port: 19443,
        certificate_id: 13,
        pin_set: [],
        trusted_ca_certificate_ids: [],
      }]),
      /pin_set.*trusted_ca_certificate_ids/i,
    );

    assert.deepStrictEqual(
      jsonStorage.loadRelayListenersForAgent("agent-json"),
      validListeners,
    );
  });

  it("rejects relay listeners without ids in the JSON backend without overwriting stored listeners", () => {
    const validListeners = [
      normalizeRelayListenerPayload({
        id: 7,
        agent_id: "agent-json",
        name: "relay-a",
        listen_host: "0.0.0.0",
        listen_port: 18443,
        certificate_id: 12,
        pin_set: [{ type: "spki_sha256", value: "abc" }],
      }),
    ];

    jsonStorage.saveRelayListenersForAgent("agent-json", validListeners);

    assert.throws(
      () => jsonStorage.saveRelayListenersForAgent("agent-json", [{
        agent_id: "agent-json",
        name: "relay-b",
        listen_host: "0.0.0.0",
        listen_port: 19443,
        certificate_id: 13,
        pin_set: [{ type: "spki_sha256", value: "abc" }],
      }]),
      /id is required|id/i,
    );

    assert.deepStrictEqual(
      jsonStorage.loadRelayListenersForAgent("agent-json"),
      validListeners,
    );
  });
});
