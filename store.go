package store

import "errors"

// SchemaMigrationTableName is the table adapters use to record applied migrations.
const SchemaMigrationTableName = `schema_migrations`

var (
	// ErrInvalidMigrationName is returned when a migration has an empty name.
	ErrInvalidMigrationName = errors.New("store: invalid migration name")

	// ErrDuplicateMigration is returned when two migrations share a name.
	ErrDuplicateMigration = errors.New("store: duplicate migration")

	// ErrChecksumMismatch is returned when an applied migration's content has changed.
	ErrChecksumMismatch = errors.New("store: migration checksum mismatch")
)
