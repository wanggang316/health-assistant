#!/usr/bin/env bash
# Activate a release tarball built by .github/workflows/server-deploy.yml.
# Runs on the VM as /opt/health-assistant/bin/activate.sh (§10.5).
#
# Args:
#   $1  path to the release tarball on this host
#   $2  release name (for example 20261123-010203-abc1234)

set -euo pipefail

TARBALL="${1:?tarball path required}"
RELEASE_NAME="${2:?release name required}"

ROOT=/opt/health-assistant
RELEASES_DIR="$ROOT/releases"
SHARED_DIR="$ROOT/shared"
BACKUP_DIR="$ROOT/backups"
RELEASE_DIR="$RELEASES_DIR/$RELEASE_NAME"
UNIT=health-assistant-api.service
PORT=3140
KEEP=5

log() { printf '\033[36m[activate]\033[0m %s\n' "$*"; }
err() { printf '\033[31m[activate]\033[0m %s\n' "$*" >&2; }

[[ -f "$TARBALL" ]] || { err "Tarball not found: $TARBALL"; exit 1; }
install -d -m 755 "$RELEASES_DIR" "$SHARED_DIR/data" "$BACKUP_DIR" "$ROOT/bin"

if command -v project-env-install >/dev/null; then
  log "Installing env from project-env (health-assistant/prod)"
  project-env-install health-assistant prod "$SHARED_DIR/.env"
fi
[[ -f "$SHARED_DIR/.env" ]] || { err "Missing $SHARED_DIR/.env"; exit 1; }

log "Extracting $TARBALL to $RELEASE_DIR"
mkdir -p "$RELEASE_DIR"
tar -xzf "$TARBALL" -C "$RELEASE_DIR"
[[ -x "$RELEASE_DIR/health-api" ]] || { err "health-api binary missing"; exit 1; }
[[ -f "$RELEASE_DIR/nutrition.db" ]] || { err "nutrition.db missing"; exit 1; }
EXPECTED_SHA=$(cat "$RELEASE_DIR/SHA")

run_api() {
  ( set -a; . "$SHARED_DIR/.env"; set +a
    export NUTRITION_DB_PATH="$RELEASE_DIR/nutrition.db"
    "$RELEASE_DIR/health-api" "$@" )
}

# Pre-migration copy (consistent snapshot), keep the newest $KEEP.
DB=$(set -a; . "$SHARED_DIR/.env"; echo "${DATABASE_PATH:-}")
if [[ -n "$DB" && -f "$DB" ]]; then
  log "Copying database before migration"
  sqlite3 "$DB" ".backup '$BACKUP_DIR/pre-$RELEASE_NAME.db'"
  # shellcheck disable=SC2012
  ls -1t "$BACKUP_DIR"/pre-*.db 2>/dev/null | tail -n +$((KEEP + 1)) | xargs -r rm -f
  # The privacy policy promises that backups expire within 30 days.
  find "$BACKUP_DIR" -name 'pre-*.db' -mtime +30 -delete
fi

log "Running migrations"
run_api migrate

if ! cmp -s "$RELEASE_DIR/deploy/$UNIT" "/etc/systemd/system/$UNIT" 2>/dev/null; then
  log "Updating systemd unit"
  install -m 644 "$RELEASE_DIR/deploy/$UNIT" "/etc/systemd/system/$UNIT"
  systemctl daemon-reload
  systemctl enable "$UNIT" >/dev/null 2>&1 || true
fi

CADDY_RELOAD=0
csrc="$RELEASE_DIR/deploy/health-assistant.caddy"
cdst=/etc/caddy/conf.d/health-assistant.caddy
if ! cmp -s "$csrc" "$cdst" 2>/dev/null; then
  CADDY_LOG=/var/log/caddy/health-api.gumpw.com.log
  if [[ -d /var/log/caddy && ! -e "$CADDY_LOG" ]]; then
    own=$(stat -c '%U:%G' /var/log/caddy 2>/dev/null || echo caddy:caddy)
    install -o "${own%:*}" -g "${own#*:}" -m 600 /dev/null "$CADDY_LOG" 2>/dev/null || true
  fi
  old=$(mktemp)
  [[ -f "$cdst" ]] && cp "$cdst" "$old"
  log "Installing Caddy snippet"
  install -m 644 "$csrc" "$cdst"
  if caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile >/dev/null 2>&1; then
    CADDY_RELOAD=1
  else
    err "Caddy config failed validation; restoring the previous snippet"
    if [[ -s "$old" ]]; then cp "$old" "$cdst"; else rm -f "$cdst"; fi
  fi
  rm -f "$old"
fi

PREVIOUS=$(readlink -f "$ROOT/current" 2>/dev/null || true)
log "Switching current -> $RELEASE_NAME"
ln -sfn "$RELEASE_DIR" "$ROOT/current"
systemctl restart "$UNIT"
[[ "$CADDY_RELOAD" == "1" ]] && systemctl reload caddy.service

log "Waiting for /healthz with version $EXPECTED_SHA"
ok=0
for _ in $(seq 1 30); do
  if curl -fsS --max-time 2 "http://127.0.0.1:$PORT/healthz" 2>/dev/null | grep -q "\"version\":\"$EXPECTED_SHA\""; then
    ok=1; break
  fi
  sleep 1
done
if [[ "$ok" != "1" ]]; then
  err "Health check failed"
  journalctl -u "$UNIT" -n 30 --no-pager || true
  if [[ -n "$PREVIOUS" && -d "$PREVIOUS" ]]; then
    err "Rolling back to $PREVIOUS"
    ln -sfn "$PREVIOUS" "$ROOT/current"
    systemctl restart "$UNIT"
  fi
  exit 1
fi
log "Health OK"

cd "$RELEASES_DIR"
# shellcheck disable=SC2012
ls -1dt */ 2>/dev/null | tail -n +$((KEEP + 1)) | xargs -r rm -rf
rm -f "$TARBALL"

if ! cmp -s "$RELEASE_DIR/deploy/activate.sh" "$ROOT/bin/activate.sh" 2>/dev/null; then
  install -m 755 "$RELEASE_DIR/deploy/activate.sh" "$ROOT/bin/activate.sh"
fi
log "Activated $RELEASE_NAME ($EXPECTED_SHA)"
