CREATE TABLE IF NOT EXISTS agents (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  agent_url TEXT DEFAULT '',
  agent_token TEXT DEFAULT '',
  version TEXT DEFAULT '',
  platform TEXT DEFAULT '',
  desired_version TEXT DEFAULT '',
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
  backends TEXT DEFAULT '[]',
  load_balancing TEXT DEFAULT '{}',
  enabled INTEGER DEFAULT 1,
  tags TEXT DEFAULT '[]',
  proxy_redirect INTEGER DEFAULT 1,
  relay_chain TEXT DEFAULT '[]',
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
  relay_chain TEXT DEFAULT '[]',
  enabled INTEGER DEFAULT 1,
  tags TEXT DEFAULT '[]',
  revision INTEGER DEFAULT 0,
  PRIMARY KEY (agent_id, id)
);

CREATE INDEX IF NOT EXISTS idx_l4_rules_agent ON l4_rules(agent_id);

CREATE TABLE IF NOT EXISTS relay_listeners (
  id INTEGER PRIMARY KEY,
  agent_id TEXT NOT NULL,
  name TEXT DEFAULT '',
  bind_hosts TEXT DEFAULT '[]',
  listen_host TEXT DEFAULT '0.0.0.0',
  listen_port INTEGER NOT NULL,
  public_host TEXT,
  public_port INTEGER,
  enabled INTEGER DEFAULT 1,
  certificate_id INTEGER,
  tls_mode TEXT DEFAULT 'pin_or_ca',
  pin_set TEXT DEFAULT '[]',
  trusted_ca_certificate_ids TEXT DEFAULT '[]',
  allow_self_signed INTEGER DEFAULT 0,
  tags TEXT DEFAULT '[]',
  revision INTEGER DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_relay_listeners_agent ON relay_listeners(agent_id);

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
  usage TEXT DEFAULT 'https',
  certificate_type TEXT DEFAULT 'acme',
  self_signed INTEGER DEFAULT 0,
  tags TEXT DEFAULT '[]',
  revision INTEGER DEFAULT 0
);

CREATE TABLE IF NOT EXISTS local_agent_state (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  desired_revision INTEGER DEFAULT 0,
  current_revision INTEGER DEFAULT 0,
  last_apply_revision INTEGER DEFAULT 0,
  last_apply_status TEXT DEFAULT 'success',
  last_apply_message TEXT DEFAULT '',
  desired_version TEXT DEFAULT ''
);

CREATE TABLE IF NOT EXISTS version_policy (
  id TEXT PRIMARY KEY,
  channel TEXT DEFAULT 'stable',
  desired_version TEXT DEFAULT '',
  packages TEXT DEFAULT '[]',
  tags TEXT DEFAULT '[]'
);

CREATE TABLE IF NOT EXISTS meta (
  key TEXT PRIMARY KEY,
  value TEXT
);
