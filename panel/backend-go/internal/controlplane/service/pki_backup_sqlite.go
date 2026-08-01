package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/glebarez/sqlite"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var pkiBackupSQLiteHeader = []byte("SQLite format 3\x00")

var requiredPKIBackupTables = []string{
	"pki_authorities",
	"pki_certificates",
	"pki_confirmation_nonces",
	"pki_enrollment_replays",
	"pki_enrollment_tokens",
	"pki_events",
	"pki_identities",
	"pki_instance_lease",
	"pki_lifecycle_jobs",
	"pki_settings",
	"pki_security_snapshot",
}

var requiredPKIBackupColumns = map[string][]string{
	"pki_settings": {
		"id", "pki_domain_id", "ca_lifetime_seconds", "endpoint_lifetime_seconds", "audit_retention_days",
		"security_revision", "pki_epoch", "upgrade_state", "relay_fail_closed", "created_at", "updated_at",
	},
	"pki_authorities": {
		"id", "pki_domain_id", "generation", "status", "certificate_pem", "encrypted_key_ref",
		"fingerprint_sha256", "not_before", "not_after", "retire_deadline", "created_reason",
		"retired_reason", "private_key_destroy_pending_at", "private_key_destroyed_at", "created_at", "updated_at",
	},
	"pki_identities": {
		"id", "pki_domain_id", "kind", "agent_id", "listener_id", "state", "current_certificate_id",
		"revoked_at", "revoked_reason", "created_at", "updated_at",
	},
	"pki_certificates": {
		"id", "serial_hex", "identity_id", "purpose", "authority_id", "ca_generation", "certificate_pem",
		"public_key_fingerprint_sha256", "not_before", "not_after", "status", "active_identity_purpose_key",
		"revoked_at", "revoked_reason", "superseded_by_id", "created_at", "updated_at",
	},
	"pki_enrollment_tokens": {
		"id", "token_digest_sha256", "scope", "bound_agent_id", "expires_at", "consumed_at", "created_by", "created_at",
	},
	"pki_enrollment_replays": {
		"id", "pki_domain_id", "request_key", "request_fingerprint_sha256", "result_json", "created_at",
	},
	"pki_confirmation_nonces": {
		"id", "pki_domain_id", "digest_sha256", "operator_id", "action", "target_id", "expires_at", "consumed_at", "created_at",
	},
	"pki_security_snapshot": {
		"id", "pki_domain_id", "pki_epoch", "security_revision", "snapshot_json", "updated_at",
	},
	"pki_lifecycle_jobs": {
		"id", "pki_domain_id", "target_type", "target_id", "kind", "phase", "state", "attempt", "next_attempt_at",
		"deadline", "last_error", "runtime_json", "operation_id", "idempotency_key", "active_target_key", "lease_owner",
		"lease_deadline", "created_at", "updated_at",
	},
	"pki_events": {
		"id", "pki_domain_id", "type", "occurred_at", "source", "operator_id", "object_type", "object_id",
		"certificate_id", "ca_generation", "result", "reason", "security_revision", "details_json",
	},
	"pki_instance_lease": {
		"id", "pki_domain_id", "instance_id", "lease_term", "lease_deadline", "pki_epoch", "state", "updated_at",
	},
}

type pkiBackupStageOptions struct {
	Sanitize     bool
	ForceVersion *PKISecurityVersion
}

type pkiBackupSQLiteStage struct {
	Snapshot         []byte
	State            storage.PKICanonicalState
	SchemaVersion    int
	SchemaSHA256     string
	EnrollmentTokens int64
}

