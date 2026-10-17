package server_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/great-magician-01/timeline/internal/testkit"
)

// TestAllWriteRoutesRequireCSRF 遍历路由表中所有 /api/v1 的 POST 接口，
// 断言它们在缺少 CSRF 令牌时一律返回 403 csrf_failed。
// CSRF 中间件在业务 Handler 之前执行，因此路径参数可以用占位值。
// 这条用例覆盖“新增写接口忘记挂 CSRF”这类回归。
func TestAllWriteRoutesRequireCSRF(t *testing.T) {
	srv := testkit.NewIntegrationServer(t)

	suffix := testkit.IntegrationSuffix()
	user := srv.Client(t)
	user.RegisterAndLogin("csrf_"+suffix, "csrf-"+suffix+"@example.com")
	// 管理接口在分组中间件里先做角色校验，因此必须用管理员会话才能走到 CSRF 校验。
	admin := srv.Client(t)
	admin.LoginAndSetCSRF(testkit.IntegrationAdminUsername, testkit.IntegrationAdminPassword)

	checked := 0
	for _, route := range srv.Routes() {
		if route.Method != http.MethodPost || !strings.HasPrefix(route.Path, "/api/v1") {
			continue
		}
		// 登录/注册使用游客双提交 Cookie，同样必须拒绝缺少令牌的请求；
		// 这里统一验证总有一个 CSRF 校验拦住它们。
		path := testkit.SubstituteRouteParams(route.Path)
		caller := user
		if strings.HasPrefix(path, "/api/v1/admin/") {
			caller = admin
		}
		status, _, payload, raw := caller.Call(http.MethodPost, path, false, map[string]any{})
		if status != http.StatusForbidden {
			t.Errorf("%s 缺少 CSRF 令牌时状态应为 403，实际 %d：%s", path, status, raw)
			continue
		}
		if code := testkit.ErrorCode(payload); code != "csrf_failed" {
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
