// Package pgdo provides a shared *pgxpool.Pool to a samber/do injector.
package pgdo

import (
	"context"
	"errors"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/do/v2"
)

// ErrNoDatabaseURL is returned when DATABASE_URL is unset and no
// PoolConnection has been provided.
var ErrNoDatabaseURL = errors.New("pgdo: DATABASE_URL is not set")

// PoolConnection is the connection string PoolProvider opens the pool with.
// By default it comes from the DATABASE_URL environment variable. Override it
// with do.OverrideValue.
type PoolConnection string

// PoolSetupContext names the context.Context PoolProvider uses to open and
// ping the pool. It defaults to context.Background(); override it with
// do.OverrideNamedValue.
const PoolSetupContext = "pgdo.pool-setup-context"

// PoolProvider registers a shared *pgxpool.Pool with a do injector. The pool is
// opened and pinged on first invoke, and every later invoke returns the same
// pool. Shutting down the injector closes it.
var PoolProvider = do.Package(
	do.Lazy(providePoolConnection),
	do.Lazy(providePoolCloser),
	do.TransientNamed(PoolSetupContext, providePoolSetupContext),
	do.Transient(providePool),
)

func providePoolSetupContext(do.Injector) (context.Context, error) {
	return context.Background(), nil
}

func providePoolConnection(do.Injector) (PoolConnection, error) {
	value := os.Getenv("DATABASE_URL")
	if value == "" {
		return "", ErrNoDatabaseURL
	}
	return PoolConnection(value), nil
}

func providePoolCloser(i do.Injector) (*poolCloser, error) {
	conn, err := do.Invoke[PoolConnection](i)
	if err != nil {
		return nil, err
	}
	ctx, err := do.InvokeNamed[context.Context](i, PoolSetupContext)
	if err != nil {
		return nil, err
	}

	pool, err := pgxpool.New(ctx, string(conn))
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}

	return &poolCloser{pool: pool}, nil
}

func providePool(i do.Injector) (*pgxpool.Pool, error) {
	closer, err := do.Invoke[*poolCloser](i)
	if err != nil {
		return nil, err
	}
	return closer.pool, nil
}

type poolCloser struct {
	pool *pgxpool.Pool
}

func (c *poolCloser) Shutdown() {
	slog.Debug("PGX Pool Shutting Down")
	c.pool.Close()
}

var _ do.Shutdowner = (*poolCloser)(nil)