func stagePKIBackupSQLite(ctx context.Context, snapshot []byte, options pkiBackupStageOptions) (pkiBackupSQLiteStage, error) {
	if len(snapshot) < len(pkiBackupSQLiteHeader) || len(snapshot) > pkiBackupMaxSnapshotSize ||
		!bytes.Equal(snapshot[:len(pkiBackupSQLiteHeader)], pkiBackupSQLiteHeader) {
		return pkiBackupSQLiteStage{}, fmt.Errorf("%w: payload is not a standalone SQLite database", ErrPKIBackupSchema)
	}
	directory, err := os.MkdirTemp("", "nre-pki-backup-stage-")
	if err != nil {
		return pkiBackupSQLiteStage{}, fmt.Errorf("create PKI backup staging directory: %w", err)
	}
	defer os.RemoveAll(directory)
	if err := os.Chmod(directory, 0o700); err != nil {
		return pkiBackupSQLiteStage{}, fmt.Errorf("restrict PKI backup staging directory: %w", err)
	}
	path := filepath.Join(directory, "panel.db")
	if err := os.WriteFile(path, snapshot, 0o600); err != nil {
		return pkiBackupSQLiteStage{}, fmt.Errorf("write staged PKI SQLite snapshot: %w", err)
	}

	db, closeDB, err := openPKIBackupSQLite(path)
	if err != nil {
		return pkiBackupSQLiteStage{}, err
	}
	closed := false
	defer func() {
		if !closed {
			_ = closeDB()
		}
	}()
	if err := validatePKIBackupTables(ctx, db); err != nil {
		return pkiBackupSQLiteStage{}, err
	}
	var removedTokenDigests [][]byte
	if options.Sanitize {
		var encodedDigests []string
		if err := db.WithContext(ctx).Raw("SELECT token_digest_sha256 FROM pki_enrollment_tokens").Scan(&encodedDigests).Error; err != nil {
			return pkiBackupSQLiteStage{}, fmt.Errorf("read enrollment token digests before sanitization: %w", err)
		}
		removedTokenDigests = make([][]byte, 0, len(encodedDigests))
		for _, digest := range encodedDigests {
			removedTokenDigests = append(removedTokenDigests, []byte(digest))
		}
		encodedDigests = nil
		if err := db.WithContext(ctx).Raw("SELECT digest_sha256 FROM pki_confirmation_nonces").Scan(&encodedDigests).Error; err != nil {
			return pkiBackupSQLiteStage{}, fmt.Errorf("read confirmation nonce digests before sanitization: %w", err)
		}
		for _, digest := range encodedDigests {
			removedTokenDigests = append(removedTokenDigests, []byte(digest))
		}
		defer func() {
			for _, digest := range removedTokenDigests {
				clear(digest)
			}
		}()
		if err := db.WithContext(ctx).Exec("PRAGMA secure_delete = ON").Error; err != nil {
			return pkiBackupSQLiteStage{}, fmt.Errorf("enable SQLite secure_delete: %w", err)
		}
		var secureDelete int
		if err := db.WithContext(ctx).Raw("PRAGMA secure_delete").Scan(&secureDelete).Error; err != nil || secureDelete != 1 {
			return pkiBackupSQLiteStage{}, fmt.Errorf("%w: SQLite secure_delete was not enabled", ErrPKIBackupIntegrity)
		}
	}
	if options.Sanitize || options.ForceVersion != nil {
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if options.Sanitize {
				if err := tx.Exec("DELETE FROM pki_enrollment_tokens").Error; err != nil {
					return fmt.Errorf("remove enrollment tokens: %w", err)
				}
				if err := tx.Exec("DELETE FROM pki_enrollment_replays").Error; err != nil {
					return fmt.Errorf("remove enrollment replays: %w", err)
				}
				if err := tx.Exec("DELETE FROM pki_confirmation_nonces").Error; err != nil {
					return fmt.Errorf("remove confirmation nonces: %w", err)
				}
				if err := tx.Exec("DELETE FROM pki_instance_lease").Error; err != nil {
					return fmt.Errorf("remove instance lease: %w", err)
				}
			}
			if options.ForceVersion != nil {
				if options.ForceVersion.PKIEpoch < 0 || options.ForceVersion.SecurityRevision != 0 {
					return fmt.Errorf("%w: forced version must use a non-negative epoch and revision zero", ErrPKIBackupIntegrity)
				}
				result := tx.Exec("UPDATE pki_settings SET pki_epoch = ?, security_revision = 0", options.ForceVersion.PKIEpoch)
				if result.Error != nil {
					return fmt.Errorf("rewrite forced PKI epoch: %w", result.Error)
				}
				if result.RowsAffected != 1 {
					return fmt.Errorf("%w: forced restore requires exactly one PKI settings row", ErrPKIBackupIntegrity)
				}
				if err := tx.Exec("DELETE FROM pki_instance_lease").Error; err != nil {
					return fmt.Errorf("remove stale instance lease: %w", err)
				}
				if err := tx.Exec("DELETE FROM pki_security_snapshot").Error; err != nil {
					return fmt.Errorf("remove stale security snapshot: %w", err)
				}
			}
			return nil
		})
		if err != nil {
			return pkiBackupSQLiteStage{}, err
		}
	}
	if options.Sanitize {
		if err := db.WithContext(ctx).Exec("VACUUM").Error; err != nil {
			return pkiBackupSQLiteStage{}, fmt.Errorf("vacuum sanitized PKI SQLite snapshot: %w", err)
		}
		if err := validatePKIBackupSQLiteFreelist(ctx, db); err != nil {
			return pkiBackupSQLiteStage{}, err
		}
	}
	var tokenCount int64
	if err := db.WithContext(ctx).Raw("SELECT COUNT(*) FROM pki_enrollment_tokens").Scan(&tokenCount).Error; err != nil {
		return pkiBackupSQLiteStage{}, fmt.Errorf("count staged enrollment tokens: %w", err)
	}
	if tokenCount != 0 {
		return pkiBackupSQLiteStage{}, fmt.Errorf("%w: enrollment tokens are forbidden in a protected backup", ErrPKIBackupIntegrity)
	}
	var settingsCount int64
	if err := db.WithContext(ctx).Raw("SELECT COUNT(*) FROM pki_settings WHERE id = 1").Scan(&settingsCount).Error; err != nil {
		return pkiBackupSQLiteStage{}, fmt.Errorf("count staged PKI settings: %w", err)
	}
	var allSettingsCount int64
	if err := db.WithContext(ctx).Raw("SELECT COUNT(*) FROM pki_settings").Scan(&allSettingsCount).Error; err != nil {
		return pkiBackupSQLiteStage{}, fmt.Errorf("count all staged PKI settings: %w", err)
	}
	if settingsCount != 1 || allSettingsCount != 1 {
		return pkiBackupSQLiteStage{}, fmt.Errorf("%w: staged database requires exactly the PKI settings singleton", ErrPKIBackupSchema)
	}
	if err := validatePKIBackupSQLiteIntegrity(ctx, db); err != nil {
		return pkiBackupSQLiteStage{}, err
	}
	schemaVersion, schemaDigest, err := inspectPKIBackupSchema(ctx, db)
	if err != nil {
		return pkiBackupSQLiteStage{}, err
	}
	if err := closeDB(); err != nil {
		return pkiBackupSQLiteStage{}, fmt.Errorf("close staged PKI SQLite validation handle: %w", err)
	}
	closed = true

	state, err := validatePKIBackupCanonicalState(ctx, path, directory, options.ForceVersion != nil)
	if err != nil {
		return pkiBackupSQLiteStage{}, err
	}
	result, err := os.ReadFile(path)
	if err != nil {
		return pkiBackupSQLiteStage{}, fmt.Errorf("read validated PKI SQLite snapshot: %w", err)
	}
	if len(result) == 0 || len(result) > pkiBackupMaxSnapshotSize {
		clear(result)
		return pkiBackupSQLiteStage{}, fmt.Errorf("%w: validated SQLite snapshot size is invalid", ErrPKIBackupSchema)
	}
	for _, digest := range removedTokenDigests {
		if len(digest) != 0 && bytes.Contains(result, digest) {
			clear(result)
			return pkiBackupSQLiteStage{}, fmt.Errorf("%w: removed enrollment token digest remains in sanitized SQLite bytes", ErrPKIBackupIntegrity)
		}
	}
	return pkiBackupSQLiteStage{
		Snapshot: result, State: state, SchemaVersion: schemaVersion,
		SchemaSHA256: schemaDigest, EnrollmentTokens: tokenCount,
	}, nil
}

