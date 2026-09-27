// Package db embeds the dbmate migration files so the application binary
// can apply them without requiring the dbmate CLI or filesystem access to
// the repo at runtime.
package db

import "embed"

//go:embed postgres/migrations/*.sql
var PostgresMigrations embed.FS

//go:embed sqlite/migrations/*.sql
var SQLiteMigrations embed.FS
