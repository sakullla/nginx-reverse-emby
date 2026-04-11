ALTER TABLE rules ADD COLUMN pass_proxy_headers INTEGER DEFAULT 1;
ALTER TABLE rules ADD COLUMN user_agent TEXT DEFAULT '';
ALTER TABLE rules ADD COLUMN custom_headers TEXT DEFAULT '[]';
UPDATE rules SET pass_proxy_headers = 1 WHERE pass_proxy_headers IS NULL;
UPDATE rules SET user_agent = '' WHERE user_agent IS NULL;
UPDATE rules SET custom_headers = '[]' WHERE custom_headers IS NULL;

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

ALTER TABLE agents ADD COLUMN platform TEXT DEFAULT '';

ALTER TABLE rules ADD COLUMN relay_chain TEXT DEFAULT '[]';
ALTER TABLE l4_rules ADD COLUMN relay_chain TEXT DEFAULT '[]';
ALTER TABLE managed_certificates ADD COLUMN usage TEXT DEFAULT 'https';
ALTER TABLE managed_certificates ADD COLUMN certificate_type TEXT DEFAULT 'acme';
ALTER TABLE managed_certificates ADD COLUMN self_signed INTEGER DEFAULT 0;
UPDATE rules SET relay_chain = '[]' WHERE relay_chain IS NULL OR trim(relay_chain) = '';
UPDATE l4_rules SET relay_chain = '[]' WHERE relay_chain IS NULL OR trim(relay_chain) = '';
UPDATE managed_certificates SET usage = 'https' WHERE usage IS NULL OR trim(usage) = '';
UPDATE managed_certificates SET certificate_type = 'acme' WHERE certificate_type IS NULL OR trim(certificate_type) = '';
UPDATE managed_certificates SET self_signed = 0 WHERE self_signed IS NULL;

ALTER TABLE relay_listeners ADD COLUMN bind_hosts TEXT DEFAULT '[]';
ALTER TABLE relay_listeners ADD COLUMN public_host TEXT;
ALTER TABLE relay_listeners ADD COLUMN public_port INTEGER;
UPDATE relay_listeners
SET bind_hosts = json_array(COALESCE(NULLIF(trim(listen_host), ''), '0.0.0.0'))
WHERE bind_hosts IS NULL OR trim(bind_hosts) = '' OR trim(bind_hosts) = '[]';
UPDATE relay_listeners
SET public_host = COALESCE(NULLIF(trim(public_host), ''), json_extract(bind_hosts, '$[0]'), COALESCE(NULLIF(trim(listen_host), ''), '0.0.0.0'))
WHERE public_host IS NULL OR trim(public_host) = '';
UPDATE relay_listeners
SET public_port = COALESCE(public_port, listen_port)
WHERE public_port IS NULL OR public_port <= 0;
