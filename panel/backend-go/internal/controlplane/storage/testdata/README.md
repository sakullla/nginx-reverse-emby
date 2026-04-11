This directory contains Go-owned storage compatibility fixtures.

Canonical compatibility baseline after cutover:
- `schema_base.sql`
- `schema_migrations.sql`

These two files are derived from the last Node-compatible SQLite schema and ordered SQL migrations. They are intentionally checked in so Go storage tests do not depend on `panel/backend` sources.

Maintenance contract:
- Treat `schema_base.sql` and `schema_migrations.sql` as canonical fixtures for compatibility tests in this package.
- Update them intentionally whenever storage schema compatibility behavior changes.
- Keep fixture updates in the same change as the related schema/storage change so drift is explicit in review.

Optional local-only fixture data for manual cutover verification may also live here (for example copied `panel.db` or `managed_certificates/` data), but do not commit secrets, tokens, private keys, or live production data.
