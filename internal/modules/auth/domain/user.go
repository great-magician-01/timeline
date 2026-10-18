// Package domain 定义 auth 模块的纯规则：账号角色与状态、注册/资料校验、
// 首注册超管不变式与会话判定。本包不访问数据库、不依赖 HTTP。
package domain

import (
	"net/mail"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/access"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// 角色与状态常量直接复用 shared/model，保证与数据库 CHECK 约束一致。
const (
	RoleUser  = model.RoleUser
	RoleAdmin = model.RoleAdmin

	StatusActive    = model.StatusActive
	StatusSuspended = model.StatusSuspended
)

// 注册用户名为 3-32 位、字母开头、仅字母数字下划线。
var usernamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{2,31}$`)

// RegisterInput 是注册用例输入（HTTP 层负责 JSON 解码后转换）。
type RegisterInput struct {
	Username string
	Email    string
	Password string
}

// NormalizeRegistration 规范化注册输入并返回字段级错误；返回 nil 表示通过校验。
func NormalizeRegistration(in RegisterInput) (RegisterInput, map[string]string) {
	in.Username = strings.TrimSpace(in.Username)
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	details := make(map[string]string)
	if !usernamePattern.MatchString(in.Username) {
		details["username"] = "用户名需为 3-32 位，以字母开头，只能包含字母、数字和下划线"
	}
	if len(in.Email) > 254 || !validEmail(in.Email) {
		details["email"] = "请输入有效邮箱地址"
	}
	if err := ValidatePassword(in.Password); err != nil {
		details["password"] = err.Error()
	}
	if len(details) > 0 {
		return in, details
	}
	return in, nil
}

// validEmail 校验邮箱：单 @、无换行、mail.ParseAddress 解析且不含显示名。
func validEmail(email string) bool {
	if email == "" || strings.Count(email, "@") != 1 || strings.ContainsAny(email, "\r\n") {
		return false
	}
	addr, err := mail.ParseAddress(email)
	return err == nil && addr.Name == "" && strings.EqualFold(addr.Address, email)
}

// ValidateUsername 校验用户名格式（AI 账号创建等只校验用户名时复用）。
func ValidateUsername(username string) error {
	if !usernamePattern.MatchString(username) {
		return apperr.Validation("用户名需为 3-32 位，以字母开头，只能包含字母、数字和下划线")
	}
	return nil
}

// ValidateProfile 校验昵称与简介；传入的指针会被就地 trim（与原实现一致）。
func ValidateProfile(nickname, bio *string) map[string]string {
	details := make(map[string]string)
	if nickname != nil {
		trimmed := strings.TrimSpace(*nickname)
		*nickname = trimmed
		if length := utf8.RuneCountInString(trimmed); length < 1 || length > 50 {
			details["nickname"] = "昵称需为 1-50 个字符"
		}
	}
	if bio != nil {
		trimmed := strings.TrimSpace(*bio)
		*bio = trimmed
		if utf8.RuneCountInString(trimmed) > 500 {
			details["bio"] = "简介不能超过 500 个字符"
		}
	}
	if len(details) > 0 {
		return details
	}
	return nil
}

// ValidationMessage 返回字段错误中的第一条文案（与旧实现一致）。
func ValidationMessage(details map[string]string) string {
	for _, message := range details {
		return message
	}
	return "请求参数不符合要求"
}

// BootstrapRole 返回空库首个注册用户应写入的角色与超管标记：
// role=admin 且 is_super_admin=true（users_super_admin_check 要求超管必须是 admin）。
func BootstrapRole() (role string, isSuperAdmin bool) {
	return RoleAdmin, true
}

// ValidateSuperAdminInvariant 校验超管不变式：超管必须是 admin 且不能是 AI。
// 数据库侧由 users_super_admin_check 兜底；应用层在写角色/创建账号前先校验。
func ValidateSuperAdminInvariant(role string, isSuperAdmin, isAI bool) error {
	if isSuperAdmin && role != RoleAdmin {
		return apperr.Validation("超级管理员必须是管理员角色")
	}
	if isSuperAdmin && isAI {
		return apperr.Validation("AI 账号不能成为超级管理员")
	}
	return nil
}

// CanChangeAdminRole 判断操作者能否修改目标账号角色（复用 shared/access 纯策略）。
func CanChangeAdminRole(actor, target model.User) error {
	return access.CanChangeAdminRole(actor, target)
}

// CanGrantAdminRole 在 CanChangeAdminRole 之上补充授权方向校验。
func CanGrantAdminRole(actor, target model.User) error {
	return access.CanGrantAdminRole(actor, target)
}

// CanModerateUser 判断管理员能否处置目标账号（超管保护）。
func CanModerateUser(actor model.Viewer, target model.User) error {
	return access.CanModerateUser(actor, target)
}
