#!/usr/bin/env bash
# One-off seed: saves 8 reusable components (one select-data and one
# autocomplete-data per /fake/* endpoint) through POST /custom-components.
#
# Requires a running API (make dev / make dev-sqlite) reachable at API_URL,
# authenticated with HTTP Basic using the admin account created at startup.
#
# Usage: scripts/seed_custom_components.sh
set -Eeuo pipefail

API_URL="${API_URL:-http://localhost:8080}"
AUTH_USERNAME="${AUTH_ADMIN_USERNAME:-admin}"
AUTH_PASSWORD="${AUTH_ADMIN_PASSWORD:-change-me-now}"

log() { echo "[seed_custom_components] $*" >&2; }

create_component() {
  local name="$1" field_json="$2"
  local body
  body="$(jq -n --arg name "$name" --argjson field_json "$field_json" '{name: $name, field_json: $field_json}')"
  local response status
  response="$(curl -sS -o /tmp/seed_custom_components.out -w '%{http_code}' \
    -u "$AUTH_USERNAME:$AUTH_PASSWORD" \
    -H 'Content-Type: application/json' \
    -d "$body" \
    "$API_URL/custom-components")"
  status="$response"
  if [[ "$status" != "201" ]]; then
    log "failed to create '$name' (HTTP $status): $(cat /tmp/seed_custom_components.out)"
    exit 1
  fi
  log "created '$name'"
}

data_source_field() {
  local type="$1" name="$2" label="$3" url="$4" label_key="$5" value_key="$6"
  if [[ "$type" == "autocomplete-data" ]]; then
    jq -n --arg type "$type" --arg name "$name" --arg field_label "$label" --arg url "$url" \
      --arg label_key "$label_key" --arg value_key "$value_key" '{
        "type": $type, "name": $name, "label": $field_label, "required": false,
        "data_source": { "url": $url, "label_key": $label_key, "value_key": $value_key, "search_param": "q" },
        "min_chars": 2,
      }'
  else
    jq -n --arg type "$type" --arg name "$name" --arg field_label "$label" --arg url "$url" \
      --arg label_key "$label_key" --arg value_key "$value_key" '{
        "type": $type, "name": $name, "label": $field_label, "required": false,
        "data_source": { "url": $url, "label_key": $label_key, "value_key": $value_key },
      }'
  fi
}

# collection | endpoint | label_key | value_key
collections=(
  "Anime|/fake/animes|name|id"
  "Car|/fake/cars|name|id"
  "Person|/fake/people|name|email"
  "City|/fake/cities|name|id"
)

for entry in "${collections[@]}"; do
  IFS='|' read -r label endpoint label_key value_key <<<"$entry"
  slug="$(echo "$label" | tr '[:upper:]' '[:lower:]')"

  select_json="$(data_source_field "select-data" "${slug}" "$label" "$endpoint" "$label_key" "$value_key")"
  create_component "$label select" "$select_json"

  autocomplete_json="$(data_source_field "autocomplete-data" "${slug}_search" "$label (search)" "$endpoint" "$label_key" "$value_key")"
  create_component "$label autocomplete" "$autocomplete_json"
done

log "done"
