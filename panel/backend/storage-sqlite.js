"use strict";

const path = require("path");
const Database = require("better-sqlite3");

let db = null;
const LOCAL_AGENT_ID = process.env.MASTER_LOCAL_AGENT_ID || "local";

const SCHEMA_SQL = `
CREATE TABLE IF NOT EXISTS agents (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  agent_url TEXT DEFAULT '',
  agent_token TEXT DEFAULT '',
  version TEXT DEFAULT '',
  tags TEXT DEFAULT '[]',
  capabilities TEXT DEFAULT '[]',
  mode TEXT DEFAULT 'pull',
  desired_revision INTEGER DEFAULT 0,
  current_revision INTEGER DEFAULT 0,
  last_apply_revision INTEGER DEFAULT 0,
  last_apply_status TEXT,
  last_apply_message TEXT DEFAULT '',
  last_reported_stats TEXT,
  last_seen_at TEXT,
  last_seen_ip TEXT,
  created_at TEXT,
  updated_at TEXT,
  error TEXT,
  is_local INTEGER DEFAULT 0
);

CREATE TABLE IF NOT EXISTS rules (
  id INTEGER NOT NULL,
  agent_id TEXT NOT NULL,
  frontend_url TEXT NOT NULL,
  backend_url TEXT NOT NULL,
  enabled INTEGER DEFAULT 1,
  tags TEXT DEFAULT '[]',
  proxy_redirect INTEGER DEFAULT 1,
  revision INTEGER DEFAULT 0,
  PRIMARY KEY (agent_id, id)
);
CREATE INDEX IF NOT EXISTS idx_rules_agent ON rules(agent_id);

CREATE TABLE IF NOT EXISTS l4_rules (
  id INTEGER NOT NULL,
  agent_id TEXT NOT NULL,
  name TEXT DEFAULT '',
  protocol TEXT DEFAULT 'tcp',
  listen_host TEXT DEFAULT '0.0.0.0',
  listen_port INTEGER NOT NULL,
  upstream_host TEXT DEFAULT '',
  upstream_port INTEGER DEFAULT 0,
  backends TEXT DEFAULT '[]',
  load_balancing TEXT DEFAULT '{}',
  tuning TEXT DEFAULT '{}',
  enabled INTEGER DEFAULT 1,
  tags TEXT DEFAULT '[]',
  revision INTEGER DEFAULT 0,
  PRIMARY KEY (agent_id, id)
);
CREATE INDEX IF NOT EXISTS idx_l4_rules_agent ON l4_rules(agent_id);

CREATE TABLE IF NOT EXISTS managed_certificates (
  id INTEGER PRIMARY KEY,
  domain TEXT NOT NULL,
  enabled INTEGER DEFAULT 1,
  scope TEXT DEFAULT 'domain',
  issuer_mode TEXT DEFAULT 'master_cf_dns',
  target_agent_ids TEXT DEFAULT '[]',
  status TEXT DEFAULT 'pending',
  last_issue_at TEXT,
  last_error TEXT DEFAULT '',
  material_hash TEXT DEFAULT '',
  agent_reports TEXT DEFAULT '{}',
  acme_info TEXT DEFAULT '{}',
  tags TEXT DEFAULT '[]',
  revision INTEGER DEFAULT 0
);

CREATE TABLE IF NOT EXISTS local_agent_state (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  desired_revision INTEGER DEFAULT 0,
  current_revision INTEGER DEFAULT 0,
  last_apply_revision INTEGER DEFAULT 0,
  last_apply_status TEXT DEFAULT 'success',
  last_apply_message TEXT DEFAULT ''
);

CREATE TABLE IF NOT EXISTS meta (
  key TEXT PRIMARY KEY,
  value TEXT
);
`;

function init(dataRoot) {
  const dbPath = dataRoot === ":memory:"
    ? ":memory:"
    : path.join(dataRoot, "panel.db");
  db = new Database(dbPath);
  db.pragma("journal_mode = WAL");
  db.exec(SCHEMA_SQL);
  db.prepare("INSERT OR IGNORE INTO local_agent_state (id) VALUES (1)").run();
  db.prepare("INSERT OR IGNORE INTO meta (key, value) VALUES ('schema_version', '1')").run();
}

function loadRegisteredAgents() {
  const rows = db.prepare("SELECT * FROM agents").all();
  return rows.map((row) => ({
    ...row,
    tags: JSON.parse(row.tags || "[]"),
    capabilities: JSON.parse(row.capabilities || "[]"),
    last_reported_stats: row.last_reported_stats
      ? JSON.parse(row.last_reported_stats)
      : null,
    is_local: row.is_local === 1,
  }));
}

