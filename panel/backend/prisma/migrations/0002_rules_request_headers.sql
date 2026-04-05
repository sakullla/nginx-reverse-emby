ALTER TABLE rules ADD COLUMN pass_proxy_headers INTEGER DEFAULT 1;
ALTER TABLE rules ADD COLUMN user_agent TEXT DEFAULT '';
ALTER TABLE rules ADD COLUMN custom_headers TEXT DEFAULT '[]';
UPDATE rules SET pass_proxy_headers = 1 WHERE pass_proxy_headers IS NULL;
UPDATE rules SET user_agent = '' WHERE user_agent IS NULL;
UPDATE rules SET custom_headers = '[]' WHERE custom_headers IS NULL;
