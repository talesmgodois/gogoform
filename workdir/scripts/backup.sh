#!/bin/sh
# PostgreSQL backups of the production stack. Entrypoint of the `backup`
# service of docker-compose.prod.yml (postgres image + AWS CLI), so it is
# POSIX sh (busybox). Commands:
#
#   daemon       run `run` every day at BACKUP_TIME (HH:MM, UTC); the default
#   run          dump the database now, prune old dumps, upload to S3 if set
#   fetch <key>  download s3://$S3_BUCKET/<key> (or a full s3:// URL) into
#                BACKUP_DIR and print its local path (used by restore.sh)
#   list         list the local dumps and, with S3 configured, the remote ones
#
# By hand, from the directory of docker-compose.prod.yml:
#   docker compose --env-file .env.prod -f docker-compose.prod.yml run --rm backup run
#
# Environment: PGHOST, PGUSER, PGPASSWORD, PGDATABASE (libpq); BACKUP_DIR
# (default /backups); BACKUP_TIME (default 03:00); BACKUP_RETENTION_DAYS
# (default 14; local dumps only, expire remote ones with a bucket lifecycle
# rule); S3_BUCKET (empty: no upload), S3_PREFIX, S3_ENDPOINT_URL (for
# non-AWS providers) and the AWS_* credentials of the AWS CLI.
set -eu
# Dumps hold every tenant's data: readable by their owner (root) only.
umask 077

BACKUP_DIR=${BACKUP_DIR:-/backups}
BACKUP_TIME=${BACKUP_TIME:-03:00}
BACKUP_RETENTION_DAYS=${BACKUP_RETENTION_DAYS:-14}
S3_BUCKET=${S3_BUCKET:-}
S3_PREFIX=${S3_PREFIX:-}
S3_ENDPOINT_URL=${S3_ENDPOINT_URL:-}

log() {
	echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) backup: $*"
}

# s3 runs the AWS CLI against S3_ENDPOINT_URL when set.
s3() {
	if [ -n "$S3_ENDPOINT_URL" ]; then
		aws --endpoint-url "$S3_ENDPOINT_URL" s3 "$@"
	else
		aws s3 "$@"
	fi
}

# s3_url prints the s3:// URL of the object (or prefix) named $1 under
# S3_PREFIX.
s3_url() {
	prefix=${S3_PREFIX#/}
	prefix=${prefix%/}
	if [ -n "$prefix" ]; then
		echo "s3://$S3_BUCKET/$prefix/$1"
	else
		echo "s3://$S3_BUCKET/$1"
	fi
}

run_backup() {
	mkdir -p "$BACKUP_DIR"
	name="${PGDATABASE:-postgres}-$(date -u +%Y%m%dT%H%M%SZ).dump"
	tmp="$BACKUP_DIR/.$name.partial"
	log "dumping ${PGDATABASE:-postgres} to $BACKUP_DIR/$name"
	# Custom format: compressed, restorable selectively with pg_restore.
	if ! pg_dump --format=custom --no-owner --file="$tmp"; then
		rm -f "$tmp"
		log "pg_dump failed"
		return 1
	fi
	mv "$tmp" "$BACKUP_DIR/$name"
	log "dump done ($(du -h "$BACKUP_DIR/$name" | cut -f1))"

	find "$BACKUP_DIR" -maxdepth 1 -name '*.dump' -type f -mtime "+$BACKUP_RETENTION_DAYS" -print -delete |
		while read -r old; do log "pruned $old"; done

	if [ -n "$S3_BUCKET" ]; then
		s3 cp --only-show-errors "$BACKUP_DIR/$name" "$(s3_url "$name")"
		log "uploaded to $(s3_url "$name")"
	fi
}

# seconds_until prints the number of seconds until the next HH:MM (UTC).
seconds_until() {
	now=$(date -u +%s)
	target=$(date -u -d "$(date -u +%Y-%m-%d) $1:00" +%s)
	if [ "$target" -le "$now" ]; then
		target=$((target + 86400))
	fi
	echo $((target - now))
}

case "${1:-daemon}" in
daemon)
	log "scheduled daily at $BACKUP_TIME UTC, keeping $BACKUP_RETENTION_DAYS days${S3_BUCKET:+, uploading to $(s3_url '')}"
	while :; do
		wait_s=$(seconds_until "$BACKUP_TIME")
		log "next backup in ${wait_s}s"
		sleep "$wait_s"
		# A failed backup is logged and retried the next day; the service
		# keeps running.
		run_backup || log "backup failed"
	done
	;;
run)
	run_backup
	;;
fetch)
	key=${2:?usage: backup.sh fetch <key|s3://bucket/key>}
	[ -n "$S3_BUCKET" ] || case "$key" in s3://*) ;; *) echo "S3_BUCKET is not set" >&2; exit 1 ;; esac
	case "$key" in
	s3://*) url=$key ;;
	*) url=$(s3_url "$key") ;;
	esac
	mkdir -p "$BACKUP_DIR"
	s3 cp --only-show-errors "$url" "$BACKUP_DIR/$(basename "$url")" >&2
	echo "$BACKUP_DIR/$(basename "$url")"
	;;
list)
	ls -lh "$BACKUP_DIR"/*.dump 2>/dev/null || echo "no local dumps in $BACKUP_DIR"
	if [ -n "$S3_BUCKET" ]; then
		s3 ls "$(s3_url '')"
	fi
	;;
*)
	echo "usage: backup.sh [daemon|run|fetch <key>|list]" >&2
	exit 2
	;;
esac
