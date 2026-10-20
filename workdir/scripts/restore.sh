#!/usr/bin/env bash
# Restores a pg_dump (custom format) made by scripts/backup.sh into the
# production database of docker-compose.prod.yml. Run it on the Docker host:
#
#   scripts/restore.sh backups/app_db-20261020T030000Z.dump   # local dump
#   scripts/restore.sh app_db-20261020T030000Z.dump            # in BACKUP_DIR, else S3
#   scripts/restore.sh s3://bucket/prefix/app_db-....dump      # from S3
#
# Steps: fetch the dump (from S3 through the backup service if it is not
# local), stop the app, pg_restore --clean --if-exists into the database,
# apply the migrations newer than the dump (migrate service) and start the
# app again. Pass -y to skip the confirmation. ENV_FILE (default .env.prod)
# and COMPOSE_FILE (default docker-compose.prod.yml) select the stack.
set -euo pipefail

cd "$(dirname "$0")/.."

ENV_FILE=${ENV_FILE:-.env.prod}
COMPOSE_FILE=${COMPOSE_FILE:-docker-compose.prod.yml}
compose() { docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" "$@"; }

yes=0
if [ "${1:-}" = "-y" ]; then
	yes=1
	shift
fi
src=${1:?usage: scripts/restore.sh [-y] <dump file | name in BACKUP_DIR | s3://bucket/key>}

# BACKUP_DIR as mounted by the backup service (host side).
backup_dir=$(grep -E '^BACKUP_DIR=' "$ENV_FILE" 2>/dev/null | tail -n1 | cut -d= -f2- || true)
backup_dir=${backup_dir:-./backups}

# Dumps in BACKUP_DIR are owned by root (mode 600), so they are read through
# the backup service, which mounts it at /backups; other files from the host.
case "$src" in
s3://*) name="" ;;
*) name=$(basename "$src") ;;
esac
if [ -n "$name" ] && [ -e "$backup_dir/$name" ] &&
	{ [ "$src" = "$name" ] || [ "$(cd "$(dirname "$src")" && pwd)" = "$(cd "$backup_dir" && pwd)" ]; }; then
	dump=/backups/$name
elif [ -f "$src" ]; then
	dump=$src
else
	echo "fetching $src from S3..."
	dump=$(compose run --rm -T backup fetch "$src" | tail -n1)
fi

# read_dump writes the dump to stdout.
read_dump() {
	case "$dump" in
	/backups/*) compose run --rm -T --no-deps --entrypoint cat backup "$dump" ;;
	*) cat "$dump" ;;
	esac
}

echo "This replaces the contents of the production database with $dump."
if [ "$yes" -ne 1 ]; then
	read -r -p "Type 'restore' to continue: " answer
	[ "$answer" = "restore" ] || { echo "aborted"; exit 1; }
fi

echo "stopping the app..."
compose stop app

echo "restoring $dump..."
# pg_restore runs in the postgres container, reading the dump from stdin;
# the user and database come from its own environment.
compose exec -T postgres sh -c \
	'pg_restore --clean --if-exists --no-owner --exit-on-error -U "$POSTGRES_USER" -d "$POSTGRES_DB"' < <(read_dump)

echo "applying migrations..."
compose run --rm migrate

echo "starting the app..."
compose up -d --wait app
echo "restore done"
