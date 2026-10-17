package pg

import (
	"cmp"
	"context"
	"fmt"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/angryboat/go-store"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var adminURL = cmp.Or(os.Getenv("DATABASE_URL"), "postgres://postgres:password@localhost:5432/postgres?sslmode=disable")

var dbCounter atomic.Int64

func connect(tb testing.TB, connString string) *pgx.Conn {
	tb.Helper()
	conn, err := pgx.Connect(tb.Context(), connString)
	require.NoError(tb, err)
	tb.Cleanup(func() { conn.Close(context.Background()) })
	return conn
}

func newDatabaseURL(tb testing.TB) string {
	tb.Helper()
	admin := connect(tb, adminURL)
	name := fmt.Sprintf("go_store_test_%d", dbCounter.Add(1))
	drop := `DROP DATABASE IF EXISTS "` + name + `" WITH (FORCE);`
	_, err := admin.Exec(tb.Context(), drop)
	require.NoError(tb, err)
	_, err = admin.Exec(tb.Context(), `CREATE DATABASE "`+name+`";`)
	require.NoError(tb, err)
	tb.Cleanup(func() {
		_, err := admin.Exec(context.WithoutCancel(tb.Context()), drop)
		assert.NoError(tb, err)
	})

	u, err := url.Parse(adminURL)
	require.NoError(tb, err)
	u.Path = "/" + name
	return u.String()
}

type sqlMigration struct {
	name string
	sql  string
}

func (m sqlMigration) Name() string        { return m.name }
func (m sqlMigration) Up() (string, []any) { return m.sql, nil }

func migrations(ms ...sqlMigration) []store.UpMigration {
	out := make([]store.UpMigration, len(ms))
	for i, m := range ms {
		out[i] = m
	}
	return out
}

func appliedVersions(tb testing.TB, conn *pgx.Conn) []string {
	tb.Helper()
	rows, err := conn.Query(tb.Context(), `SELECT "version" FROM `+schemaMigrationTableName+` ORDER BY "applied_at";`)
	require.NoError(tb, err)
	versions, err := pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(tb, err)
	return versions
}

func TestMigrateUp(t *testing.T) {
	t.Run("applies in order and is idempotent", func(t *testing.T) {
		conn := connect(t, newDatabaseURL(t))
		ms := migrations(
			sqlMigration{"001_users", `CREATE TABLE users (id BIGINT PRIMARY KEY);`},
			sqlMigration{"002_posts", `CREATE TABLE posts (id BIGINT PRIMARY KEY, user_id BIGINT REFERENCES users); CREATE INDEX ON posts (user_id);`},
		)

		require.NoError(t, MigrateUp(t.Context(), conn, ms))
		require.NoError(t, MigrateUp(t.Context(), conn, ms))

		assert.Equal(t, []string{"001_users", "002_posts"}, appliedVersions(t, conn))
	})

	t.Run("rejects changed applied migration", func(t *testing.T) {
		conn := connect(t, newDatabaseURL(t))
		require.NoError(t, MigrateUp(t.Context(), conn, migrations(sqlMigration{"001", `CREATE TABLE a (id INT);`})))

		err := MigrateUp(t.Context(), conn, migrations(sqlMigration{"001", `CREATE TABLE a (id BIGINT);`}))

		assert.ErrorIs(t, err, store.ErrChecksumMismatch)
	})

	t.Run("rolls back failed migration and keeps earlier ones", func(t *testing.T) {
		conn := connect(t, newDatabaseURL(t))
		ms := migrations(
			sqlMigration{"001", `CREATE TABLE a (id INT);`},
			sqlMigration{"002", `CREATE TABLE b (id INT); SELECT * FROM missing;`},
			sqlMigration{"003", `CREATE TABLE c (id INT);`},
		)

		require.Error(t, MigrateUp(t.Context(), conn, ms))

		assert.Equal(t, []string{"001"}, appliedVersions(t, conn))
		var exists bool
		require.NoError(t, conn.QueryRow(t.Context(), `SELECT to_regclass('b') IS NOT NULL;`).Scan(&exists))
		assert.False(t, exists)
	})

	t.Run("releases lock after failure", func(t *testing.T) {
		conn := connect(t, newDatabaseURL(t))
		require.Error(t, MigrateUp(t.Context(), conn, migrations(sqlMigration{"001", `NOT SQL;`})))

		var locks int
		require.NoError(t, conn.QueryRow(t.Context(), `SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND pid = pg_backend_pid();`).Scan(&locks))
		assert.Zero(t, locks)
	})

	t.Run("serializes concurrent callers", func(t *testing.T) {
		dbURL := newDatabaseURL(t)
		ms := migrations(
			sqlMigration{"001", `CREATE TABLE a (id INT); SELECT pg_sleep(0.2);`},
			sqlMigration{"002", `CREATE TABLE b (id INT);`},
		)

		const callers = 4
		conns := make([]*pgx.Conn, callers)
		for i := range conns {
			conns[i] = connect(t, dbURL)
		}
		errs := make([]error, callers)
		var wg sync.WaitGroup
		for i, conn := range conns {
			wg.Go(func() { errs[i] = MigrateUp(t.Context(), conn, ms) })
		}
		wg.Wait()

		for _, err := range errs {
			assert.NoError(t, err)
		}
		assert.Equal(t, []string{"001", "002"}, appliedVersions(t, conns[0]))
	})

	t.Run("validates before touching the database", func(t *testing.T) {
		tests := map[string]struct {
			migrations []store.UpMigration
			want       error
		}{
			"empty name": {migrations(sqlMigration{"", `SELECT 1;`}), store.ErrInvalidMigrationName},
			"duplicate":  {migrations(sqlMigration{"001", `SELECT 1;`}, sqlMigration{"001", `SELECT 2;`}), store.ErrDuplicateMigration},
		}
		for name, tt := range tests {
			t.Run(name, func(t *testing.T) {
				assert.ErrorIs(t, MigrateUp(t.Context(), nil, tt.migrations), tt.want)
			})
		}
	})
}
