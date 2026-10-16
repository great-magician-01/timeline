#!/usr/bin/env bash
# 备份脚本（docs/DEPLOYMENT.md §8.2）：
#   1) 对外部 PostgreSQL 实例执行 pg_dump（一次性 postgres 容器，宿主机无需安装客户端）
#   2) 打包本部署的附件目录
# 注意：AI 调用记录、日志等业务数据都在数据库中，随 pg_dump 一并备份；
#       应用日志不参与业务备份，由部署方按日期目录自行清理。
set -euo pipefail
cd "$(dirname "$0")/../.." # 切到项目根目录

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

DATE=$(date +%Y%m%d-%H%M%S)
DIR="backups/$DATE"
mkdir -p "$DIR"

echo "[1/2] 导出数据库到 $DIR/db.sql.gz"
# 若 DB_HOST 是宿主机地址，视环境在 docker run 中增加 --network host
docker run --rm -e PGPASSWORD="$DB_PASSWORD" postgres:16-alpine \
  pg_dump -h "$DB_HOST" -p "${DB_PORT:-5432}" -U "$DB_USER" -d "$DB_NAME" \
  | gzip > "$DIR/db.sql.gz"

echo "[2/2] 打包附件目录到 $DIR/uploads.tar.gz"
tar -czf "$DIR/uploads.tar.gz" -C deploy/data uploads

echo "备份完成: $DIR"
