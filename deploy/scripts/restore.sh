#!/usr/bin/env bash
# 恢复脚本（docs/DEPLOYMENT.md §8.3）。用法: deploy/scripts/restore.sh backups/<日期时间>
# 前提：目标数据库已由实例管理方重建或清空为空库。
set -euo pipefail
cd "$(dirname "$0")/../.."

DIR="${1:?用法: restore.sh backups/<日期时间>}"
[ -f "$DIR/db.sql.gz" ] || { echo "缺少 $DIR/db.sql.gz"; exit 1; }
[ -f "$DIR/uploads.tar.gz" ] || { echo "缺少 $DIR/uploads.tar.gz"; exit 1; }

if [ -f .env ]; then
  set -a
  # shellcheck disable=SC1091
  . ./.env
  set +a
fi

: "${DB_HOST:?缺少 DB_HOST}"
: "${DB_USER:?缺少 DB_USER}"
: "${DB_NAME:?缺少 DB_NAME}"
: "${DB_PASSWORD:?缺少 DB_PASSWORD}"

echo "[1/3] 停止应用容器（避免恢复期间写入）"
docker compose stop app || true

echo "[2/3] 恢复数据库"
gunzip -c "$DIR/db.sql.gz" | docker run --rm -i -e PGPASSWORD="$DB_PASSWORD" postgres:16-alpine \
  psql -h "$DB_HOST" -p "${DB_PORT:-5432}" -U "$DB_USER" -d "$DB_NAME" -v ON_ERROR_STOP=1

echo "[3/3] 恢复附件目录"
rm -rf deploy/data/uploads
tar -xzf "$DIR/uploads.tar.gz" -C deploy/data

echo "恢复完成。请启动应用并检查 /readyz：docker compose start app"
