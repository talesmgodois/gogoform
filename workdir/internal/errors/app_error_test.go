package errors

import (
	stderrors "errors"
	"fmt"
	"testing"
)

func TestAppErrorError(t *testing.T) {
	if got := New(CodeNotFound, "form not found").Error(); got != "form not found" {
		t.Fatalf("Error() = %q", got)
	}
	cause := stderrors.New("no rows")
	if got := Wrap(cause, CodeNotFound, "form not found").Error(); got != "form not found: no rows" {
		t.Fatalf("Error() = %q", got)
	}
}

func TestAppErrorUnwrap(t *testing.T) {
	cause := stderrors.New("no rows")
	err := fmt.Errorf("get form: %w", Wrap(cause, CodeNotFound, "form not found"))

	if !stderrors.Is(err, cause) {
		t.Fatal("errors.Is does not see the wrapped cause")
	}
	appErr, ok := As(err)
	if !ok {
		t.Fatal("As did not find the AppError")
	}
	if appErr.Code != CodeNotFound {
		t.Fatalf("Code = %q, want %q", appErr.Code, CodeNotFound)
	}
}

func TestAsNonAppError(t *testing.T) {
	if appErr, ok := As(stderrors.New("boom")); ok || appErr != nil {
		t.Fatalf("As = (%v, %v), want (nil, false)", appErr, ok)
	}
	if _, ok := As(nil); ok {
		t.Fatal("As(nil) reported an AppError")
	}
}
