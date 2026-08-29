#!/usr/bin/env bash
# 每日备份 SQLite 与附件，保留 14 天。crontab: 20 4 * * * /usr/local/bin/relais-backup.sh
set -euo pipefail
DATA_DIR=/var/lib/relais
BACKUP_DIR=/var/backups/relais
mkdir -p "$BACKUP_DIR"
day=$(date +%F)
sqlite3 "$DATA_DIR/relais.db" ".backup '$BACKUP_DIR/relais-$day.db'"
tar -czf "$BACKUP_DIR/files-$day.tgz" -C "$DATA_DIR" --exclude=relais.db --exclude='relais.db-*' .
find "$BACKUP_DIR" -type f -mtime +14 -delete
