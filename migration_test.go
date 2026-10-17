package store

import (
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpFS(t *testing.T) {
	fsys := fstest.MapFS{
		"migrations/002_posts.sql": {Data: []byte("CREATE TABLE posts ();")},
		"migrations/001_users.sql": {Data: []byte("CREATE TABLE users ();")},
		"migrations/README.md":     {Data: []byte("ignored")},
	}

	t.Run("loads matches in lexical order with renames applied", func(t *testing.T) {
		ms, err := UpFS(fsys, "migrations/*.sql", RenameStripPrefix("migrations/"), RenameStripSuffix(".sql"))
		require.NoError(t, err)
		require.Len(t, ms, 2)

		assert.Equal(t, "001_users", ms[0].Name())
		sql, args := ms[0].Up()
		assert.Equal(t, "CREATE TABLE users ();", sql)
		assert.Nil(t, args)
		assert.Equal(t, "002_posts", ms[1].Name())
	})

	t.Run("returns ErrNotExist when nothing matches", func(t *testing.T) {
		_, err := UpFS(fsys, "nope/*.sql")
		assert.ErrorIs(t, err, fs.ErrNotExist)
	})
}
