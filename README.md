# go-store

Forward-only SQL migrations for Go. Migrations are plain SQL files, applied in
order and recorded in a `schema_migrations` table. The `pgdo` package also
provides a shared pgx pool to a samber/do injector.

Requires Go 1.27+. The `pg` adapter uses pgx and is tested against
PostgreSQL 18.

```sh
go get github.com/angryboat/go-store
```

## Usage

Embed your migrations and load them with `UpFS`. Files are applied in lexical
order, so prefix them with a sortable version. The rename funcs turn each path
into the migration's name, which is what gets recorded.

```go
//go:embed migrations/*.sql
var migrationFS embed.FS

migrations, err := store.UpFS(migrationFS, "migrations/*.sql",
	store.RenameStripPrefix("migrations/"),
	store.RenameStripSuffix(".sql"),
)
if err != nil {
	return err
}
```

Run them at boot. Running it again, or from several processes at once, is
safe.

```go
conn, err := pgx.Connect(ctx, os.Getenv("DATABASE_URL"))
if err != nil {
	return err
}
defer conn.Close(ctx)

if err := pg.MigrateUp(ctx, conn, migrations); err != nil {
	return err
}
```

`MigrateUp` takes a `*pgx.Conn`, not a pool, because it holds a session
advisory lock for the whole run. With a `pgxpool.Pool`, `Acquire` a connection
and pass `Conn()`.

To build migrations in code instead of from files, implement
`store.UpMigration`. `Up` returns the statement and its arguments.

## Behavior

- **Order**: migrations run in slice order. A name already recorded is
  skipped, and a recorded name missing from the slice is ignored.
- **Transactions**: each migration runs in its own transaction along with the
  insert that records it, so a failed migration leaves no trace and the ones
  before it stay applied. Statements that can't run in a transaction, like
  `CREATE INDEX CONCURRENTLY`, aren't supported.
- **Multiple statements**: a file can hold several statements when `Up`
  returns no arguments. With arguments, pgx allows only one.
- **Checksums**: each recorded migration stores a SHA-256 of its statement.
  If an applied migration's statement changes, `MigrateUp` fails with
  `store.ErrChecksumMismatch` rather than skipping it. Arguments aren't part of
  the checksum.
- **Validation**: an empty or duplicate name fails with
  `store.ErrInvalidMigrationName` or `store.ErrDuplicateMigration` before
  anything touches the database.
- **Concurrency**: callers serialize on one advisory lock. The lock is
  released when `MigrateUp` returns, even if `ctx` was cancelled.

## Dependency injection

`pgdo.PoolProvider` registers a shared `*pgxpool.Pool` with a
[samber/do](https://github.com/samber/do) injector. It lives in its own
package, so importing `pg` doesn't pull in `do`. The pool opens and pings the
database on first invoke, and closes when the injector shuts down.

```go
injector := do.New(pgdo.PoolProvider)
defer injector.Shutdown()

pool, err := do.Invoke[*pgxpool.Pool](injector)
```

The connection string comes from `DATABASE_URL`. If it's unset, invoking the
pool fails with `pgdo.ErrNoDatabaseURL`. Override it with
`do.OverrideValue(injector, pgdo.PoolConnection(url))`, and the context used
to open and ping the pool with `do.OverrideNamedValue(injector,
pgdo.PoolSetupContext, ctx)`.

## Development

```sh
make db
make test
```

Tests connect to `DATABASE_URL`, defaulting to the compose database at
`localhost:5432` with user `postgres` and password `password`. Each test
creates and drops its own database.
