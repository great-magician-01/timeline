package domain

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// TestRandomTokenFormatAndUniqueness 校验安全随机令牌：32 字节随机数、base64url 无填充、
// 每次生成互不相同（会话/CSRF 令牌不能可预测）。
func TestRandomTokenFormatAndUniqueness(t *testing.T) {
	first, err := RandomToken()
	if err != nil {
		t.Fatalf("RandomToken: %v", err)
	}
	second, err := RandomToken()
	if err != nil {
		t.Fatalf("RandomToken: %v", err)
	}
	if first == second {
		t.Fatal("两次生成的令牌不应相同")
	}
	if strings.ContainsAny(first, "+/=") {
		t.Fatalf("令牌必须是 base64url 无填充格式: %q", first)
	}
	raw, err := base64.RawURLEncoding.DecodeString(first)
	if err != nil {
		t.Fatalf("令牌不是合法 base64url: %v", err)
	}
	if len(raw) != 32 {
		t.Fatalf("令牌随机字节数 = %d，期望 32", len(raw))
	}
}

// TestHashSessionToken 校验会话令牌摘要：SHA-256 十六进制（64 字符）、确定性、
// 不同令牌摘要不同（数据库只保存摘要，不保存明文）。
func TestHashSessionToken(t *testing.T) {
	token := "session-token-value"
	digest := HashSessionToken(token)
	if len(digest) != 64 {
		t.Fatalf("摘要长度 = %d，期望 64（SHA-256 hex）", len(digest))
	}
	if strings.ToLower(digest) != digest {
		t.Fatalf("摘要应为小写十六进制: %q", digest)
	}
	if digest != HashSessionToken(token) {
		t.Fatal("同一令牌的摘要必须确定")
	}
	if digest == HashSessionToken(token+"x") {
		t.Fatal("不同令牌的摘要不应相同")
	}
	if digest == token {
		t.Fatal("摘要不能等于明文令牌")
	}
}

// TestSessionStateIsAuthenticated 校验只有 SessionValid 代表已登录；
// 其余状态（缺失/吊销/过期/停用/AI/内部错误）都不能当作登录身份。
func TestSessionStateIsAuthenticated(t *testing.T) {
	valid := []SessionState{SessionValid}
	invalid := []SessionState{
		SessionMissing, SessionNotFound, SessionRevoked, SessionExpired,
		SessionVersionMismatch, SessionSuspended, SessionAIAccount, SessionResolveError,
	}
	for _, state := range valid {
		if !state.IsAuthenticated() {
			t.Errorf("状态 %q 应视为已登录", state)
		}
	}
	for _, state := range invalid {
		if state.IsAuthenticated() {
			t.Errorf("状态 %q 不应视为已登录", state)
		}
	}
}

// TestValidateUsernameBranches 校验用户名格式：3-32 位、字母开头、仅字母数字下划线，
// 错误必须是 validation_failed（供 HTTP 层映射 400）。
func TestValidateUsernameBranches(t *testing.T) {
	valid := []string{"abc", "Alice_01", "a1234567890123456789012345678901"}
	for _, username := range valid {
		if err := ValidateUsername(username); err != nil {
			t.Errorf("用户名 %q 应合法: %v", username, err)
		}
	}
	invalid := []string{
		"", "ab", "1abc", "_abc", "a b", "a-b", "用户名abc",
		strings.Repeat("a", 33),
	}
	for _, username := range invalid {
		err := ValidateUsername(username)
		if err == nil {
			t.Errorf("用户名 %q 应非法", username)
			continue
		}
		if !apperr.IsKind(err, apperr.KindValidation) {
			t.Errorf("用户名 %q 的错误 Kind 应为 validation_failed: %v", username, err)
		}
	}
}

// TestValidationMessage 校验字段错误取第一条文案；空 map 回退通用提示。
func TestValidationMessage(t *testing.T) {
	if got := ValidationMessage(nil); got != "请求参数不符合要求" {
		t.Fatalf("空错误详情文案 = %q", got)
	}
	details := map[string]string{"username": "用户名不合法"}
	if got := ValidationMessage(details); got != "用户名不合法" {
		t.Fatalf("字段错误文案 = %q，期望第一条", got)
	}
}

// superAdminUser、adminUser、normalUser、aiUser 构造角色矩阵夹具。
func superAdminUser(id int64) model.User {
	return model.User{ID: id, Role: RoleAdmin, IsSuperAdmin: true, Status: StatusActive}
}

