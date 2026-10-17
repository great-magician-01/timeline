package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

// PasswordBcryptCost 是密码哈希成本；与旧实现一致（12）。
const PasswordBcryptCost = 12

// ValidatePassword 同时限制字符数与 UTF-8 字节数，避免 bcrypt 静默截断超长密码。
func ValidatePassword(password string) error {
	length := utf8.RuneCountInString(password)
	if length < 8 || length > 64 {
		return fmt.Errorf("密码需为 8-64 个字符")
	}
	if len(password) > 72 {
		return fmt.Errorf("密码的 UTF-8 编码不能超过 72 字节")
	}
	return nil
}

// HashPassword 校验并生成 bcrypt 哈希。bcrypt 成本较高，调用方应在事务外执行。
func HashPassword(password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), PasswordBcryptCost)
	if err != nil {
		return "", fmt.Errorf("生成密码哈希失败: %w", err)
	}
	return string(hash), nil
}

// PasswordMatches 比较明文与 bcrypt 哈希；空哈希恒为不匹配。
func PasswordMatches(hash, password string) bool {
	if hash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// RandomToken 生成 32 字节安全随机令牌（base64url 无填充），用于会话与 CSRF。
func RandomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成安全随机数失败: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// HashSessionToken 计算会话令牌的 SHA-256 十六进制摘要；数据库只保存摘要。
func HashSessionToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// TokensEqual 以常量时间比较两个非空令牌；任一为空返回 false。
func TokensEqual(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	aHash := sha256.Sum256([]byte(a))
	bHash := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(aHash[:], bHash[:]) == 1
}
