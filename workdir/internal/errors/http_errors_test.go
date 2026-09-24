package errors

import (
	"encoding/json"
	stderrors "errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPStatus(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"bad request", NewBadRequest("bad"), http.StatusBadRequest},
		{"unauthorized", NewUnauthorized("who"), http.StatusUnauthorized},
		{"forbidden", NewForbidden("no"), http.StatusForbidden},
		{"not found", NewNotFound("missing"), http.StatusNotFound},
		{"conflict", NewConflict("dup"), http.StatusConflict},
		{"internal", NewInternal(stderrors.New("db down")), http.StatusInternalServerError},
		{"unknown code", New(Code("weird"), "?"), http.StatusInternalServerError},
		{"plain error", stderrors.New("boom"), http.StatusInternalServerError},
		{"wrapped app error", fmt.Errorf("ctx: %w", NewNotFound("missing")), http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HTTPStatus(tt.err); got != tt.want {
				t.Fatalf("HTTPStatus = %d, want %d", got, tt.want)
			}
		})
	}
}

func writeAndDecode(t *testing.T, err error) (*httptest.ResponseRecorder, string, HTTPErrorResponse) {
	t.Helper()
	rec := httptest.NewRecorder()
	WriteHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil), err)

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q", ct)
	}
	raw := rec.Body.String()
	var body HTTPErrorResponse
	if decErr := json.Unmarshal([]byte(raw), &body); decErr != nil {
		t.Fatalf("decode: %v", decErr)
	}
	return rec, raw, body
}

func TestWriteHTTPClientError(t *testing.T) {
	rec, _, body := writeAndDecode(t, NewNotFound("form not found"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if body.Code != CodeNotFound || body.Message != "form not found" {
		t.Fatalf("body = %+v", body)
	}
}

func TestWriteHTTPHidesInternalCause(t *testing.T) {
	for _, err := range []error{
		NewInternal(stderrors.New("password=secret")),
		stderrors.New("password=secret"),
		New(Code("weird"), "password=secret"),
	} {
		rec, raw, body := writeAndDecode(t, err)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
		}
		if body.Code != CodeInternal || body.Message != internalMessage {
			t.Fatalf("body = %+v", body)
		}
		if strings.Contains(raw, "secret") {
			t.Fatalf("response leaks cause: %s", raw)
		}
	}
}
