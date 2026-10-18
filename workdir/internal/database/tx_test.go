package database

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"app/internal/db"
)

// fakeTx records Commit and Rollback; any other pgx.Tx method panics
// through the nil embedded interface.
type fakeTx struct {
	pgx.Tx
	commitErr   error
	rollbackErr error
	commits     int
	rollbacks   int
}

func (t *fakeTx) Commit(context.Context) error {
	t.commits++
	return t.commitErr
}

func (t *fakeTx) Rollback(context.Context) error {
	t.rollbacks++
	return t.rollbackErr
}

type fakeBeginner struct {
	tx  *fakeTx
	err error
}

func (b *fakeBeginner) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	if b.err != nil {
		return nil, b.err
	}
	return b.tx, nil
}

func TestPoolTxRunnerCommitsOnSuccess(t *testing.T) {
	tx := &fakeTx{}
	r := NewTxRunner(&fakeBeginner{tx: tx})

	var got db.Querier
	err := r.InTx(context.Background(), func(q db.Querier) error {
		got = q
		return nil
	})
	if err != nil {
		t.Fatalf("InTx: %v", err)
	}
	if got == nil {
		t.Fatal("fn got a nil Querier")
	}
	if tx.commits != 1 || tx.rollbacks != 0 {
		t.Fatalf("commits=%d rollbacks=%d, want 1/0", tx.commits, tx.rollbacks)
	}
}

func TestPoolTxRunnerRollsBackOnError(t *testing.T) {
	tx := &fakeTx{}
	r := NewTxRunner(&fakeBeginner{tx: tx})
	boom := errors.New("boom")

	err := r.InTx(context.Background(), func(db.Querier) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	if tx.commits != 0 || tx.rollbacks != 1 {
		t.Fatalf("commits=%d rollbacks=%d, want 0/1", tx.commits, tx.rollbacks)
	}
}

func TestPoolTxRunnerJoinsRollbackError(t *testing.T) {
	rbErr := errors.New("connection lost")
	tx := &fakeTx{rollbackErr: rbErr}
	r := NewTxRunner(&fakeBeginner{tx: tx})
	boom := errors.New("boom")

	err := r.InTx(context.Background(), func(db.Querier) error { return boom })
	if !errors.Is(err, boom) || !errors.Is(err, rbErr) {
		t.Fatalf("err = %v, want both fn and rollback errors", err)
	}
}

func TestPoolTxRunnerIgnoresTxClosedOnRollback(t *testing.T) {
	tx := &fakeTx{rollbackErr: pgx.ErrTxClosed}
	r := NewTxRunner(&fakeBeginner{tx: tx})
	boom := errors.New("boom")

	err := r.InTx(context.Background(), func(db.Querier) error { return boom })
	if !errors.Is(err, boom) || errors.Is(err, pgx.ErrTxClosed) {
		t.Fatalf("err = %v, want only the fn error", err)
	}
}

func TestPoolTxRunnerRollsBackOnPanic(t *testing.T) {
	tx := &fakeTx{}
	r := NewTxRunner(&fakeBeginner{tx: tx})

	defer func() {
		if p := recover(); p != "kaboom" {
			t.Fatalf("recovered %v, want the original panic", p)
		}
		if tx.commits != 0 || tx.rollbacks != 1 {
			t.Fatalf("commits=%d rollbacks=%d, want 0/1", tx.commits, tx.rollbacks)
		}
	}()
	_ = r.InTx(context.Background(), func(db.Querier) error { panic("kaboom") })
	t.Fatal("InTx did not re-raise the panic")
}

func TestPoolTxRunnerRollsBackOnCommitError(t *testing.T) {
	commitErr := errors.New("serialization failure")
	tx := &fakeTx{commitErr: commitErr}
	r := NewTxRunner(&fakeBeginner{tx: tx})

	err := r.InTx(context.Background(), func(db.Querier) error { return nil })
	if !errors.Is(err, commitErr) {
		t.Fatalf("err = %v, want %v", err, commitErr)
	}
	if tx.rollbacks != 1 {
		t.Fatalf("rollbacks = %d, want 1", tx.rollbacks)
	}
}

func TestPoolTxRunnerBeginError(t *testing.T) {
	beginErr := errors.New("pool closed")
	called := false
	r := NewTxRunner(&fakeBeginner{err: beginErr})

	err := r.InTx(context.Background(), func(db.Querier) error {
		called = true
		return nil
	})
	if !errors.Is(err, beginErr) {
		t.Fatalf("err = %v, want %v", err, beginErr)
	}
	if called {
		t.Fatal("fn ran without a transaction")
	}
}

func TestFakeTxRunner(t *testing.T) {
	f := &FakeTxRunner{}
	boom := errors.New("boom")

	if err := f.InTx(context.Background(), func(db.Querier) error { return nil }); err != nil {
		t.Fatalf("InTx: %v", err)
	}
	if err := f.InTx(context.Background(), func(db.Querier) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	if f.Calls != 2 || f.Commits != 1 || f.Rollbacks != 1 {
		t.Fatalf("calls=%d commits=%d rollbacks=%d, want 2/1/1", f.Calls, f.Commits, f.Rollbacks)
	}

	f.Err = errors.New("begin failed")
	called := false
	if err := f.InTx(context.Background(), func(db.Querier) error { called = true; return nil }); !errors.Is(err, f.Err) || called {
		t.Fatalf("err = %v called = %v, want begin error without running fn", err, called)
	}
}