func adminUser(id int64) model.User {
	return model.User{ID: id, Role: RoleAdmin, Status: StatusActive}
}

func normalUser(id int64) model.User {
	return model.User{ID: id, Role: RoleUser, Status: StatusActive}
}

func aiUser(id int64) model.User {
	return model.User{ID: id, Role: RoleUser, IsAI: true, Status: StatusActive}
}

// TestCanChangeAdminRoleGuards 校验授权/撤销管理员的操作者与目标限制：
// 仅超管操作、不能操作自己/其他超管/AI 账号。
func TestCanChangeAdminRoleGuards(t *testing.T) {
	actor := superAdminUser(1)
	cases := []struct {
		name     string
		actor    model.User
		target   model.User
		wantKind apperr.Kind
	}{
		{"超管操作普通用户", actor, normalUser(2), ""},
		{"超管操作普通管理员", actor, adminUser(2), ""},
		{"普通管理员越权", adminUser(2), normalUser(3), apperr.KindForbidden},
		{"普通用户越权", normalUser(2), normalUser(3), apperr.KindForbidden},
		{"AI 账号不能操作", aiUser(2), normalUser(3), apperr.KindForbidden},
		{"不能操作自己", actor, superAdminUser(1), apperr.KindValidation},
		{"不能操作其他超管", actor, superAdminUser(9), apperr.KindValidation},
		{"AI 目标不能成为管理员", actor, aiUser(3), apperr.KindValidation},
	}
	for _, tc := range cases {
		err := CanChangeAdminRole(tc.actor, tc.target)
		if tc.wantKind == "" {
			if err != nil {
				t.Errorf("%s: 不应报错，实际 %v", tc.name, err)
			}
			continue
		}
		if !apperr.IsKind(err, tc.wantKind) {
			t.Errorf("%s: Kind 应为 %v，实际 %v", tc.name, tc.wantKind, err)
		}
	}
}

// TestCanGrantAdminRoleGuards 校验授权方向额外限制：已停用账号不能授权为管理员，
// 撤销方向（CanChangeAdminRole）不受停用状态影响。
func TestCanGrantAdminRoleGuards(t *testing.T) {
	actor := superAdminUser(1)
	suspended := model.User{ID: 2, Role: RoleUser, Status: StatusSuspended}
	if err := CanChangeAdminRole(actor, suspended); err != nil {
		t.Fatalf("撤销方向不要求账号启用: %v", err)
	}
	if err := CanGrantAdminRole(actor, suspended); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("停用账号授权应为 validation_failed，实际 %v", err)
	}
	if err := CanGrantAdminRole(actor, normalUser(3)); err != nil {
		t.Fatalf("启用账号授权应允许: %v", err)
	}
}

// TestCanModerateUserGuards 校验管理员处置目标账号：普通管理员不能操作超管账号，
// 超管可处置普通管理员，非管理员/游客不能处置任何人。
func TestCanModerateUserGuards(t *testing.T) {
	superAdmin := superAdminUser(1)
	admin := adminUser(2)
	targetSuper := superAdminUser(9)
	targetAdmin := adminUser(8)

	cases := []struct {
		name     string
		actor    model.Viewer
		target   model.User
		wantKind apperr.Kind
	}{
		{"超管处置普通管理员", model.ViewerFrom(superAdmin), targetAdmin, ""},
		{"超管处置超管账号", model.ViewerFrom(superAdmin), targetSuper, ""},
		{"普通管理员处置超管被拒", model.ViewerFrom(admin), targetSuper, apperr.KindForbidden},
		{"普通管理员处置普通用户", model.ViewerFrom(admin), normalUser(3), ""},
		{"普通用户无权处置", model.ViewerFrom(normalUser(3)), targetAdmin, apperr.KindForbidden},
		{"游客无权处置", model.GuestViewer(), targetAdmin, apperr.KindForbidden},
		{"AI 账号无权处置", model.ViewerFrom(aiUser(3)), targetAdmin, apperr.KindForbidden},
	}
	for _, tc := range cases {
		err := CanModerateUser(tc.actor, tc.target)
		if tc.wantKind == "" {
			if err != nil {
				t.Errorf("%s: 不应报错，实际 %v", tc.name, err)
			}
			continue
		}
		if !apperr.IsKind(err, tc.wantKind) {
			t.Errorf("%s: Kind 应为 %v，实际 %v", tc.name, tc.wantKind, err)
		}
	}
}
