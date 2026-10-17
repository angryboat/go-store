package pgdo

import (
	"cmp"
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/do/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var databaseURL = cmp.Or(os.Getenv("DATABASE_URL"), "postgres://postgres:password@localhost:5432/postgres?sslmode=disable")

func newInjector(tb testing.TB, conn string) do.Injector {
	tb.Helper()
	i := do.New(PoolProvider)
	do.OverrideValue(i, PoolConnection(conn))
	tb.Cleanup(func() { i.Shutdown() })
	return i
}

func TestPoolProvider(t *testing.T) {
	t.Run("reads the connection from DATABASE_URL", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "postgres://example:5432/app")
		i := do.New(PoolProvider)

		conn, err := do.Invoke[PoolConnection](i)

		require.NoError(t, err)
		assert.Equal(t, PoolConnection("postgres://example:5432/app"), conn)
	})

	t.Run("fails when DATABASE_URL is unset", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "")
		i := do.New(PoolProvider)

		_, err := do.Invoke[*pgxpool.Pool](i)

		assert.ErrorIs(t, err, ErrNoDatabaseURL)
	})

	t.Run("shares one pool across invokes", func(t *testing.T) {
		i := newInjector(t, databaseURL)

		first, err := do.Invoke[*pgxpool.Pool](i)
		require.NoError(t, err)
		second, err := do.Invoke[*pgxpool.Pool](i)
		require.NoError(t, err)

		assert.Same(t, first, second)
		assert.NoError(t, first.Ping(t.Context()))
	})

	t.Run("closes the pool on shutdown", func(t *testing.T) {
		i := do.New(PoolProvider)
		do.OverrideValue(i, PoolConnection(databaseURL))
		pool, err := do.Invoke[*pgxpool.Pool](i)
		require.NoError(t, err)

		require.True(t, i.Shutdown().Succeed)

		assert.Error(t, pool.Ping(t.Context()))
	})

	t.Run("fails on an invalid connection string", func(t *testing.T) {
		i := newInjector(t, "postgres://%zz")

		_, err := do.Invoke[*pgxpool.Pool](i)

		assert.Error(t, err)
	})

	t.Run("fails when the database is unreachable", func(t *testing.T) {
		i := newInjector(t, "postgres://postgres:password@127.0.0.1:1/postgres?sslmode=disable&connect_timeout=1")

		_, err := do.Invoke[*pgxpool.Pool](i)

		assert.Error(t, err)
	})

	t.Run("uses the setup context", func(t *testing.T) {
		i := newInjector(t, databaseURL)
		ctx, cancel := context.WithTimeout(t.Context(), time.Nanosecond)
		defer cancel()
		<-ctx.Done()
		do.OverrideNamedValue(i, PoolSetupContext, ctx)

		_, err := do.Invoke[*pgxpool.Pool](i)

		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})
}
