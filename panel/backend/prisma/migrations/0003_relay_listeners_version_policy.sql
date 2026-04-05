ALTER TABLE agents ADD COLUMN desired_version TEXT DEFAULT '';
ALTER TABLE local_agent_state ADD COLUMN desired_version TEXT DEFAULT '';
CREATE TABLE IF NOT EXISTS relay_listeners (
  id INTEGER PRIMARY KEY,
  agent_id TEXT NOT NULL,
  name TEXT DEFAULT '',
  listen_host TEXT DEFAULT '0.0.0.0',
  listen_port INTEGER NOT NULL,
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
CREATE TABLE IF NOT EXISTS version_policy (
  id TEXT PRIMARY KEY,
  channel TEXT DEFAULT 'stable',
  desired_version TEXT DEFAULT '',
  packages TEXT DEFAULT '[]',
  tags TEXT DEFAULT '[]'
);
UPDATE agents SET desired_version = '' WHERE desired_version IS NULL;
UPDATE local_agent_state SET desired_version = '' WHERE desired_version IS NULL;
