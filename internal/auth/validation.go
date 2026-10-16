package auth

import (
	"net/mail"
	"regexp"
	"strings"
	"unicode/utf8"
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{2,31}$`)

type RegisterInput struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

func normalizeRegistration(in RegisterInput) (RegisterInput, map[string]string) {
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

func validEmail(email string) bool {
	if email == "" || strings.Count(email, "@") != 1 || strings.ContainsAny(email, "\r\n") {
		return false
	}
	addr, err := mail.ParseAddress(email)
	return err == nil && addr.Name == "" && strings.EqualFold(addr.Address, email)
}

func validateProfile(nickname, bio *string) map[string]string {
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

func validationMessage(details map[string]string) string {
	for _, message := range details {
		return message
	}
	return "请求参数不符合要求"
}

func invalidInputError(_ error) string {
	return "请求体必须是有效的 JSON 对象，且不能包含未知字段"
}
