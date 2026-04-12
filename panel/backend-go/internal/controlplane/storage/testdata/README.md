This directory is reserved for optional local, non-secret storage verification assets.

Canonical schema/bootstrap behavior for fresh control-plane SQLite databases lives in Go code:
- `internal/controlplane/storage/schema.go` (`BootstrapSQLiteSchema`)
- `internal/controlplane/storage/sqlite_models.go` (GORM model schema)

`BootstrapSQLiteSchema` also performs upgrade-path normalization for a focused set of legacy SQLite fields covered by storage tests.

Storage tests in this package seed databases through GORM/bootstrap helpers and inline legacy setups, and do not depend on checked-in SQL schema fixtures.

Do not commit secrets, tokens, private keys, or live production data.