function saveRegisteredAgents(agents) {
  const deleteAll = db.prepare("DELETE FROM agents");
  const insert = db.prepare(
    `INSERT INTO agents (id, name, agent_url, agent_token, version, tags, capabilities, mode,
      desired_revision, current_revision, last_apply_revision,
      last_apply_status, last_apply_message, last_reported_stats,
      last_seen_at, last_seen_ip, created_at, updated_at, error, is_local)
     VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
  );
  const transaction = db.transaction((agentList) => {
    deleteAll.run();
    for (const a of agentList) {
      insert.run(
        a.id, a.name, a.agent_url || "", a.agent_token || "",
        a.version || "", JSON.stringify(a.tags || []),
        JSON.stringify(a.capabilities || []), a.mode || "pull",
        a.desired_revision || 0, a.current_revision || 0,
        a.last_apply_revision || 0, a.last_apply_status || null,
        a.last_apply_message || "",
        a.last_reported_stats != null ? JSON.stringify(a.last_reported_stats) : null,
        a.last_seen_at || null, a.last_seen_ip || null,
        a.created_at || null, a.updated_at || null,
        a.error || null, a.is_local ? 1 : 0
      );
    }
  });
  transaction(agents);
}

function loadRulesForAgent(agentId) {
  const rows = db.prepare("SELECT * FROM rules WHERE agent_id = ? ORDER BY id").all(agentId);
  return rows.map((row) => ({
    ...row,
    tags: JSON.parse(row.tags || "[]"),
    enabled: row.enabled === 1,
    proxy_redirect: row.proxy_redirect === 1,
  }));
}

function saveRulesForAgent(agentId, rules) {
  const deleteAll = db.prepare("DELETE FROM rules WHERE agent_id = ?");
  const insert = db.prepare(
    `INSERT INTO rules (id, agent_id, frontend_url, backend_url, enabled, tags, proxy_redirect, revision)
     VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
  );
  const transaction = db.transaction((agentId, ruleList) => {
    deleteAll.run(agentId);
    for (const r of ruleList) {
      insert.run(
        r.id, agentId, r.frontend_url, r.backend_url,
        r.enabled ? 1 : 0, JSON.stringify(r.tags || []),
        r.proxy_redirect ? 1 : 0, r.revision || 0
      );
    }
  });
  transaction(agentId, rules);
}

function deleteRulesForAgent(agentId) {
  db.prepare("DELETE FROM rules WHERE agent_id = ?").run(agentId);
}

function loadL4RulesForAgent(agentId) {
  const rows = db.prepare("SELECT * FROM l4_rules WHERE agent_id = ? ORDER BY id").all(agentId);
  return rows.map((row) => ({
    ...row,
    backends: JSON.parse(row.backends || "[]"),
    load_balancing: JSON.parse(row.load_balancing || "{}"),
    tuning: JSON.parse(row.tuning || "{}"),
    tags: JSON.parse(row.tags || "[]"),
    enabled: row.enabled === 1,
  }));
}

function saveL4RulesForAgent(agentId, rules) {
  const deleteAll = db.prepare("DELETE FROM l4_rules WHERE agent_id = ?");
  const insert = db.prepare(
    `INSERT INTO l4_rules (id, agent_id, name, protocol, listen_host, listen_port,
      upstream_host, upstream_port, backends, load_balancing, tuning, enabled, tags, revision)
     VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
  );
  const transaction = db.transaction((agentId, ruleList) => {
    deleteAll.run(agentId);
    for (const r of ruleList) {
      insert.run(
        r.id, agentId, r.name || "", r.protocol || "tcp",
        r.listen_host || "0.0.0.0", r.listen_port,
        r.upstream_host || "", r.upstream_port || 0,
        JSON.stringify(r.backends || []),
        JSON.stringify(r.load_balancing || {}),
        JSON.stringify(r.tuning || {}),
        r.enabled ? 1 : 0, JSON.stringify(r.tags || []),
        r.revision || 0
      );
    }
  });
  transaction(agentId, rules);
}

function deleteL4RulesForAgent(agentId) {
  db.prepare("DELETE FROM l4_rules WHERE agent_id = ?").run(agentId);
}

function loadManagedCertificates() {
  const rows = db.prepare("SELECT * FROM managed_certificates").all();
  return rows.map((row) => ({
    ...row,
    target_agent_ids: JSON.parse(row.target_agent_ids || "[]"),
    agent_reports: JSON.parse(row.agent_reports || "{}"),
    acme_info: JSON.parse(row.acme_info || "{}"),
    tags: JSON.parse(row.tags || "[]"),
    enabled: row.enabled === 1,
  }));
}

function saveManagedCertificates(certs) {
  const deleteAll = db.prepare("DELETE FROM managed_certificates");
  const insert = db.prepare(
    `INSERT INTO managed_certificates (id, domain, enabled, scope, issuer_mode,
      target_agent_ids, status, last_issue_at, last_error, material_hash,
      agent_reports, acme_info, tags, revision)
     VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
  );
  const transaction = db.transaction((certList) => {
    deleteAll.run();
    for (const c of certList) {
      insert.run(
        c.id, c.domain, c.enabled ? 1 : 0, c.scope || "domain",
        c.issuer_mode || "master_cf_dns",
        JSON.stringify(c.target_agent_ids || []),
        c.status || "pending", c.last_issue_at || null,
        c.last_error || "", c.material_hash || "",
        JSON.stringify(c.agent_reports || {}),
        JSON.stringify(c.acme_info || {}),
        JSON.stringify(c.tags || []),
        c.revision || 0
      );
    }
  });
  transaction(certs);
}

