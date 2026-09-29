// Package db embeds the dbmate migrations of every supported engine, so the
// API binary can apply them at startup without the migration files or the
// dbmate CLI (see internal/database.Migrate).
package db

import (
	"embed"
	"fmt"
	"io/fs"
)

//go:embed postgres/migrations/*.sql sqlite/migrations/*.sql
var migrations embed.FS

// Migrations returns the migrations directory of engine ("postgres" or
// "sqlite"), rooted so its entries are the migration files themselves.
func Migrations(engine string) (fs.FS, error) {
	switch engine {
	case "postgres", "sqlite":
		return fs.Sub(migrations, engine+"/migrations")
	default:
		return nil, fmt.Errorf("db: no embedded migrations for engine %q", engine)
	}
}
