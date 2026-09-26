#!/usr/bin/env bash
# Database automation: DBML -> SQL (dbml2sql) -> dbmate migration -> apply.
#
# Usage: scripts/setup_migrations.sh [all|tools|schema|migration|up]
#   tools      check/install dbml2sql, dbmate, sqlc and air
#   schema     convert $DBML_INPUT into $SCHEMA_OUTPUT
#   migration  create the initial dbmate migration from $SCHEMA_OUTPUT (once)
#   up         apply pending migrations to $DATABASE_URL
#   all        every step above, in order (default)
#
# Every path and URL below can be overridden through the environment.
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

DBML_INPUT="${DBML_INPUT:-../__ai_work/_inputs/db_schema.dbml}"
SCHEMA_OUTPUT="${SCHEMA_OUTPUT:-../__ai_work/outputs/schema.sql}"
MIGRATIONS_DIR="${MIGRATIONS_DIR:-db/postgres/migrations}"
INITIAL_MIGRATION_NAME="initial_schema"
BIN_DIR="${BIN_DIR:-$HOME/.local/bin}"
DBMATE_FLAGS="${DBMATE_FLAGS:---wait --no-dump-schema}"
export DATABASE_URL="${DATABASE_URL:-postgres://postgres:postgres@localhost:5432/app_db?sslmode=disable}"

# Binaries installed by this script must be visible to the later steps.
export PATH="$PATH:$BIN_DIR"
if command -v go >/dev/null 2>&1; then
  PATH="$PATH:$(go env GOPATH)/bin"
fi

log()  { printf '\033[36m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[33mWARN:\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[31mERROR:\033[0m %s\n' "$*" >&2; exit 1; }
trap 'die "command failed at line $LINENO: $BASH_COMMAND"' ERR

has() { command -v "$1" >/dev/null 2>&1; }

# ---------------------------------------------------------------------------
# 1. Tools
# ---------------------------------------------------------------------------

install_dbml2sql() {
  has dbml2sql && { log "dbml2sql found: $(command -v dbml2sql)"; return; }
  has npm || die "dbml2sql missing and npm not found. Install Node.js, then run: npm install -g @dbml/cli"
  log "Installing dbml2sql (npm install -g @dbml/cli)"
  npm install -g @dbml/cli
  has dbml2sql || die "dbml2sql still not on PATH after install; check 'npm prefix -g'"
}

install_dbmate() {
  has dbmate && { log "dbmate found: $(command -v dbmate)"; return; }
  if has brew; then
    log "Installing dbmate (brew install dbmate)"
    brew install dbmate
    return
  fi

  local os arch
  case "$(uname -s)" in
    Linux)  os=linux ;;
    Darwin) os=macos ;;
    *) die "unsupported OS for dbmate auto-install; see https://github.com/amacneil/dbmate#installation" ;;
  esac
  case "$(uname -m)" in
    x86_64|amd64)  arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) die "unsupported architecture $(uname -m); see https://github.com/amacneil/dbmate#installation" ;;
  esac
  has curl || die "curl is required to download dbmate"

  log "Downloading dbmate-$os-$arch into $BIN_DIR"
  mkdir -p "$BIN_DIR"
  curl -fsSL -o "$BIN_DIR/dbmate" \
    "https://github.com/amacneil/dbmate/releases/latest/download/dbmate-$os-$arch"
  chmod +x "$BIN_DIR/dbmate"
  warn "make sure $BIN_DIR is in your PATH"
}

install_sqlc() {
  has sqlc && { log "sqlc found: $(command -v sqlc)"; return; }
  if has brew; then
    log "Installing sqlc (brew install sqlc)"
    brew install sqlc
  elif has go; then
    log "Installing sqlc (go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest)"
    go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
    warn "make sure $(go env GOPATH)/bin is in your PATH"
  else
    die "sqlc missing. Install it with 'brew install sqlc' or 'go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest'"
  fi
  has sqlc || die "sqlc still not on PATH after install"
}

install_air() {
  has air && { log "air found: $(command -v air)"; return; }
  if has brew; then
    log "Installing air (brew install air)"
    brew install air
  elif has go; then
    log "Installing air (go install github.com/air-verse/air@latest)"
    go install github.com/air-verse/air@latest
    warn "make sure $(go env GOPATH)/bin is in your PATH"
  else
    die "air missing. Install it with 'brew install air' or 'go install github.com/air-verse/air@latest'"
  fi
  has air || die "air still not on PATH after install"
}

