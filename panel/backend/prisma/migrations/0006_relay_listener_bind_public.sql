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
