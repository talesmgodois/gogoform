package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"app/internal/db"
)

// TxRunner runs a unit of work inside a single database transaction.
type TxRunner interface {
	// InTx calls fn with a Querier bound to a new transaction. The
	// transaction is committed when fn returns nil and rolled back when it
	// returns an error or panics (the panic is then re-raised).
	InTx(ctx context.Context, fn func(q db.Querier) error) error
}

// txBeginner is the part of *pgxpool.Pool used by PoolTxRunner.
type txBeginner interface {
	BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
}

// PoolTxRunner is the TxRunner backed by a connection pool.
type PoolTxRunner struct {
	pool txBeginner
}

// NewTxRunner returns a TxRunner that opens its transactions on pool
// (typically a *pgxpool.Pool).
func NewTxRunner(pool txBeginner) *PoolTxRunner {
	return &PoolTxRunner{pool: pool}
}

// InTx implements TxRunner.
func (r *PoolTxRunner) InTx(ctx context.Context, fn func(q db.Querier) error) (err error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("database: begin transaction: %w", err)
	}

	committed := false
	defer func() {
		if committed {
			return
		}
		// Rollback uses a fresh context so it still runs when ctx is done.
		if rbErr := tx.Rollback(context.WithoutCancel(ctx)); rbErr != nil &&
			!errors.Is(rbErr, pgx.ErrTxClosed) && err != nil {
			err = errors.Join(err, fmt.Errorf("database: rollback: %w", rbErr))
		}
	}()

	if err := fn(db.New(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("database: commit: %w", err)
	}
	committed = true
	return nil
}

// FakeTxRunner is a TxRunner for unit tests: it runs fn against Querier
// without a real transaction and records the outcome of each call.
type FakeTxRunner struct {
	Querier db.Querier
	// Err, when set, is returned by InTx without calling fn, as if the
	// transaction could not be started.
	Err error

	Calls     int
	Commits   int
	Rollbacks int
}

// InTx implements TxRunner.
func (f *FakeTxRunner) InTx(_ context.Context, fn func(q db.Querier) error) (err error) {
	f.Calls++
	if f.Err != nil {
		return f.Err
	}
	defer func() {
		if p := recover(); p != nil {
			f.Rollbacks++
			panic(p)
		}
	}()
	if err := fn(f.Querier); err != nil {
		f.Rollbacks++
		return err
	}
	f.Commits++
	return nil
}
