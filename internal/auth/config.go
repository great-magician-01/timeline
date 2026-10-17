package auth

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/settings"
)

// Config 处理 GET /api/v1/auth/config（公开接口，不要求登录与 CSRF）：
// 返回注册开关与初始化状态，供前端在注册关闭时隐藏注册入口，
// 并在系统尚未初始化（无人类用户）时提示首个注册账号将成为超级管理员。
// 设置每次请求即时读取；数据库不可用时回退代码内默认值（开放注册），
// 避免认证配置接口在启动早期不可用。
func (s *Service) Config(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	values := settings.LoadOrDefaults(c.Request.Context(), pool)
	bootstrap, err := BootstrapRequired(c.Request.Context(), pool)
	if err != nil {
		// 初始化状态只用于前端提示，查询失败时回退 false，不阻塞公开配置读取。
		bootstrap = false
		s.log.Warn("读取初始化状态失败", "err", err)
	}
	c.JSON(http.StatusOK, gin.H{
		"registration_enabled": values.RegistrationEnabled,
		"bootstrap_required":   bootstrap,
	})
}
