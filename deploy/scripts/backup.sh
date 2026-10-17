#!/usr/bin/env bash
# 备份脚本（docs/DEPLOYMENT.md §8.2）：
#   1) 对外部 PostgreSQL 实例执行 pg_dump（一次性 postgres 容器，宿主机无需安装客户端）
#   2) 打包本部署的附件目录
#   3) 单独保存 ENCRYPTION_KEY（模型 API Key 的加密密钥），恢复时必须与原值一致
# 注意：AI 调用记录等业务数据都在数据库中，随 pg_dump 一并备份；
#       应用日志不参与业务备份，由部署方按日期目录自行清理。
#
# 保留份数：脚本不自动删除旧备份。建议保留最近 7 份，由部署方另行归档或清理，
#   例如按 backups/ 下的日期目录判断。cron 示例（每日 03:00 执行）：
#   0 3 * * * cd /srv/timeline && deploy/scripts/backup.sh >> deploy/data/logs/backup.log 2>&1
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

# PostgreSQL 客户端镜像（pg_dump/psql 版本随该镜像）。默认 postgres:17-alpine；
# 目标数据库主版本高于客户端时需改为对应主版本，否则 pg_dump 会拒绝导出。
PG_CLIENT_IMAGE="${PG_CLIENT_IMAGE:-postgres:17-alpine}"

DATE=$(date +%Y%m%d-%H%M%S)
DIR="backups/$DATE"
mkdir -p "$DIR"
chmod 700 "$DIR" # 备份含用户数据与密钥，仅允许部署账号访问

echo "[1/3] 导出数据库到 $DIR/db.sql.gz（客户端镜像 $PG_CLIENT_IMAGE）"
# 若 DB_HOST 是宿主机地址，视环境在 docker run 中增加 --network host
docker run --rm -e PGPASSWORD="$DB_PASSWORD" "$PG_CLIENT_IMAGE" \
  pg_dump -h "$DB_HOST" -p "${DB_PORT:-5432}" -U "$DB_USER" -d "$DB_NAME" \
  | gzip > "$DIR/db.sql.gz"

echo "[2/3] 打包附件目录到 $DIR/uploads.tar.gz"
tar -czf "$DIR/uploads.tar.gz" -C deploy/data uploads

echo "[3/3] 保存 ENCRYPTION_KEY 到 $DIR/encryption-key.txt（权限 600）"
# 使用重定向写入而不回显，避免密钥进入终端历史、日志或 CI 输出。
( umask 077; printf '%s' "${ENCRYPTION_KEY:-}" > "$DIR/encryption-key.txt" )
chmod 600 "$DIR/encryption-key.txt"
if [ -z "${ENCRYPTION_KEY:-}" ]; then
  echo "警告：未配置 ENCRYPTION_KEY，encryption-key.txt 为空。" >&2
  echo "当前模型密钥由数据库连接信息派生；恢复时必须使用相同的数据库连接信息，或显式设置相同的 ENCRYPTION_KEY。" >&2
fi

echo "[校验] 检查备份产物完整"
for name in db.sql.gz uploads.tar.gz encryption-key.txt; do
  [ -f "$DIR/$name" ] || { echo "错误：缺少备份产物 $DIR/$name" >&2; exit 1; }
done
[ -s "$DIR/db.sql.gz" ] || { echo "错误：$DIR/db.sql.gz 为空" >&2; exit 1; }
[ -s "$DIR/uploads.tar.gz" ] || { echo "错误：$DIR/uploads.tar.gz 为空" >&2; exit 1; }
gzip -t "$DIR/db.sql.gz"
tar -tzf "$DIR/uploads.tar.gz" >/dev/null
chmod 600 "$DIR/db.sql.gz" "$DIR/uploads.tar.gz"

echo "备份完成: $DIR"
echo "注意：备份包含用户数据与 ENCRYPTION_KEY，请同步到安全存储并限制访问。"
