package app

import "github.com/great-magician-01/timeline/internal/modules/auth/domain"

// 测试辅助：直接复用 domain 的密码/令牌函数，避免测试里出现第二套实现。
func hashPasswordForTest(password string) (string, error) { return domain.HashPassword(password) }

func passwordMatchesForTest(hash, password string) bool {
	return domain.PasswordMatches(hash, password)
}

func hashSessionTokenForTest(token string) string { return domain.HashSessionToken(token) }