func validatePKIBackupSQLiteFreelist(ctx context.Context, db *gorm.DB) error {
	var freelistCount int64
	if err := db.WithContext(ctx).Raw("PRAGMA freelist_count").Scan(&freelistCount).Error; err != nil {
		return fmt.Errorf("%w: read SQLite freelist count: %v", ErrPKIBackupIntegrity, err)
	}
	if freelistCount != 0 {
		return fmt.Errorf("%w: sanitized SQLite snapshot retains %d free pages", ErrPKIBackupIntegrity, freelistCount)
	}
	return nil
}

func openPKIBackupSQLite(path string) (*gorm.DB, func() error, error) {
	dsn := path + "?_pragma=journal_mode(DELETE)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(FULL)"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return nil, nil, fmt.Errorf("%w: open staged SQLite database: %v", ErrPKIBackupSchema, err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, nil, fmt.Errorf("%w: access staged SQLite database: %v", ErrPKIBackupSchema, err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	return db, sqlDB.Close, nil
}

func validatePKIBackupTables(ctx context.Context, db *gorm.DB) error {
	var names []string
	if err := db.WithContext(ctx).Raw("SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name").Scan(&names).Error; err != nil {
		return fmt.Errorf("%w: list staged schema: %v", ErrPKIBackupSchema, err)
	}
	present := make(map[string]struct{}, len(names))
	for _, name := range names {
		present[name] = struct{}{}
	}
	for _, required := range requiredPKIBackupTables {
		if _, found := present[required]; !found {
			return fmt.Errorf("%w: required table %q is missing", ErrPKIBackupSchema, required)
		}
		if err := validatePKIBackupTableColumns(ctx, db, required, requiredPKIBackupColumns[required]); err != nil {
			return err
		}
	}
	return nil
}

type pkiBackupTableColumn struct {
	Name string `gorm:"column:name"`
}

func validatePKIBackupTableColumns(ctx context.Context, db *gorm.DB, table string, required []string) error {
	var columns []pkiBackupTableColumn
	if err := db.WithContext(ctx).Raw("PRAGMA table_info(" + table + ")").Scan(&columns).Error; err != nil {
		return fmt.Errorf("%w: inspect required table %q: %v", ErrPKIBackupSchema, table, err)
	}
	present := make(map[string]struct{}, len(columns))
	for _, column := range columns {
		present[column.Name] = struct{}{}
	}
	for _, name := range required {
		if _, found := present[name]; !found {
			return fmt.Errorf("%w: required column %q.%q is missing", ErrPKIBackupSchema, table, name)
		}
	}
	return nil
}

func validatePKIBackupSQLiteIntegrity(ctx context.Context, db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("%w: access SQLite integrity handle", ErrPKIBackupSchema)
	}
	rows, err := sqlDB.QueryContext(ctx, "PRAGMA integrity_check")
	if err != nil {
		return fmt.Errorf("%w: run SQLite integrity check: %v", ErrPKIBackupIntegrity, err)
	}
	integrityRows := 0
	for rows.Next() {
		var result string
		if err := rows.Scan(&result); err != nil {
			_ = rows.Close()
			return fmt.Errorf("%w: read SQLite integrity check: %v", ErrPKIBackupIntegrity, err)
		}
		integrityRows++
		if result != "ok" {
			_ = rows.Close()
			return fmt.Errorf("%w: SQLite integrity check rejected the snapshot", ErrPKIBackupIntegrity)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("%w: iterate SQLite integrity check: %v", ErrPKIBackupIntegrity, err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("%w: close SQLite integrity result: %v", ErrPKIBackupIntegrity, err)
	}
	if integrityRows != 1 {
		return fmt.Errorf("%w: SQLite integrity check did not return ok", ErrPKIBackupIntegrity)
	}

	foreignRows, err := sqlDB.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("%w: run SQLite foreign key check: %v", ErrPKIBackupIntegrity, err)
	}
	violated := foreignRows.Next()
	foreignErr := foreignRows.Err()
	if err := foreignRows.Close(); err != nil {
		return fmt.Errorf("%w: close SQLite foreign key result: %v", ErrPKIBackupIntegrity, err)
	}
	if violated {
		return fmt.Errorf("%w: SQLite foreign key check found a violation", ErrPKIBackupIntegrity)
	}
	if foreignErr != nil {
		return fmt.Errorf("%w: iterate SQLite foreign key check: %v", ErrPKIBackupIntegrity, foreignErr)
	}
	return nil
}

type pkiBackupSchemaEntry struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Table string `json:"table"`
	SQL   string `json:"sql"`
}

func inspectPKIBackupSchema(ctx context.Context, db *gorm.DB) (int, string, error) {
	var version int
	if err := db.WithContext(ctx).Raw("PRAGMA user_version").Scan(&version).Error; err != nil {
		return 0, "", fmt.Errorf("%w: read SQLite schema version: %v", ErrPKIBackupSchema, err)
	}
	var entries []pkiBackupSchemaEntry
	if err := db.WithContext(ctx).Raw(`
		SELECT type AS type, name AS name, tbl_name AS "table", COALESCE(sql, '') AS sql
		FROM sqlite_master
		WHERE name NOT LIKE 'sqlite_%'
		ORDER BY type, name, tbl_name
	`).Scan(&entries).Error; err != nil {
		return 0, "", fmt.Errorf("%w: inspect SQLite schema: %v", ErrPKIBackupSchema, err)
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		return 0, "", fmt.Errorf("encode SQLite schema fingerprint: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return version, hex.EncodeToString(digest[:]), nil
}

func validatePKIBackupCanonicalState(ctx context.Context, path, dataRoot string, allowMissingSecuritySnapshot bool) (storage.PKICanonicalState, error) {
	dsn := path + "?_pragma=journal_mode(DELETE)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(FULL)"
	store, err := storage.NewStore(storage.StoreConfig{
		Driver: "sqlite", DSN: dsn, DataRoot: dataRoot, LocalAgentID: "pki-backup-stage", SkipBootstrapSchema: true,
	})
	if err != nil {
		return storage.PKICanonicalState{}, fmt.Errorf("%w: open canonical PKI staging store: %v", ErrPKIBackupSchema, err)
	}
	state, loadErr := store.LoadPKICanonicalState(ctx)
	if loadErr == nil {
		loadErr = store.WithPKITransaction(ctx, func(*storage.PKITransaction) error { return nil })
	}
	closeErr := store.Close()
	if loadErr != nil {
		return storage.PKICanonicalState{}, fmt.Errorf("%w: canonical PKI relations are invalid: %v", ErrPKIBackupIntegrity, loadErr)
	}
	if closeErr != nil {
		return storage.PKICanonicalState{}, fmt.Errorf("close canonical PKI staging store: %w", closeErr)
	}
	if state.Settings == nil {
		return storage.PKICanonicalState{}, fmt.Errorf("%w: canonical PKI settings are missing", ErrPKIBackupSchema)
	}
	if len(state.EnrollmentTokens) != 0 || state.InstanceLease != nil {
		return storage.PKICanonicalState{}, fmt.Errorf("%w: ephemeral PKI credentials or lease survived sanitization", ErrPKIBackupIntegrity)
	}
	if allowMissingSecuritySnapshot {
		if state.Settings.SecurityRevision != 0 || state.SecuritySnapshot != nil {
			return storage.PKICanonicalState{}, fmt.Errorf("%w: forced restore staging must reset the security snapshot to revision zero", ErrPKIBackupIntegrity)
		}
	} else if _, err := storage.ValidateCanonicalPKISecuritySnapshot(state); err != nil {
		return storage.PKICanonicalState{}, fmt.Errorf("%w: canonical signed security snapshot is invalid: %v", ErrPKIBackupIntegrity, err)
	}
	return state, nil
}
