#!/usr/bin/env bash
# 首次部署目录准备（docs/DEPLOYMENT.md §11）。
# 创建绑定挂载所需的宿主机目录，并把属主设置为应用容器内的非 root 用户（UID/GID 10001）。
# 可重复执行：已存在的目录只校正属主，不会清空数据。
set -euo pipefail
cd "$(dirname "$0")/../.." # 切到项目根目录

APP_UID=10001
APP_GID=10001
DIRS=(deploy/data/uploads deploy/data/logs)

for dir in "${DIRS[@]}"; do
  mkdir -p "$dir"
done

if [ "$(id -u)" -eq 0 ]; then
  chown -R "${APP_UID}:${APP_GID}" "${DIRS[@]}"
  echo "已创建并授权: ${DIRS[*]}（属主 ${APP_UID}:${APP_GID}）"
else
  if chown -R "${APP_UID}:${APP_GID}" "${DIRS[@]}" 2>/dev/null; then
    echo "已创建并授权: ${DIRS[*]}（属主 ${APP_UID}:${APP_GID}）"
  else
    echo "警告：当前用户不是 root，未改变目录属主。" >&2
    echo "请执行：sudo chown -R ${APP_UID}:${APP_GID} ${DIRS[*]}" >&2
    echo "否则容器内 UID ${APP_UID} 无法写入上传目录和日志目录。" >&2
  fi
fi

echo "目录准备完成。下一步：cp .env.example .env 并填写数据库连接信息。"
