# syntax=docker/dockerfile:1
# 多阶段构建（docs/DEPLOYMENT.md §3.1）：前端构建 → 后端编译（嵌入前端产物）→ 精简运行镜像。

# ---- 前端构建 ----
FROM node:22-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

# ---- 后端构建 ----
FROM golang:1.25-alpine AS build
# 版本号由 compose build.args 传入（默认 IMAGE_TAG），通过 -X 注入 main.appVersion；
# 未传入时保持 "dev"，不阻断本地构建。
ARG APP_VERSION=dev
# Go 模块代理可在构建时覆盖（受限网络使用镜像，如 https://goproxy.cn,direct）；
# 默认保持官方代理，不改变常规 CI/部署行为。
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web/dist web/dist
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.appVersion=${APP_VERSION}" \
    -o /out/timeline ./cmd/server

# ---- 运行镜像 ----
# ffmpeg：视频封面提取（后台任务）；tzdata：时区解析兜底（应用已内嵌 tzdata）
# /data/uploads、/data/logs 在镜像内预创建并交给 UID/GID 10001，避免绑定挂载目录缺失或属主错误。
FROM alpine:3.20
RUN apk add --no-cache ffmpeg tzdata ca-certificates \
    && addgroup -g 10001 -S app \
    && adduser -D -u 10001 -G app app \
    && mkdir -p /data/uploads /data/logs \
    && chown -R 10001:10001 /data
ENV TZ=Asia/Shanghai
WORKDIR /app
COPY --from=build /out/timeline /app/timeline
USER app
EXPOSE 5070
ENTRYPOINT ["/app/timeline"]
