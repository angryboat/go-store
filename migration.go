package store

import (
	"io/fs"
	"strings"
)

// Migration is a named schema change.
type Migration interface {
	Name() string
}

// UpMigration is a Migration that can be applied. Up returns the SQL statement
// and its arguments; the checksum recorded for it covers the statement only.
type UpMigration interface {
	Migration

	Up() (string, []any)
}

// RenameStripPrefix returns a rename func for UpFS that trims prefix from a path.
func RenameStripPrefix(prefix string) func(string) string {
	return func(name string) string {
		return strings.TrimPrefix(name, prefix)
	}
}

// RenameStripSuffix returns a rename func for UpFS that trims suffix from a path.
func RenameStripSuffix(suffix string) func(string) string {
	return func(name string) string {
		return strings.TrimSuffix(name, suffix)
	}
}

// UpFS loads every file in f matching glob as an UpMigration, in lexical order.
// Each rename func is applied in turn to the file path to produce the migration
// name. It returns fs.ErrNotExist when glob matches nothing.
func UpFS(f fs.FS, glob string, rename ...func(string) string) ([]UpMigration, error) {
	filePaths, err := fs.Glob(f, glob)
	if err != nil {
		return nil, err
	}
	if len(filePaths) == 0 {
		return nil, fs.ErrNotExist
	}

	migrations := make([]UpMigration, 0, len(filePaths))
	for _, path := range filePaths {
		content, err := fs.ReadFile(f, path)
		if err != nil {
			return nil, err
		}
		name := path
		for _, r := range rename {
			name = r(name)
		}
		migrations = append(migrations, &fileUpMigration{
			name:    name,
			content: string(content),
		})
	}

	return migrations, nil
}

type fileUpMigration struct {
	name    string
	content string
}

func (f *fileUpMigration) Name() string {
	return f.name
}

func (f *fileUpMigration) Up() (string, []any) {
	return f.content, nil
}
