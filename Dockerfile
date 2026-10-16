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
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web/dist web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/timeline ./cmd/server

# ---- 运行镜像 ----
# ffmpeg：视频封面提取（后台任务）；tzdata：时区解析兜底（应用已内嵌 tzdata）
FROM alpine:3.20
RUN apk add --no-cache ffmpeg tzdata ca-certificates \
    && adduser -D -u 10001 app
ENV TZ=Asia/Shanghai
WORKDIR /app
COPY --from=build /out/timeline /app/timeline
USER app
EXPOSE 5070
ENTRYPOINT ["/app/timeline"]
