package auth

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

const passwordBcryptCost = 12

// ValidatePassword 同时限制字符数和字节数，避免 bcrypt 静默截断超长密码。
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

func HashPassword(password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), passwordBcryptCost)
	if err != nil {
		return "", fmt.Errorf("生成密码哈希失败: %w", err)
	}
	return string(hash), nil
}

func passwordMatches(hash, password string) bool {
	if hash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成安全随机数失败: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func hashSessionToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func tokensEqual(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	aHash := sha256.Sum256([]byte(a))
	bHash := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(aHash[:], bHash[:]) == 1
}
