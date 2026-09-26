package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"app/internal/config"
	"app/internal/db"
)

// pingTimeout bounds the connectivity check performed by openPostgres.
const pingTimeout = 5 * time.Second

// openPostgres creates a connection pool for uri and verifies the database
// is reachable.
func openPostgres(ctx context.Context, uri string) (*DB, error) {
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

	return &DB{
		Querier: db.New(pool),
		Driver:  config.DriverPostgres,
		close:   pool.Close,
		logAttrs: []any{
			"host", poolCfg.ConnConfig.Host,
			"port", poolCfg.ConnConfig.Port,
			"database", poolCfg.ConnConfig.Database,
		},
	}, nil
}
