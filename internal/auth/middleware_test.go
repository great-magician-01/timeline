package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestRequireSuperAdminRoleMatrix 覆盖 RequireSuperAdmin 的角色矩阵：
// 未登录 401、普通用户/普通管理员/AI 伪超管 403、超级管理员放行。
// 该中间件位于 RequireAdmin 之后，这里直接注入当前用户模拟前置中间件的结果。
func TestRequireSuperAdminRoleMatrix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &Service{}

	cases := []struct {
		name     string
		user     *User
		want     int
		wantCode string
	}{
		{name: "未登录", user: nil, want: http.StatusUnauthorized, wantCode: "unauthenticated"},
		{name: "普通用户", user: &User{Role: RoleUser, Status: StatusActive}, want: http.StatusForbidden, wantCode: "forbidden"},
		{name: "普通管理员", user: &User{Role: RoleAdmin, Status: StatusActive}, want: http.StatusForbidden, wantCode: "forbidden"},
		{name: "超级管理员", user: &User{Role: RoleAdmin, IsSuperAdmin: true, Status: StatusActive}, want: http.StatusNoContent},
		{name: "AI 伪超管", user: &User{Role: RoleAdmin, IsSuperAdmin: true, IsAI: true, Status: StatusActive}, want: http.StatusForbidden, wantCode: "forbidden"},
		{name: "超管标记但非管理员", user: &User{Role: RoleUser, IsSuperAdmin: true, Status: StatusActive}, want: http.StatusForbidden, wantCode: "forbidden"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			user := tc.user
			engine := gin.New()
			engine.GET("/protected", func(c *gin.Context) {
				if user != nil {
					c.Set(userContextKey, *user)
				}
				service.RequireSuperAdmin(c)
			}, func(c *gin.Context) {
				// 通过 RequireSuperAdmin 后由该处理函数返回 204，便于断言放行。
				c.Status(http.StatusNoContent)
			})

			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/protected", nil)
			engine.ServeHTTP(recorder, req)

			if recorder.Code != tc.want {
				t.Fatalf("状态码 = %d，期望 %d，响应体: %s", recorder.Code, tc.want, recorder.Body.String())
			}
			if tc.wantCode == "" {
				return
			}
			var payload struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
				t.Fatalf("解析错误响应失败: %v，原文: %s", err, recorder.Body.String())
			}
			if payload.Error.Code != tc.wantCode {
				t.Fatalf("错误码 = %q，期望 %q，响应体: %s", payload.Error.Code, tc.wantCode, recorder.Body.String())
			}
		})
	}
}
