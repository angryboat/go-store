package pg

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/angryboat/go-store"
	"github.com/jackc/pgx/v5"
)

const (
	lockID = 0xFEED_C0DE_BAD0
)

//go:embed create_table.sql
var createSchemaMigrationTableSQL string

const schemaMigrationTableName = `"` + store.SchemaMigrationTableName + `"`

func init() {
	createSchemaMigrationTableSQL = strings.ReplaceAll(createSchemaMigrationTableSQL, `{{TABLE_NAME}}`, schemaMigrationTableName)
}

// MigrateUp applies each migration not yet recorded in the schema migrations
// table, in order, each in its own transaction. It holds a session advisory lock
// for the duration so concurrent callers serialize. A recorded migration whose
// statement has changed fails with store.ErrChecksumMismatch.
func MigrateUp(ctx context.Context, conn *pgx.Conn, migrations []store.UpMigration) (err error) {
	if err := validate(migrations); err != nil {
		return err
	}

	logger := slog.With(slog.String("store-adapter", "pgx"), slog.Int64("lock-id", lockID))

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1);`, lockID); err != nil {
		return fmt.Errorf("acquire advisory lock: %w", err)
	}

	logger.DebugContext(ctx, "Acquired advisory lock, proceeding with MigrateUp")

	defer func() {
		if _, uErr := conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1);`, lockID); uErr != nil {
			err = errors.Join(err, fmt.Errorf("release advisory lock: %w", uErr))
		}
	}()

	if _, err := conn.Exec(ctx, createSchemaMigrationTableSQL); err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}

	for _, migration := range migrations {
		if err := up(ctx, conn, logger, migration); err != nil {
			return fmt.Errorf("migration %q: %w", migration.Name(), err)
		}
	}

	return nil
}

func validate(migrations []store.UpMigration) error {
	seen := make(map[string]struct{}, len(migrations))
	for i, migration := range migrations {
		name := migration.Name()
		if name == "" {
			return fmt.Errorf("%w: migration at index %d has an empty name", store.ErrInvalidMigrationName, i)
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("%w: %q", store.ErrDuplicateMigration, name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func up(ctx context.Context, conn *pgx.Conn, logger *slog.Logger, migration store.UpMigration) (err error) {
	name := migration.Name()
	statement, args := migration.Up()
	checksum := sha256.Sum256([]byte(statement))

	t, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if rErr := t.Rollback(ctx); rErr != nil && !errors.Is(rErr, pgx.ErrTxClosed) {
			err = errors.Join(err, fmt.Errorf("rollback: %w", rErr))
		}
	}()

	var applied []byte
	err = t.QueryRow(ctx, `SELECT "checksum" FROM `+schemaMigrationTableName+` WHERE "version" = $1;`, name).Scan(&applied)
	switch {
	case err == nil:
		if !bytes.Equal(applied, checksum[:]) {
			return fmt.Errorf("%w: applied %s, have %s", store.ErrChecksumMismatch, hex.EncodeToString(applied), hex.EncodeToString(checksum[:]))
		}
		return nil
	case !errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("check applied: %w", err)
	}

	logger.DebugContext(ctx, "Migrate Up", slog.String("name", name))

	if _, err := t.Exec(ctx, statement, args...); err != nil {
		return fmt.Errorf("execute: %w", err)
	}
	if _, err := t.Exec(ctx, `INSERT INTO `+schemaMigrationTableName+` ("version", "checksum") VALUES ($1, $2);`, name, checksum[:]); err != nil {
		return fmt.Errorf("record: %w", err)
	}
	if err := t.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	logger.DebugContext(ctx, "Migration Done", slog.String("name", name), slog.String("checksum", hex.EncodeToString(checksum[:])))

	return nil
}