step_tools() {
  install_dbml2sql
  install_dbmate
  install_sqlc
  install_air
}

# ---------------------------------------------------------------------------
# 2. DBML -> SQL
# ---------------------------------------------------------------------------

step_schema() {
  has dbml2sql || die "dbml2sql not found; run: make setup-tools"
  [[ -f "$DBML_INPUT" ]] || die "DBML input not found: $DBML_INPUT"
  mkdir -p "$(dirname "$SCHEMA_OUTPUT")"

  log "Converting $DBML_INPUT -> $SCHEMA_OUTPUT"
  # dbml2sql exits 0 even on parse errors, so validate the output instead.
  local tmp
  tmp="$(mktemp)"
  if ! dbml2sql --postgres "$DBML_INPUT" -o "$tmp" || ! grep -q 'CREATE TABLE' "$tmp"; then
    rm -f "$tmp"
    die "dbml2sql failed to convert $DBML_INPUT (see messages above)"
  fi
  mv "$tmp" "$SCHEMA_OUTPUT"
}

# ---------------------------------------------------------------------------
# 3. Initial dbmate migration
# ---------------------------------------------------------------------------

# Prints DROP statements for every table/type in $1, in reverse creation order.
down_statements() {
  grep -oE '^CREATE (TABLE|TYPE) +("[^"]+"|[A-Za-z0-9_.]+)' "$1" \
    | awk '{ print $2, $3 }' \
    | awk '{ l[NR] = $0 } END { for (i = NR; i > 0; i--) print l[i] }' \
    | while read -r kind name; do
        echo "DROP $kind IF EXISTS $name CASCADE;"
      done
}

# Prints the body of the "-- migrate:up" block of migration file $1.
up_block() {
  awk '/^-- migrate:up/ {f=1; next} /^-- migrate:down/ {f=0} f' "$1"
}

step_migration() {
  [[ -s "$SCHEMA_OUTPUT" ]] || die "$SCHEMA_OUTPUT is missing or empty; run: make generate-schema"
  mkdir -p "$MIGRATIONS_DIR"

  local existing
  existing="$(find "$MIGRATIONS_DIR" -maxdepth 1 -name "*_${INITIAL_MIGRATION_NAME}.sql" | sort | head -n1)"
  if [[ -n "$existing" ]]; then
    log "Initial migration already exists: $existing (left untouched)"
    # Ignore blank lines and comments (dbml2sql stamps a "Generated at" header).
    if ! diff -q <(up_block "$existing" | sed '/^$/d; /^--/d') <(sed '/^$/d; /^--/d' "$SCHEMA_OUTPUT") >/dev/null; then
      warn "$SCHEMA_OUTPUT differs from the initial migration; add the change with: make migrate-new NAME=<change>"
    fi
    return
  fi

  has dbmate || die "dbmate not found; run: make setup-tools"
  log "Creating dbmate migration '$INITIAL_MIGRATION_NAME'"
  dbmate --migrations-dir "$MIGRATIONS_DIR" new "$INITIAL_MIGRATION_NAME" >/dev/null

  local file
  file="$(find "$MIGRATIONS_DIR" -maxdepth 1 -name "*_${INITIAL_MIGRATION_NAME}.sql" | sort | head -n1)"
  [[ -n "$file" ]] || die "dbmate did not create the migration file in $MIGRATIONS_DIR"

  {
    echo "-- migrate:up"
    cat "$SCHEMA_OUTPUT"
    echo
    echo "-- migrate:down"
    down_statements "$SCHEMA_OUTPUT"
  } >"$file"
  log "Wrote schema into $file"
}

# ---------------------------------------------------------------------------
# 4. Apply
# ---------------------------------------------------------------------------

step_up() {
  has dbmate || die "dbmate not found; run: make setup-tools"
  log "Applying migrations (dbmate up)"
  # shellcheck disable=SC2086 # DBMATE_FLAGS is intentionally word-split.
  dbmate --migrations-dir "$MIGRATIONS_DIR" $DBMATE_FLAGS up
}

case "${1:-all}" in
  tools)     step_tools ;;
  schema)    step_schema ;;
  migration) step_migration ;;
  up)        step_up ;;
  all)       step_tools; step_schema; step_migration; step_up ;;
  *) die "unknown step '$1' (expected: all|tools|schema|migration|up)" ;;
esac
