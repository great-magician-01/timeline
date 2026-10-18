package server

import (
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/web"
)

// registerFrontend 注册前端托管与 SPA 回退；未构建时提供内置占位页。
//
// /api 的未知路径：数据库/迁移未就绪时返回 503 service_unavailable（与业务路由的
// Ready 中间件语义一致，避免启动期把"尚未就绪"误报成"接口不存在"）；就绪后返回 404。
func (s *Server) registerFrontend() {
	dist, built := web.Dist()
	if !built {
		s.log.Warn("前端产物未构建（web/dist 无 index.html），使用内置占位页")
		s.engine.NoRoute(func(c *gin.Context) {
			if isAPIRequest(c.Request.URL.Path) {
				s.handleUnknownAPI(c)
				return
			}
			c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(placeholderHTML))
		})
		return
	}
	s.engine.GET("/", func(c *gin.Context) { serveIndex(c, dist) })
	s.engine.NoRoute(func(c *gin.Context) {
		p := strings.TrimPrefix(path.Clean("/"+c.Request.URL.Path), "/")
		if isAPIRequest(p) {
			s.handleUnknownAPI(c)
			return
		}
		if f, err := fs.Stat(dist, p); err == nil && !f.IsDir() {
			c.FileFromFS(c.Request.URL.Path, http.FS(dist))
			return
		}
		serveIndex(c, dist)
	})
}

// handleUnknownAPI 处理未注册的 /api 路径：未就绪 503，就绪后 404。
func (s *Server) handleUnknownAPI(c *gin.Context) {
	if s.readiness == nil || !s.readiness.Ready() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{
			"code": "service_unavailable", "message": "服务尚未就绪，请稍后重试",
		}})
		return
	}
	notFoundAPI(c)
}

func isAPIRequest(p string) bool {
	cleaned := strings.TrimPrefix(path.Clean("/"+p), "/")
	return cleaned == "api" || strings.HasPrefix(cleaned, "api/")
}

func notFoundAPI(c *gin.Context) {
	c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"code": "not_found", "message": "接口不存在"}})
}

func serveIndex(c *gin.Context, dist fs.FS) {
	index, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		c.String(http.StatusInternalServerError, "index.html 缺失")
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", index)
}

const placeholderHTML = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><title>时间线</title></head>
<body style="font-family:sans-serif;padding:2rem">
<h1>时间线</h1>
<p>前端尚未构建（web/dist 为空）。开发模式请运行 <code>cd web && npm run dev</code>，或在 web 目录执行 <code>npm run build</code> 后重启本服务。</p>
<p>后端状态：<a href="/healthz">/healthz</a> · <a href="/readyz">/readyz</a></p>
</body></html>`
