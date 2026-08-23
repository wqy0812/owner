#!/usr/bin/env bash
set -Eeuo pipefail

PROJECT_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TARGET="${CLUSTERFORGE_FSS_DEPLOY_TARGET:-root@192.168.88.57}"
SSH_PORT="${CLUSTERFORGE_FSS_DEPLOY_SSH_PORT:-22}"
artifact="$(mktemp "${TMPDIR:-/tmp}/clusterforge-fss-linux-amd64.XXXXXX")"
unit="$(mktemp "${TMPDIR:-/tmp}/clusterforge-fss.service.XXXXXX")"
trap 'rm -f "$artifact" "$unit"' EXIT

cd "$PROJECT_ROOT"
go test ./internal/fss ./cmd/fss
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o "$artifact" ./cmd/fss
cp deploy/fss/clusterforge-fss.service "$unit"

checksum="$(shasum -a 256 "$artifact" | awk '{print $1}')"
remote_binary="/tmp/clusterforge-fss-${checksum:0:12}"
remote_unit="/tmp/clusterforge-fss.service-${checksum:0:12}"
ssh_options=(-o BatchMode=yes -o ConnectTimeout=8 -p "$SSH_PORT")
scp_options=(-o BatchMode=yes -o ConnectTimeout=8 -P "$SSH_PORT")

scp "${scp_options[@]}" "$artifact" "$TARGET:$remote_binary"
scp "${scp_options[@]}" "$unit" "$TARGET:$remote_unit"
ssh "${ssh_options[@]}" "$TARGET" bash -s -- "$remote_binary" "$remote_unit" "$checksum" <<'REMOTE_SCRIPT'
set -Eeuo pipefail
remote_binary="$1"
remote_unit="$2"
expected_checksum="$3"
live_binary="/usr/local/bin/clusterforge-fss"
live_unit="/etc/systemd/system/clusterforge-fss.service"
backup_root="/var/lib/clusterforge/fss-deploy-backups"

actual_checksum="$(sha256sum "$remote_binary" | awk '{print $1}')"
[[ "$actual_checksum" == "$expected_checksum" ]]
if ! id clusterforge-fss >/dev/null 2>&1; then
  useradd --system --home-dir /srv/clusterforge-fss --shell /usr/sbin/nologin clusterforge-fss
fi
install -d -o clusterforge-fss -g clusterforge-fss -m 0755 /srv/clusterforge-fss "$backup_root"
stamp="$(date -u +%Y%m%dT%H%M%SZ)-$$"
backup="$backup_root/$stamp"
mkdir -p "$backup"
[[ ! -f "$live_binary" ]] || cp -a "$live_binary" "$backup/"
[[ ! -f "$live_unit" ]] || cp -a "$live_unit" "$backup/"
install -m 0755 "$remote_binary" "$live_binary"
install -m 0644 "$remote_unit" "$live_unit"
rm -f "$remote_binary" "$remote_unit"
systemctl daemon-reload
systemctl enable clusterforge-fss
systemctl restart clusterforge-fss

ready=0
for _ in 1 2 3 4 5 6 7 8 9 10; do
  if systemctl is-active --quiet clusterforge-fss && curl --fail --silent --show-error http://127.0.0.1:8080/healthz >/dev/null; then
    ready=1
    break
  fi
  sleep 1
done
[[ "$ready" -eq 1 ]]
systemctl is-enabled clusterforge-fss
systemctl is-active clusterforge-fss
sha256sum "$live_binary"
curl --fail --silent --show-error http://127.0.0.1:8080/healthz
REMOTE_SCRIPT
