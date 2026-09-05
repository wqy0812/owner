#!/usr/bin/env bash
# Remote preparation only. Leaves the service stopped for copying and deployment.
# No live DB rows are changed, and no complete Run-bearing DB is copied.
set -Eeuo pipefail
umask 077
bundle="$1"
tool="$2"
packager="$3"
[[ "$bundle" =~ ^/var/lib/clusterforge/business-reset-backups/[A-Za-z0-9_-]+$ ]]
[[ ! -e "$bundle" ]]
exec 9>/var/lock/clusterforge-platform-deploy.lock
flock -n 9
exec 8>/var/lib/clusterforge/catalog-backups/.backup.lock
flock -n 8
[[ -z "$("$tool" database active-work --db /var/lib/clusterforge/platform.db)" ]]
systemctl is-active --quiet clusterforge-platform
mkdir -p -m 0700 "$bundle"
cp -a /etc/clusterforge/platform.env "$bundle/platform.env"
cp -a /etc/systemd/system/clusterforge-platform.service "$bundle/clusterforge-platform.service"
cp -a /opt/clusterforge/platform/clusterforge-platform "$bundle/clusterforge-platform"
cp -a /opt/clusterforge/platform/clusterforge-backup "$bundle/clusterforge-backup"
for unit in clusterforge-backup.service clusterforge-backup.timer; do
  if [[ -f "/etc/systemd/system/$unit" ]]; then cp -a "/etc/systemd/system/$unit" "$bundle/$unit"; fi
done
# Disable before stopping, so even an interrupted preparation cannot restart an
# automatic full-history Catalog backup. Retain the old config in the bundle.
sed -i '/^CLUSTERFORGE_BACKUP_ENABLED=/d' /etc/clusterforge/platform.env
printf '\nCLUSTERFORGE_BACKUP_ENABLED=false\n' >> /etc/clusterforge/platform.env
trap 'systemctl start clusterforge-platform' ERR INT TERM
systemctl disable --now clusterforge-backup.timer >/dev/null 2>&1 || true
systemctl stop clusterforge-backup.service >/dev/null 2>&1 || true
systemctl stop clusterforge-platform
[[ -z "$("$tool" database active-work --db /var/lib/clusterforge/platform.db)" ]]
"$tool" database business-snapshot --db /var/lib/clusterforge/platform.db --target "$bundle/platform.db"
"$tool" database foundation-snapshot --db /var/lib/clusterforge/platform.db --target "$bundle/foundation.db"
"$tool" database verify-business --db "$bundle/platform.db"
"$tool" database verify-foundation --db "$bundle/foundation.db"
"$tool" database business-export --db "$bundle/platform.db" --target "$bundle/business.json"
sha256sum /var/lib/clusterforge/platform.db > "$bundle/source-database.sha256"
python3 "$packager" "$bundle"
(cd "$bundle" && sha256sum --quiet -c SHA256SUMS)
trap - ERR INT TERM
printf 'PREPARED=%s\nService is stopped; copy and verify the bundle before deployment.\n' "$bundle"
