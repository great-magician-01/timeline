package app

import (
	"encoding/base64"
	"strings"
	"testing"
)

// 说明：secret.go 是 AES-256-GCM 加解密的纯函数层，全部用例只使用 crypto 标准库，
// 不依赖数据库/网络；断言具体错误文案与往返结果，防止密钥格式或掩码策略漂移。

// TestNewGCMRejectsInvalidKeyLength 校验只接受 32 字节密钥。
func TestNewGCMRejectsInvalidKeyLength(t *testing.T) {
	for _, size := range []int{0, 1, 16, 24, 31, 33, 64} {
		gcm, err := newGCM(make([]byte, size))
		if err == nil || gcm != nil {
			t.Errorf("长度 %d 的密钥应被拒绝，实际 err=%v gcm=%v", size, err, gcm)
			continue
		}
		if !strings.Contains(err.Error(), "32 字节") {
			t.Errorf("错误文案应提示 32 字节，实际: %v", err)
		}
	}
	gcm, err := newGCM(testSecretKey)
	if err != nil {
		t.Fatalf("32 字节密钥应可用: %v", err)
	}
	if gcm.NonceSize() != 12 {
		t.Fatalf("AES-GCM 的 nonce 长度应为 12，实际 %d", gcm.NonceSize())
	}
}

// TestEncryptSecretEmptyPlain 校验空明文原样返回空串（表示未设置密钥），且不校验密钥长度。
func TestEncryptSecretEmptyPlain(t *testing.T) {
	got, err := encryptSecret([]byte("short"), "")
	if err != nil || got != "" {
		t.Fatalf("空明文应返回空串且不报错，实际 got=%q err=%v", got, err)
	}
	got, err = encryptSecret(testSecretKey, "")
	if err != nil || got != "" {
		t.Fatalf("空明文应返回空串且不报错，实际 got=%q err=%v", got, err)
	}
}

// TestEncryptSecretRejectsInvalidKey 校验非空明文遇到错误密钥长度时返回错误。
func TestEncryptSecretRejectsInvalidKey(t *testing.T) {
	if _, err := encryptSecret([]byte("short"), "sk-demo"); err == nil {
		t.Fatal("16 字节密钥应导致加密失败")
	}
}

// TestEncryptDecryptSecretRoundTrip 校验加密/解密往返与密文格式。
func TestEncryptDecryptSecretRoundTrip(t *testing.T) {
	cases := map[string]string{
		"ASCII":   "sk-1234567890abcdef",
		"中文多字节":   "密钥-中文-测试值",
		"带空格与引号":  `  "sk with space"  `,
		"类 JSON":  `{"api_key":"sk-x"}`,
		"emoji":   "sk-🔑-secret",
		"超长值":     strings.Repeat("k", 1000),
		"仅一个字符":   "x",
		"含换行与制表符": "line1\nline2\ttab",
	}
	for name, plain := range cases {
		t.Run(name, func(t *testing.T) {
			cipher, err := encryptSecret(testSecretKey, plain)
			if err != nil {
				t.Fatalf("加密失败: %v", err)
			}
			if !strings.HasPrefix(cipher, secretPrefix) {
				t.Fatalf("密文必须以 %q 开头，实际 %q", secretPrefix, cipher)
			}
			if len([]rune(plain)) >= 4 && strings.Contains(cipher, plain) {
				t.Fatalf("密文不得包含明文")
			}
			got, err := decryptSecret(testSecretKey, cipher)
			if err != nil {
				t.Fatalf("解密失败: %v", err)
			}
			if got != plain {
				t.Fatalf("往返结果不一致: got=%q want=%q", got, plain)
			}
			// 每次加密使用随机 nonce，同一明文应产生不同密文。
			again, err := encryptSecret(testSecretKey, plain)
			if err != nil {
				t.Fatalf("第二次加密失败: %v", err)
			}
			if again == cipher {
				t.Fatal("同一明文的两次加密应因随机 nonce 而不同")
			}
		})
	}
}

