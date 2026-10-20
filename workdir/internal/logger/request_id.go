package logger

import (
	"context"
	"log/slog"
)

// requestIDKey is the context key of the request ID.
type requestIDKey struct{}

// WithRequestID returns ctx carrying the ID of the HTTP request it serves.
// Every record logged with that context (slog.InfoContext and friends)
// through a logger built by New gets a "request_id" attribute.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestID returns the request ID stored in ctx by WithRequestID, or "".
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// requestIDHandler adds the request ID of the record's context, if any, to
// every record handled by next.
type requestIDHandler struct {
	slog.Handler
}

// Handle adds the "request_id" attribute when ctx carries one.
func (h requestIDHandler) Handle(ctx context.Context, rec slog.Record) error {
	if id := RequestID(ctx); id != "" {
		rec.AddAttrs(slog.String("request_id", id))
	}
	return h.Handler.Handle(ctx, rec)
}

// WithAttrs keeps the request ID handling on the derived handler.
func (h requestIDHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return requestIDHandler{h.Handler.WithAttrs(attrs)}
}

// WithGroup keeps the request ID handling on the derived handler.
func (h requestIDHandler) WithGroup(name string) slog.Handler {
	return requestIDHandler{h.Handler.WithGroup(name)}
}
