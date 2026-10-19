package billing

import (
	"context"
	stderrors "errors"
	"testing"

	"app/internal/db"
	apperrors "app/internal/errors"
)

// fakeQuerier implements db.Querier with per-method stubs. Calling a method
// without a stub panics through the nil embedded interface.
type fakeQuerier struct {
	db.Querier
	insertEvent func(db.InsertStripeEventParams) (int64, error)
}

func (f *fakeQuerier) InsertStripeEvent(_ context.Context, arg db.InsertStripeEventParams) (int64, error) {
	return f.insertEvent(arg)
}

func TestMarkProcessed(t *testing.T) {
	tests := []struct {
		name      string
		rows      int64
		err       error
		wantFirst bool
		wantCode  apperrors.Code
	}{
		{"first time", 1, nil, true, ""},
		{"already processed", 0, nil, false, ""},
		{"db failure", 0, stderrors.New("connection reset"), false, apperrors.CodeInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewEventStore(&fakeQuerier{insertEvent: func(arg db.InsertStripeEventParams) (int64, error) {
				if want := (db.InsertStripeEventParams{ID: "evt_1", Type: "invoice.payment_failed"}); arg != want {
					t.Fatalf("params = %+v, want %+v", arg, want)
				}
				return tt.rows, tt.err
			}})

			first, err := store.MarkProcessed(context.Background(), "evt_1", "invoice.payment_failed")

			if first != tt.wantFirst {
				t.Fatalf("firstTime = %v, want %v", first, tt.wantFirst)
			}
			if tt.wantCode == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				return
			}
			if appErr, ok := apperrors.As(err); !ok || appErr.Code != tt.wantCode {
				t.Fatalf("err = %v, want AppError %q", err, tt.wantCode)
			}
		})
	}
}