function loadLocalAgentState() {
  const row = db.prepare("SELECT * FROM local_agent_state WHERE id = 1").get();
  if (!row) {
    return {
      desired_revision: 0,
      current_revision: 0,
      last_apply_revision: 0,
      last_apply_status: "success",
      last_apply_message: "",
    };
  }
  return {
    desired_revision: row.desired_revision || 0,
    current_revision: row.current_revision || 0,
    last_apply_revision: row.last_apply_revision || 0,
    last_apply_status: row.last_apply_status || "success",
    last_apply_message: row.last_apply_message || "",
  };
}

function saveLocalAgentState(state) {
  db.prepare(
    `UPDATE local_agent_state SET
      desired_revision = ?, current_revision = ?, last_apply_revision = ?,
      last_apply_status = ?, last_apply_message = ?
     WHERE id = 1`
  ).run(
    state.desired_revision || 0, state.current_revision || 0,
    state.last_apply_revision || 0, state.last_apply_status || "success",
    state.last_apply_message || ""
  );
}

function getNextGlobalRevision() {
  const agentMax = db.prepare(
    "SELECT MAX(MAX(desired_revision, current_revision, last_apply_revision)) as m FROM agents"
  ).get();
  const localMax = db.prepare(
    "SELECT MAX(desired_revision, current_revision, last_apply_revision) as m FROM local_agent_state WHERE id = 1"
  ).get();
  const certMax = db.prepare(
    "SELECT MAX(revision) as m FROM managed_certificates"
  ).get();
  const maxRev = Math.max(
    agentMax?.m || 0,
    localMax?.m || 0,
    certMax?.m || 0
  );
  return Math.max(maxRev + 1, 1);
}

