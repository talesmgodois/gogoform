// Package database opens the PostgreSQL connection pool used by the application.
package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// pingTimeout bounds the connectivity check performed by Connect.
const pingTimeout = 5 * time.Second

// Connect creates a connection pool for uri and verifies the database is
// reachable. The caller must Close the returned pool.
func Connect(ctx context.Context, uri string) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(uri)
	if err != nil {
		// Parse errors may echo the URI, which can contain credentials.
		return nil, errors.New("database: invalid connection URI")
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("database: create pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("database: ping %s:%d/%s: %w",
			poolCfg.ConnConfig.Host, poolCfg.ConnConfig.Port, poolCfg.ConnConfig.Database, err)
	}
	return pool, nil
}
