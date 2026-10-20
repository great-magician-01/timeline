package router

import (
	"net/http"
	"strings"
	"testing"
)

// 基线：super-admin 合并后的 b6fed97 共 110 条 /api/v1 路由（GET 45 / POST 65），
// 私信新增 11 条后为 121 条（GET 50 / POST 71）；置顶新增 3 条后为 124 条（GET 50 / POST 74）。
const baselineRouteCount = 124

func TestRouteTableBaselineAndPolicies(t *testing.T) {
	routes := Routes()
	if len(routes) != baselineRouteCount {
		t.Fatalf("路由数量 = %d，期望基线 %d；新增/删除路由必须同步更新本测试", len(routes), baselineRouteCount)
	}

	seen := make(map[string]bool, len(routes))
	methods := map[string]int{}
	policyCount := map[Policy]int{}
	for _, route := range routes {
		key := route.Method + " " + route.Path
		if seen[key] {
			t.Errorf("重复路由: %s", key)
		}
		seen[key] = true
		methods[route.Method]++
		policyCount[route.Policy]++

		if route.Handler == nil {
			t.Errorf("路由 %s 缺少处理器", key)
			continue
		}
		if !strings.HasPrefix(route.Path, "/") {
			t.Errorf("路由 %s 路径必须以 / 开头", key)
		}
		switch route.Method {
		case http.MethodGet:
			if isWritePolicy(route.Policy) {
				t.Errorf("GET 路由 %s 不应使用写策略 %v", key, route.Policy)
			}
		case http.MethodPost:
			if !isWritePolicy(route.Policy) {
				t.Errorf("POST 路由 %s 必须使用写策略，实际 %v", key, route.Policy)
			}
		default:
			t.Errorf("业务接口只允许 GET/POST，发现 %s %s", route.Method, route.Path)
		}
	}

	if methods[http.MethodGet] != 50 || methods[http.MethodPost] != 74 {
		t.Fatalf("方法分布 = GET %d / POST %d，期望 50 / 74",
			methods[http.MethodGet], methods[http.MethodPost])
	}

	wantPolicy := map[Policy]int{
		Public:          1,
		Optional:        17,
		GuestWrite:      2,
		Login:           13,
		HumanWrite:      38,
		AdminRead:       19,
		AdminWrite:      32,
		SuperAdminWrite: 2,
	}
	for policy, want := range wantPolicy {
		if got := policyCount[policy]; got != want {
			t.Errorf("策略 %v 路由数 = %d，期望 %d", policy, got, want)
		}
	}
}

func TestAdminRoutesUseAdminPolicies(t *testing.T) {
	for _, route := range Routes() {
		if !strings.HasPrefix(route.Path, "/admin/") {
			continue
		}
		switch route.Policy {
		case AdminRead, AdminWrite, SuperAdminWrite:
		default:
			t.Errorf("管理路由 %s %s 必须使用 Admin/SuperAdmin 策略，实际 %v",
				route.Method, route.Path, route.Policy)
		}
	}
}

func TestSuperAdminWriteOnlyGrantRevoke(t *testing.T) {
	want := map[string]bool{
		"POST /admin/users/:id/grant-admin":  true,
		"POST /admin/users/:id/revoke-admin": true,
	}
	got := map[string]bool{}
	for _, route := range Routes() {
		if route.Policy == SuperAdminWrite {
			got[route.Method+" "+route.Path] = true
		}
	}
	if len(got) != len(want) {
		t.Fatalf("SuperAdminWrite 路由 = %v，期望 %v", got, want)
	}
	for key := range want {
		if !got[key] {
			t.Errorf("缺少 SuperAdminWrite 路由 %s", key)
		}
	}
}

func isWritePolicy(p Policy) bool {
	switch p {
	case GuestWrite, HumanWrite, AdminWrite, SuperAdminWrite:
		return true
	default:
		return false
	}
}
