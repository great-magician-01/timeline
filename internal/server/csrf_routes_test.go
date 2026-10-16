package server

import (
	"net/http"
	"os"
	"strings"
	"testing"
)

// TestAllWriteRoutesRequireCSRF 遍历路由表中所有 /api/v1 的 POST 接口，
// 断言它们在缺少 CSRF 令牌时一律返回 403 csrf_failed。
// CSRF 中间件在业务 Handler 之前执行，因此路径参数可以用占位值。
// 这条用例覆盖“新增写接口忘记挂 CSRF”这类回归。
func TestAllWriteRoutesRequireCSRF(t *testing.T) {
	if os.Getenv("TIMELINE_INTEGRATION") != "1" {
		t.Skip("设置 TIMELINE_INTEGRATION=1 后运行测试 schema 集成测试")
	}
	_, srv, base := setupIntegrationServer(t)

	client := newIntegrationClient(t, base)
	registerIntegrationUser(t, client,
		"csrf_"+integrationSuffix(), "csrf-"+integrationSuffix()+"@example.com")
	// 管理接口在分组中间件里先做角色校验，因此必须用管理员会话才能走到 CSRF 校验。
	admin := newIntegrationClient(t, base)
	loginIntegrationUser(t, admin, "admin_integration", "admin-password-123")

	checked := 0
	for _, route := range srv.engine.Routes() {
		if route.Method != http.MethodPost || !strings.HasPrefix(route.Path, "/api/v1") {
			continue
		}
		// 登录/注册使用游客双提交 Cookie，同样必须拒绝缺少令牌的请求；
		// 这里统一验证总有一个 CSRF 校验拦住它们。
		path := substituteRouteParams(route.Path)
		caller := client
		if strings.HasPrefix(path, "/api/v1/admin/") {
			caller = admin
		}
		status, _, payload, raw := caller.call(http.MethodPost, path, false, map[string]any{})
		if status != http.StatusForbidden {
			t.Errorf("%s 缺少 CSRF 令牌时状态应为 403，实际 %d：%s", path, status, raw)
			continue
		}
		if code := integrationErrorCode(payload); code != "csrf_failed" {
			t.Errorf("%s 缺少 CSRF 令牌时错误码应为 csrf_failed，实际 %q：%s", path, code, raw)
			continue
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("没有检查到任何 POST 路由，路由表可能未注册")
	}
	t.Logf("已验证 %d 条写接口在缺少 CSRF 时返回 403 csrf_failed", checked)
}

// substituteRouteParams 把 Gin 路径参数替换为占位值，便于直接发起请求。
func substituteRouteParams(path string) string {
	segments := strings.Split(path, "/")
	for index, segment := range segments {
		if strings.HasPrefix(segment, ":") || strings.HasPrefix(segment, "*") {
			segments[index] = "1"
		}
	}
	return strings.Join(segments, "/")
}
