package auth

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name     string
		password string
		valid    bool
	}{
		{"过短", "short", false},
		{"正常", "correct horse battery staple", true},
		{"超过字符数", strings.Repeat("a", 65), false},
		{"超过 bcrypt 字节上限", strings.Repeat("密", 25), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePassword(tt.password)
			if (err == nil) != tt.valid {
				t.Fatalf("ValidatePassword() err=%v valid=%v", err, tt.valid)
			}
		})
	}
}

func TestHashAndVerifyPassword(t *testing.T) {
	const password = "测试-password-123"
	if utf8.RuneCountInString(password) < 8 {
		t.Fatal("测试密码长度不足")
	}
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(hash, password) {
		t.Fatal("哈希不得包含明文密码")
	}
	if !passwordMatches(hash, password) {
		t.Fatal("正确密码应通过校验")
	}
	if passwordMatches(hash, password+"x") {
		t.Fatal("错误密码不应通过校验")
	}
}

func TestRandomTokensAreDistinctAndHashed(t *testing.T) {
	a, err := randomToken()
	if err != nil {
		t.Fatal(err)
	}
	b, err := randomToken()
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("随机会话令牌不应重复")
	}
	if len(hashSessionToken(a)) != 64 || hashSessionToken(a) == a {
		t.Fatal("会话令牌必须保存为 SHA-256 哈希")
	}
	if !tokensEqual(a, a) || tokensEqual(a, b) {
		t.Fatal("CSRF 常量时间比较结果错误")
	}
}
