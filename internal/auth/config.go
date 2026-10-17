package auth

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/settings"
)

// Config 处理 GET /api/v1/auth/config（公开接口，不要求登录与 CSRF）：
// 返回注册开关，供前端在注册关闭时隐藏注册入口。
// 设置每次请求即时读取；数据库不可用时回退代码内默认值（开放注册），
// 避免认证配置接口在启动早期不可用。
func (s *Service) Config(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	values := settings.LoadOrDefaults(c.Request.Context(), pool)
	c.JSON(http.StatusOK, gin.H{"registration_enabled": values.RegistrationEnabled})
}
