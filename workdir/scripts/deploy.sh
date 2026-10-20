#!/usr/bin/env bash
# Deploys a new app image to the docker-compose.prod.yml stack. It is the
# command behind DEPLOY_WEBHOOK_URL (see deploy/webhook/), run on the Docker
# host, and can also be run by hand:
#
#   scripts/deploy.sh                                   # webhook mode, see below
#   scripts/deploy.sh ghcr.io/<owner>/<repo>@sha256:... # manual mode
#
# Webhook mode reads the JSON body POSTed by the deploy job of
# .github/workflows/deploy.yml from DEPLOY_PAYLOAD (or stdin) and its
# X-Hub-Signature-256 header from DEPLOY_SIGNATURE, and refuses to run unless
# the signature is the HMAC-SHA256 of the body keyed with
# DEPLOY_WEBHOOK_SECRET. The payload's image must be a sha256 digest of the
# repository already deployed (APP_IMAGE in ENV_FILE) or of DEPLOY_IMAGE_REPO.
#
# Steps: pin APP_IMAGE in ENV_FILE to the new image, `docker compose pull`,
# `up -d` (the migrate service applies pending migrations before the app
# starts) and wait up to HEALTH_TIMEOUT seconds (default 120) for the app's
# healthcheck. When any step fails, APP_IMAGE is put back and the previous
# image started again. Deploys are serialized with a lock file. ENV_FILE
# (default .env.prod) and COMPOSE_FILE (default docker-compose.prod.yml)
# select the stack. Needs docker, jq, openssl and flock.
set -euo pipefail

cd "$(dirname "$0")/.."

ENV_FILE=${ENV_FILE:-.env.prod}
COMPOSE_FILE=${COMPOSE_FILE:-docker-compose.prod.yml}
HEALTH_TIMEOUT=${HEALTH_TIMEOUT:-120}
compose() { docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" "$@"; }
log() { echo "deploy: $*" >&2; }
die() { log "error: $*"; exit 1; }

[ -f "$ENV_FILE" ] || die "$ENV_FILE not found"

# repo_of prints the repository of an image reference, without tag or digest.
repo_of() {
	local ref=${1%@*}
	case "${ref##*/}" in
	*:*) ref=${ref%:*} ;;
	esac
	printf '%s' "$ref" | tr '[:upper:]' '[:lower:]'
}

current=$(grep -E '^APP_IMAGE=' "$ENV_FILE" | tail -n1 | cut -d= -f2- || true)

if [ $# -gt 0 ]; then
	image=$1
else
	payload=${DEPLOY_PAYLOAD-$(cat)}
	secret=${DEPLOY_WEBHOOK_SECRET:-}
	[ -n "$secret" ] || die "DEPLOY_WEBHOOK_SECRET is not set"
	expected="sha256=$(printf '%s' "$payload" | openssl dgst -sha256 -hmac "$secret" | awk '{print $NF}')"
	[ "${DEPLOY_SIGNATURE:-}" = "$expected" ] || die "invalid X-Hub-Signature-256"

	repository=$(jq -er '.repository' <<<"$payload") || die "payload has no repository"
	digest=$(jq -er '.digest' <<<"$payload") || die "payload has no digest"
	[[ $digest =~ ^sha256:[0-9a-f]{64}$ ]] || die "invalid digest $digest"

	allowed=$(repo_of "${DEPLOY_IMAGE_REPO:-$current}")
	[ -n "$allowed" ] || die "set APP_IMAGE in $ENV_FILE (or DEPLOY_IMAGE_REPO) before the first webhook deploy"
	[ "$(repo_of "$repository")" = "$allowed" ] || die "repository $repository is not $allowed"
	image="$allowed@$digest"
	log "sha $(jq -r '.sha // "?"' <<<"$payload"), ref $(jq -r '.ref // "?"' <<<"$payload")"
fi

# set_image rewrites APP_IMAGE in ENV_FILE in place (keeping its owner and
# mode), appending it when missing.
set_image() {
	local tmp
	tmp=$(mktemp)
	grep -vE '^APP_IMAGE=' "$ENV_FILE" >"$tmp" || true
	echo "APP_IMAGE=$1" >>"$tmp"
	cat "$tmp" >"$ENV_FILE"
	rm -f "$tmp"
}

# wait_healthy waits for the app container's healthcheck to pass.
wait_healthy() {
	local deadline=$((SECONDS + HEALTH_TIMEOUT)) id status
	while [ $SECONDS -lt $deadline ]; do
		id=$(compose ps -q app)
		if [ -n "$id" ]; then
			status=$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$id")
			[ "$status" = "healthy" ] && return 0
		fi
		sleep 3
	done
	log "app not healthy after ${HEALTH_TIMEOUT}s (status: ${status:-missing})"
	return 1
}

exec 9>"${TMPDIR:-/tmp}/gogoform-deploy.lock"
flock 9

log "deploying $image (was ${current:-unset})"
set_image "$image"
if compose pull app migrate && compose up -d --remove-orphans && wait_healthy; then
	log "done"
	exit 0
fi

compose logs --tail=50 migrate app >&2 || true
if [ -n "$current" ] && [ "$current" != "$image" ]; then
	log "rolling back to $current"
	set_image "$current"
	if ! { compose up -d --remove-orphans && wait_healthy; }; then
		log "rollback is not healthy either"
	fi
fi
die "deploy of $image failed"