// TestDecryptSecretErrors 覆盖前缀、base64、长度、篡改与密钥不匹配等失败分支。
func TestDecryptSecretErrors(t *testing.T) {
	validCipher, err := encryptSecret(testSecretKey, "sk-needle-value")
	if err != nil {
		t.Fatalf("构造测试密文失败: %v", err)
	}

	tampered := tamperCipher(t, validCipher)
	shortPayload := secretPrefix + base64.RawURLEncoding.EncodeToString([]byte("12345"))
	otherKey := []byte("fedcba9876543210fedcba9876543210")

	cases := []struct {
		name    string
		key     []byte
		encoded string
		wantMsg string
	}{
		{name: "空密文", key: testSecretKey, encoded: "", wantMsg: ""},
		{name: "缺少前缀", key: testSecretKey, encoded: "abc", wantMsg: "缺少 v1 前缀"},
		{name: "版本不符", key: testSecretKey, encoded: "v2:abc", wantMsg: "缺少 v1 前缀"},
		{name: "base64 损坏", key: testSecretKey, encoded: "v1:!!!!", wantMsg: "base64 解码失败"},
		{name: "数据长度不足", key: testSecretKey, encoded: shortPayload, wantMsg: "数据长度不足"},
		{name: "密文被篡改", key: testSecretKey, encoded: tampered, wantMsg: "解密失败"},
		{name: "密钥不匹配", key: otherKey, encoded: validCipher, wantMsg: "解密失败"},
		{name: "密钥长度错误", key: []byte("short"), encoded: validCipher, wantMsg: "32 字节"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decryptSecret(tc.key, tc.encoded)
			if tc.wantMsg == "" {
				if err != nil || got != "" {
					t.Fatalf("空密文应返回空串且不报错，实际 got=%q err=%v", got, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("应返回解密错误，实际得到 %q", got)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("错误文案应包含 %q，实际: %v", tc.wantMsg, err)
			}
			// 错误信息不得泄露明文或密文内容。
			if strings.Contains(err.Error(), "sk-needle-value") || strings.Contains(err.Error(), validCipher) {
				t.Fatalf("错误信息泄露了敏感内容: %v", err)
			}
		})
	}
}

// tamperCipher 篡改密文最后一个字节后重新编码，用于认证失败用例。
func tamperCipher(t *testing.T, encoded string) string {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(encoded, secretPrefix))
	if err != nil {
		t.Fatalf("解码测试密文失败: %v", err)
	}
	raw[len(raw)-1] ^= 0xff
	return secretPrefix + base64.RawURLEncoding.EncodeToString(raw)
}

// TestMaskSecret 校验掩码保留前 3 与后 4 个字符、总长不足 8 个字符时不泄露任何字符。
func TestMaskSecret(t *testing.T) {
	cases := []struct {
		plain string
		want  string
	}{
		{plain: "", want: "****"},
		{plain: "abc", want: "****"},
		{plain: "1234567", want: "****"},
		{plain: "12345678", want: "123...5678"},
		{plain: "sk-abcdefgh", want: "sk-...efgh"},
		{plain: "中文密钥一二三四五六", want: "中文密...三四五六"},
		{plain: "七字符密钥一二三四", want: "七字符...一二三四"},
		{plain: "sk-🔑-secret", want: "sk-...cret"},
	}
	for _, tc := range cases {
		if got := maskSecret(tc.plain); got != tc.want {
			t.Errorf("maskSecret(%q) = %q，期望 %q", tc.plain, got, tc.want)
		}
	}
	// 任何情况下都不能返回完整明文。
	for _, plain := range []string{"12345678", "sk-abcdefgh", "中文密钥一二三四五六"} {
		if maskSecret(plain) == plain {
			t.Errorf("掩码不得等于明文: %q", plain)
		}
	}
}

// TestServiceEncryptAndCanDecryptSecret 覆盖 Service 的密钥加解密端口（admin 消费方）。
func TestServiceEncryptAndCanDecryptSecret(t *testing.T) {
	svc := testService(Deps{})
	cipher, err := svc.EncryptSecret("sk-managed-secret")
	if err != nil {
		t.Fatalf("EncryptSecret 失败: %v", err)
	}
	if !strings.HasPrefix(cipher, secretPrefix) {
		t.Fatalf("EncryptSecret 输出缺少前缀: %q", cipher)
	}
	if !svc.CanDecryptSecret(cipher) {
		t.Fatal("当前密钥应能解开自己加密的密文")
	}
	if !svc.CanDecryptSecret("") {
		t.Fatal("空密文应视为可解密")
	}
	if svc.CanDecryptSecret("v1:broken") {
		t.Fatal("损坏密文不应视为可解密")
	}

	other := testService(Deps{SecretKey: []byte("fedcba9876543210fedcba9876543210")})
	if other.CanDecryptSecret(cipher) {
		t.Fatal("不同主密钥不应能解开密文")
	}

	broken := testService(Deps{SecretKey: []byte("short")})
	if _, err := broken.EncryptSecret("sk-x"); err == nil {
		t.Fatal("错误长度的主密钥应导致加密失败")
	}
	if broken.CanDecryptSecret(cipher) {
		t.Fatal("错误长度的主密钥不应能解开密文")
	}
}
