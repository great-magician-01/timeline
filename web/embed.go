// Package web 内嵌构建后的前端产物（web/dist）。
//
// 约定：dist 由 vite build 生成（Dockerfile 构建镜像时执行）；
// 尚未构建时 dist 仅含占位文件，服务端会退回内置占位页。
// SPA 路由回退不得吞掉 /api、/healthz、/readyz（见 docs/DEPLOYMENT.md）。
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist 返回 dist 子目录；built 表示已构建的前端入口（index.html）是否存在。
func Dist() (sub fs.FS, built bool) {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, false
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return sub, false
	}
	return sub, true
}
