package httpx

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/shared/model"
)

// 请求上下文键。新中间件（含 modules/auth/http.ResolveSession）统一用本包读写身份，
// 与旧 internal/auth 的上下文键不兼容；迁移期两套键可以并存，互不读取。
const (
	viewerContextKey  = "timeline.httpx.viewer"
	sessionContextKey = "timeline.httpx.session"
)

// SetViewer 把当前请求的访问者身份写入上下文。
func SetViewer(c *gin.Context, viewer model.Viewer) {
	c.Set(viewerContextKey, viewer)
}

// Viewer 读取当前请求的访问者身份；未写入时返回 false（游客与“未解析”都可返回 false）。
func Viewer(c *gin.Context) (model.Viewer, bool) {
	value, ok := c.Get(viewerContextKey)
	if !ok {
		return model.Viewer{}, false
	}
	viewer, ok := value.(model.Viewer)
	return viewer, ok
}

// RequireViewer 读取已登录访问者；缺失或未认证时写回 401 unauthenticated 并返回 false。
// 只读上下文、不查库：登录态是否有效由 ResolveSession 中间件保证。
func RequireViewer(c *gin.Context) (model.Viewer, bool) {
	viewer, ok := Viewer(c)
	if !ok || !viewer.Authenticated {
		Error(c, http.StatusUnauthorized, "unauthenticated", "请先登录")
		return model.Viewer{}, false
	}
	return viewer, true
}

// SetSession 把当前请求的会话写入上下文。
func SetSession(c *gin.Context, session model.Session) {
	c.Set(sessionContextKey, session)
}

// Session 读取当前请求的会话；未写入时返回 false。
func Session(c *gin.Context) (model.Session, bool) {
	value, ok := c.Get(sessionContextKey)
	if !ok {
		return model.Session{}, false
	}
	session, ok := value.(model.Session)
	return session, ok
}

// RequireSession 读取当前会话；缺失时写回 401 unauthenticated 并返回 false。
func RequireSession(c *gin.Context) (model.Session, bool) {
	session, ok := Session(c)
	if !ok {
		Error(c, http.StatusUnauthorized, "unauthenticated", "请先登录")
		return model.Session{}, false
	}
	return session, true
}