function migrateFromJson(dataRoot) {
  const migrated = db.prepare("SELECT value FROM meta WHERE key = 'migrated_from_json'").get();
  if (migrated) return false;

  const fs = require("fs");
  const jsonStorage = require("./storage-json");
  const dataPath = (file) => path.join(dataRoot, file);
  const agentRulesDir = dataPath("agent_rules");
  const l4RulesDir = dataPath("l4_agent_rules");

  const hasAgentsFile = fs.existsSync(dataPath("agents.json"));
  const hasProxyRulesFile = fs.existsSync(dataPath("proxy_rules.json"));
  const hasManagedCertsFile = fs.existsSync(dataPath("managed_certificates.json"));
  const hasLocalStateFile = fs.existsSync(dataPath("local_agent_state.json"));
  const agentRuleFiles = fs.existsSync(agentRulesDir)
    ? fs.readdirSync(agentRulesDir).filter((file) => file.endsWith(".json"))
    : [];
  const l4RuleFiles = fs.existsSync(l4RulesDir)
    ? fs.readdirSync(l4RulesDir).filter((file) => file.endsWith(".json"))
    : [];

  const hasOldData =
    hasAgentsFile ||
    hasProxyRulesFile ||
    hasManagedCertsFile ||
    hasLocalStateFile ||
    agentRuleFiles.length > 0 ||
    l4RuleFiles.length > 0;
  if (!hasOldData) return false;

  jsonStorage.init(dataRoot);
  const agentsData = jsonStorage.loadRegisteredAgents();
  const managedCertsData = jsonStorage.loadManagedCertificates();
  const localStateData = jsonStorage.loadLocalAgentState();

  const agentIdsFromJson = Array.isArray(agentsData) ? agentsData.map((agent) => agent.id) : [];
  const agentIdsFromRuleFiles = agentRuleFiles.map((file) => file.replace(/\.json$/, ""));
  const agentIdsFromL4Files = l4RuleFiles.map((file) => file.replace(/\.json$/, ""));
  const allAgentIds = [...new Set([LOCAL_AGENT_ID, ...agentIdsFromJson, ...agentIdsFromRuleFiles, ...agentIdsFromL4Files])];

  const insertAgent = db.prepare(
    `INSERT OR REPLACE INTO agents (id, name, agent_url, agent_token, version, tags, capabilities, mode,
      desired_revision, current_revision, last_apply_revision, last_apply_status, last_apply_message,
      last_reported_stats, last_seen_at, last_seen_ip, created_at, updated_at, error, is_local)
     VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
  );
  const insertRule = db.prepare(
    `INSERT INTO rules (id, agent_id, frontend_url, backend_url, enabled, tags, proxy_redirect, revision)
     VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
  );
  const insertL4Rule = db.prepare(
    `INSERT INTO l4_rules (id, agent_id, name, protocol, listen_host, listen_port,
      upstream_host, upstream_port, backends, load_balancing, tuning, enabled, tags, revision)
     VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
  );
  const insertCert = db.prepare(
    `INSERT INTO managed_certificates (id, domain, enabled, scope, issuer_mode,
      target_agent_ids, status, last_issue_at, last_error, material_hash,
      agent_reports, acme_info, tags, revision)
     VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
  );

  const transaction = db.transaction(() => {
    if (Array.isArray(agentsData)) {
      for (const a of agentsData) {
        insertAgent.run(
          a.id, a.name || "", a.agent_url || "", a.agent_token || "",
          a.version || "", JSON.stringify(a.tags || []),
          JSON.stringify(a.capabilities || []), a.mode || "pull",
          a.desired_revision || 0, a.current_revision || 0,
          a.last_apply_revision || 0, a.last_apply_status || null,
          a.last_apply_message || "",
          a.last_reported_stats != null ? JSON.stringify(a.last_reported_stats) : null,
          a.last_seen_at || null, a.last_seen_ip || null,
          a.created_at || null, a.updated_at || null,
          a.error || null, a.is_local ? 1 : 0
        );
      }
    }

    for (const agentId of allAgentIds) {
      const rules = jsonStorage.loadRulesForAgent(agentId);
      for (const r of rules) {
        insertRule.run(
          r.id, agentId, r.frontend_url || "", r.backend_url || "",
          r.enabled !== false ? 1 : 0, JSON.stringify(r.tags || []),
          r.proxy_redirect !== false ? 1 : 0, r.revision || 0
        );
      }

      const l4Rules = jsonStorage.loadL4RulesForAgent(agentId);
      for (const r of l4Rules) {
        insertL4Rule.run(
          r.id, agentId, r.name || "", r.protocol || "tcp",
          r.listen_host || "0.0.0.0", r.listen_port || 0,
          r.upstream_host || "", r.upstream_port || 0,
          JSON.stringify(r.backends || []),
          JSON.stringify(r.load_balancing || {}),
          JSON.stringify(r.tuning || {}),
          r.enabled !== false ? 1 : 0, JSON.stringify(r.tags || []),
          r.revision || 0
        );
      }
    }

    if (Array.isArray(managedCertsData)) {
      for (const c of managedCertsData) {
        insertCert.run(
          c.id, c.domain || "", c.enabled !== false ? 1 : 0,
          c.scope || "domain", c.issuer_mode || "master_cf_dns",
          JSON.stringify(c.target_agent_ids || []),
          c.status || "pending", c.last_issue_at || null,
          c.last_error || "", c.material_hash || "",
          JSON.stringify(c.agent_reports || {}),
          JSON.stringify(c.acme_info || {}),
          JSON.stringify(c.tags || []),
          c.revision || 0
        );
      }
    }

    if (localStateData && typeof localStateData === "object") {
      db.prepare(
        `UPDATE local_agent_state SET
          desired_revision = ?, current_revision = ?, last_apply_revision = ?,
          last_apply_status = ?, last_apply_message = ?
         WHERE id = 1`
      ).run(
        localStateData.desired_revision || 0,
        localStateData.current_revision || 0,
        localStateData.last_apply_revision || 0,
        localStateData.last_apply_status || "success",
        localStateData.last_apply_message || ""
      );
    }

    db.prepare("INSERT INTO meta (key, value) VALUES ('migrated_from_json', ?)").run(new Date().toISOString());
  });

  transaction();
  return true;
}

function close() {
  if (db && db.open) {
    db.close();
  }
}

module.exports = {
  init,
  loadRegisteredAgents,
  saveRegisteredAgents,
  loadRulesForAgent,
  saveRulesForAgent,
  deleteRulesForAgent,
  loadL4RulesForAgent,
  saveL4RulesForAgent,
  deleteL4RulesForAgent,
  loadManagedCertificates,
  saveManagedCertificates,
  loadLocalAgentState,
  saveLocalAgentState,
  getNextGlobalRevision,
  migrateFromJson,
  close,
};
