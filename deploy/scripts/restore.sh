#!/usr/bin/env bash
# 恢复脚本（docs/DEPLOYMENT.md §8.3）。
# 用法: deploy/scripts/restore.sh backups/<日期时间>
#
# 前提：目标数据库已由实例管理方重建或清空为空库；脚本会尝试停止应用容器。
# 通过环境变量 RESTORE_YES=1 可跳过交互确认（仅建议在无人值守的恢复演练中使用）。
set -euo pipefail
cd "$(dirname "$0")/../.." # 切到项目根目录

DIR="${1:?用法: restore.sh backups/<日期时间>}"
[ -f "$DIR/db.sql.gz" ] || { echo "缺少 $DIR/db.sql.gz" >&2; exit 1; }
[ -f "$DIR/uploads.tar.gz" ] || { echo "缺少 $DIR/uploads.tar.gz" >&2; exit 1; }
[ -s "$DIR/db.sql.gz" ] || { echo "$DIR/db.sql.gz 为空" >&2; exit 1; }
[ -s "$DIR/uploads.tar.gz" ] || { echo "$DIR/uploads.tar.gz 为空" >&2; exit 1; }

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
# 客户端主版本低于目标数据库时无法可靠恢复，脚本会中止并提示修改该变量。
PG_CLIENT_IMAGE="${PG_CLIENT_IMAGE:-postgres:17-alpine}"

# 附件目录固定为 deploy/data/uploads；不使用用户输入拼接删除/移动路径。
UPLOAD_DIR="deploy/data/uploads"
UPLOAD_PARENT="deploy/data"

echo "[1/5] 检查目标数据库版本"
SERVER_VERSION_NUM=$(docker run --rm -e PGPASSWORD="$DB_PASSWORD" "$PG_CLIENT_IMAGE" \
  psql -h "$DB_HOST" -p "${DB_PORT:-5432}" -U "$DB_USER" -d "$DB_NAME" \
  -Atc 'SHOW server_version_num') || {
  echo "错误：无法连接目标数据库 ${DB_NAME}@${DB_HOST}:${DB_PORT:-5432}。" >&2
  echo "请确认实例已就绪，且数据库与账号已按部署清单创建。" >&2
  exit 1
}
CLIENT_VERSION=$(docker run --rm --entrypoint psql "$PG_CLIENT_IMAGE" --version | awk '{print $3}')
CLIENT_MAJOR=${CLIENT_VERSION%%.*}
case "$SERVER_VERSION_NUM" in
  ''|*[!0-9]*) echo "错误：无法解析目标数据库版本（$SERVER_VERSION_NUM）。" >&2; exit 1 ;;
esac
case "$CLIENT_MAJOR" in
  ''|*[!0-9]*) echo "错误：无法解析客户端版本（$CLIENT_VERSION）。" >&2; exit 1 ;;
esac
SERVER_MAJOR=$((SERVER_VERSION_NUM / 10000))
if [ "$CLIENT_MAJOR" -lt "$SERVER_MAJOR" ]; then
  echo "错误：PG_CLIENT_IMAGE=$PG_CLIENT_IMAGE（PostgreSQL $CLIENT_MAJOR）低于目标数据库版本（$SERVER_MAJOR）。" >&2
  echo "请改用不低于目标库主版本的客户端镜像，例如 PG_CLIENT_IMAGE=postgres:${SERVER_MAJOR}-alpine。" >&2
  exit 1
fi
echo "目标库版本 $SERVER_MAJOR，客户端版本 $CLIENT_MAJOR，检查通过。"

# 若备份包含密钥，提示当前 ENCRYPTION_KEY 是否一致；只比较，不回显密钥。
if [ -f "$DIR/encryption-key.txt" ]]; then
  BACKUP_KEY=$(cat "$DIR/encryption-key.txt")
  if [ -n "$BACKUP_KEY" ] && [ "$BACKUP_KEY" != "${ENCRYPTION_KEY:-}" ]; then
    echo "警告：当前 ENCRYPTION_KEY 与备份中的 encryption-key.txt 不一致。" >&2
    echo "恢复后已保存的模型接口密钥将无法解密，需要在管理后台重新填写。" >&2
  fi
fi

echo "即将恢复数据库 ${DB_NAME}@${DB_HOST}:${DB_PORT:-5432}，并用备份覆盖 ${UPLOAD_DIR}。"
if [ "${RESTORE_YES:-0}" != "1" ]; then
  printf '输入 yes 继续: '
  read -r answer
  if [ "$answer" != "yes" ]; then
    echo "已取消恢复。"
    exit 1
  fi
fi

echo "[2/5] 停止应用容器（避免恢复期间写入）"
docker compose stop app || true

echo "[3/5] 恢复数据库"
gunzip -c "$DIR/db.sql.gz" | docker run --rm -i -e PGPASSWORD="$DB_PASSWORD" "$PG_CLIENT_IMAGE" \
  psql -h "$DB_HOST" -p "${DB_PORT:-5432}" -U "$DB_USER" -d "$DB_NAME" -v ON_ERROR_STOP=1

echo "[4/5] 恢复附件目录（先移走旧目录，确认恢复成功后再由部署方清理）"
mkdir -p "$UPLOAD_PARENT"
if [ -e "$UPLOAD_DIR" ]; then
  OLD_DIR="${UPLOAD_PARENT}/uploads.restore-old-$(date +%Y%m%d-%H%M%S)"
  mv "$UPLOAD_DIR" "$OLD_DIR"
  echo "旧附件目录已移动到 $OLD_DIR（不直接删除，便于回退）。"
fi
tar -xzf "$DIR/uploads.tar.gz" -C "$UPLOAD_PARENT"

echo "[5/5] 恢复附件属主与权限（容器内应用用户 UID/GID 10001）"
if [ "$(id -u)" -eq 0 ]; then
  chown -R 10001:10001 "$UPLOAD_DIR"
  find "$UPLOAD_DIR" -type d -exec chmod 755 {} +
  find "$UPLOAD_DIR" -type f -exec chmod 644 {} +
  echo "已设置 $UPLOAD_DIR 属主 10001:10001，目录 755、文件 644。"
elif command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
  # 非 root 部署账号也能通过一次性容器修正绑定挂载目录的属主，
  # 避免恢复后 UID 10001 无法读取或写入附件。
  if docker run --rm -v "$PWD/$UPLOAD_PARENT:/data" --entrypoint /bin/sh "$PG_CLIENT_IMAGE" -c \
    'chown -R 10001:10001 /data/uploads && find /data/uploads -type d -exec chmod 755 {} + && find /data/uploads -type f -exec chmod 644 {} +'; then
    echo "已通过一次性容器设置 $UPLOAD_DIR 属主 10001:10001，目录 755、文件 644。"
  else
    echo "警告：通过容器修正附件属主失败，请执行：" >&2
    echo "  sudo chown -R 10001:10001 $UPLOAD_DIR && sudo find $UPLOAD_DIR -type d -exec chmod 755 {} + && sudo find $UPLOAD_DIR -type f -exec chmod 644 {} +" >&2
  fi
else
  echo "提示：当前非 root 且无法访问 Docker，不能恢复附件属主与权限。请执行：" >&2
  echo "  sudo chown -R 10001:10001 $UPLOAD_DIR && sudo find $UPLOAD_DIR -type d -exec chmod 755 {} + && sudo find $UPLOAD_DIR -type f -exec chmod 644 {} +" >&2
  echo "否则容器内 UID 10001 可能无法读取或写入附件。" >&2
fi

echo "恢复完成。请启动应用并检查 /readyz：docker compose start app"
