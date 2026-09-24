Create the application error type and the HTTP error helpers built on it, in `workdir/internal/errors/` (package `errors`).

1. `app_error.go` — defines `AppError`:
   - fields `Code` (a string `Code` type: `invalid_argument`, `unauthorized`, `forbidden`, `not_found`, `conflict`, `internal`), `Message` (safe to show to clients) and `Err` (optional cause);
   - `New(code, message)`, `Wrap(err, code, message)`, `Error()`, `Unwrap()` and `As(err) (*AppError, bool)`.

2. `http_errors.go`, next to `app_error.go` — uses the `AppError` struct to create HTTP errors:
   - constructors `NewBadRequest`, `NewUnauthorized`, `NewForbidden`, `NewNotFound`, `NewConflict`, `NewInternal(err)`;
   - `HTTPStatus(err)` mapping codes to 400/401/403/404/409, and anything else (including non-AppErrors) to 500;
   - `WriteHTTP(w, r, err)` writing a JSON `{"code","message"}` body; 5xx errors are logged and their message is replaced with a generic one so causes never leak.

Add table-driven unit tests for both files and godoc for every exported identifier.
