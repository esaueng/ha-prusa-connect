#!/usr/bin/env bash
set -euo pipefail

archive=${1:?Usage: $0 <app archive>}
work=$(mktemp -d)
project=ha-prusa-connect-check-$$
compose() {
  docker compose --project-name "$project" --file "$work/compose.yaml" "$@"
}
cleanup() {
  if [ "$?" -ne 0 ]; then
    compose ps -a >&2 || true
    compose logs --no-color | grep -E ' \| (cp:|install:|setpriv:)' >&2 || true
  fi
  compose down --volumes >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT

tar -xzf "$archive" -C "$work"
# Copying the app over Samba can remove the binary's execute bit.
chmod 0644 "$work/ha-prusa-connect/rootfs/prusa_connect"
cat > "$work/compose.yaml" <<'YAML'
services:
  ha-prusa-connect:
    build: ./ha-prusa-connect
    init: true
    ports:
      - "127.0.0.1::8080"
    volumes:
      - home-assistant-data:/data
volumes:
  home-assistant-data:
YAML

start_app() {
  compose up -d "$@" >&2
  address=$(compose port ha-prusa-connect 8080)
  origin=http://$address
}

wait_for_setup() {
  local state=$1
  for _ in $(seq 1 60); do
    if curl -fsS --max-time 2 "$origin/api/v1/setup" 2>/dev/null | jq -e --arg state "$state" '.state == $state' >/dev/null; then
      return
    fi
    sleep 1
  done
  echo "The app did not reach setup state $state." >&2
  return 1
}

start_app --build
wait_for_setup pending
curl -fsS "$origin/" | grep -F '<html' >/dev/null
curl -fsS "$origin/api/v1/health" | jq -ec 'select(.status == "ok" and (.version | length > 0)) | {status,version}'
compose exec -T ha-prusa-connect setpriv --reuid=prusa --regid=prusa --clear-groups \
  test -r /etc/ssl/certs/ca-certificates.crt
# shellcheck disable=SC2016 # Expand process paths inside the container.
compose exec -T ha-prusa-connect sh -ec '
  for process in /proc/[0-9]*; do
    if grep -qx prusa_connect "$process/comm" 2>/dev/null; then
      grep -Eq "^Uid:[[:space:]]+10001[[:space:]]" "$process/status"
      exit
    fi
  done
  exit 1
'

code=$(compose logs --no-color | sed -n 's/.*setup_code=\([A-Z0-9-]*\).*/\1/p' | tail -n 1)
test -n "$code"
password=$(openssl rand -hex 18)
login=$(jq -nc --arg password "$password" '{username:"app-smoke",password:$password}')
jq -nc --arg code "$code" --argjson account "$login" \
  '{code:$code,mode:"required",account:$account,admission:"ask",storage:{root:"./storage"}}' |
  curl -fsS -H 'Content-Type: application/json' --data-binary @- "$origin/api/v1/setup" >/dev/null
wait_for_setup 'done'

sign_in() {
  curl -fsS -c "$work/cookies" -H 'Content-Type: application/json' \
    --data-binary "$login" "$origin/api/v1/auth/login" >/dev/null
}
sign_in
printf 'G28\n' > "$work/sample.gcode"
curl -fsS -b "$work/cookies" -T "$work/sample.gcode" \
  "$origin/api/v1/storages/local/content?path=sample.gcode" >/dev/null
curl -fsS -D "$work/printer-headers" -o /dev/null -H 'Content-Type: application/json' \
  --data-binary '{"sn":"APP-SMOKE","fingerprint":"APP-SMOKE-1","printer_type":"1.4.0","firmware":"6.4.0"}' \
  "$origin/p/register"
registration=$(awk 'tolower($1) == "code:" { sub(/\r$/, "", $2); print $2 }' "$work/printer-headers")
test -n "$registration"
curl -fsS -b "$work/cookies" -X POST "$origin/api/v1/registrations/$registration/approve" >/dev/null
printer_token() {
  curl -fsS -D - -o /dev/null -H "Code: $registration" "$origin/p/register" |
    awk 'tolower($1) == "token:" { sub(/\r$/, "", $2); print $2 }'
}
token=$(printer_token)
test -n "$token"

start_app --force-recreate
wait_for_setup 'done'
sign_in
curl -fsS -b "$work/cookies" "$origin/api/v1/storages/local/content?path=sample.gcode" -o "$work/reloaded.gcode"
cmp "$work/sample.gcode" "$work/reloaded.gcode"
test "$(printer_token)" = "$token"
echo "Administrator, uploaded file, and printer registration survived container replacement."
