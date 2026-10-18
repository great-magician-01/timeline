package http

// 本文补充 auth/http 超管授权/撤销接口用例：完整策略链（登录→管理员→CSRF→超管）、
// 权限与参数错误映射、成功响应 DTO 与审计、非法 ID 与目标不存在等分支。
// 已有 auth_handler_test.go 覆盖非法 ID 的零装配服务分支，此处验证带真实 app 服务的链路。

import (
	"net/http"
	"testing"

	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

func TestAdminRoleHandlers(t *testing.T) {
	h := newHarness(t, true)
	engine := h.engine()
	super := h.seedUser(t, model.User{ID: 1, Username: "root", Email: "root@example.com", Role: model.RoleAdmin, IsSuperAdmin: true})
	mod := h.seedUser(t, model.User{ID: 2, Username: "mod", Role: model.RoleAdmin})
	target := h.seedUser(t, model.User{ID: 3, Username: "bob"})
	h.seedUser(t, model.User{ID: 4, Username: "ai_bot", IsAI: true})
	h.seedSession(super, "super-token", "super-csrf")
	h.seedSession(mod, "mod-token", "mod-csrf")
	h.seedSession(target, "target-token", "target-csrf")

	t.Run("未登录返回 401", func(t *testing.T) {
		recorder := h.perform(t, engine, http.MethodPost, "/api/v1/admin/users/3/grant-admin", "", withHeader(CSRFHeader, "csrf"))
		if recorder.Code != http.StatusUnauthorized || errorCode(t, recorder) != "unauthenticated" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("普通管理员越权返回 403 forbidden", func(t *testing.T) {
		recorder := h.performAs(t, engine, http.MethodPost, "/api/v1/admin/users/3/grant-admin", "", "mod-token", "mod-csrf")
		if recorder.Code != http.StatusForbidden || errorCode(t, recorder) != "forbidden" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("普通用户越权返回 403 forbidden", func(t *testing.T) {
		recorder := h.performAs(t, engine, http.MethodPost, "/api/v1/admin/users/3/grant-admin", "", "target-token", "target-csrf")
		if recorder.Code != http.StatusForbidden || errorCode(t, recorder) != "forbidden" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("缺少 CSRF 返回 403 csrf_failed（超管校验之前）", func(t *testing.T) {
		recorder := h.performAs(t, engine, http.MethodPost, "/api/v1/admin/users/3/grant-admin", "", "super-token", "")
		if recorder.Code != http.StatusForbidden || errorCode(t, recorder) != "csrf_failed" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("非法 ID 返回 400", func(t *testing.T) {
		recorder := h.performAs(t, engine, http.MethodPost, "/api/v1/admin/users/abc/grant-admin", "", "super-token", "super-csrf")
		if recorder.Code != http.StatusBadRequest || errorCode(t, recorder) != "invalid_request" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("目标不存在返回 404", func(t *testing.T) {
		recorder := h.performAs(t, engine, http.MethodPost, "/api/v1/admin/users/999/grant-admin", "", "super-token", "super-csrf")
		if recorder.Code != http.StatusNotFound || errorCode(t, recorder) != "not_found" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("不能授权 AI 账号返回 400", func(t *testing.T) {
		recorder := h.performAs(t, engine, http.MethodPost, "/api/v1/admin/users/4/grant-admin", "", "super-token", "super-csrf")
		if recorder.Code != http.StatusBadRequest || errorCode(t, recorder) != "validation_failed" {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("撤销普通用户的管理员角色幂等成功且不写审计", func(t *testing.T) {
		before := len(h.audit.entries)
		recorder := h.performAs(t, engine, http.MethodPost, "/api/v1/admin/users/3/revoke-admin", "", "super-token", "super-csrf")
		if recorder.Code != http.StatusOK {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
		responseUser, _ := bodyMap(t, recorder)["user"].(map[string]any)
		if responseUser["role"] != model.RoleUser {
			t.Fatalf("撤销后 role = %v", responseUser["role"])
		}
		if len(h.audit.entries) != before {
			t.Fatal("幂等撤销不应写审计")
		}
	})

	t.Run("授权成功返回私有 DTO 并写审计", func(t *testing.T) {
		before := len(h.audit.entries)
		recorder := h.performAs(t, engine, http.MethodPost, "/api/v1/admin/users/3/grant-admin", "", "super-token", "super-csrf")
		if recorder.Code != http.StatusOK {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
		responseUser, _ := bodyMap(t, recorder)["user"].(map[string]any)
		if responseUser["role"] != model.RoleAdmin {
			t.Fatalf("授权后 role = %v", responseUser["role"])
		}
		if responseUser["status"] != model.StatusActive {
			t.Fatalf("私有 DTO 应包含 status: %v", responseUser)
		}
		if id, _ := responseUser["id"].(string); id == "" {
			t.Fatalf("user.id 必须为字符串: %v", responseUser["id"])
		}
		if len(h.audit.entries) != before+1 {
			t.Fatalf("授权应写一条审计: %d -> %d", before, len(h.audit.entries))
		}
		last := h.audit.entries[len(h.audit.entries)-1]
		if last.Action != contracts.AuditActionUserGrantAdmin || last.TargetID == nil || *last.TargetID != target.ID {
			t.Fatalf("授权审计不符: %+v", last)
		}
	})

	t.Run("撤销成功返回普通用户并写审计", func(t *testing.T) {
		before := len(h.audit.entries)
		recorder := h.performAs(t, engine, http.MethodPost, "/api/v1/admin/users/3/revoke-admin", "", "super-token", "super-csrf")
		if recorder.Code != http.StatusOK {
			t.Fatalf("状态码 = %d，body=%s", recorder.Code, recorder.Body.String())
		}
		responseUser, _ := bodyMap(t, recorder)["user"].(map[string]any)
		if responseUser["role"] != model.RoleUser {
			t.Fatalf("撤销后 role = %v", responseUser["role"])
		}
		if len(h.audit.entries) != before+1 || h.audit.entries[len(h.audit.entries)-1].Action != contracts.AuditActionUserRevokeAdmin {
			t.Fatalf("撤销审计不符: %+v", h.audit.entries)
		}
	})

	t.Run("缺少 Viewer 的直接调用返回 401", func(t *testing.T) {
		c, recorder := newTestContext(http.MethodPost, "/api/v1/admin/users/3/grant-admin")
		h.handler.GrantAdmin(c)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("GrantAdmin 缺少 Viewer 状态码 = %d", recorder.Code)
		}
		c, recorder = newTestContext(http.MethodPost, "/api/v1/admin/users/3/revoke-admin")
		h.handler.RevokeAdmin(c)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("RevokeAdmin 缺少 Viewer 状态码 = %d", recorder.Code)
		}
	})
}
