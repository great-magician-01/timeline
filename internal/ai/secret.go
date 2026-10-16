package ai

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
)

// 密文版本前缀：便于后续更换算法时平滑迁移（解密只接受当前版本）。
const secretPrefix = "v1:"

// EncryptSecret 用 AES-256-GCM 加密接口预设的 API Key。
// 输出格式为 "v1:" + base64url(nonce || ciphertext)；空字符串原样返回空字符串，
// 表示“未设置密钥”。密钥必须是 32 字节。
func EncryptSecret(key []byte, plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("生成加密随机数失败: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return secretPrefix + base64.RawURLEncoding.EncodeToString(sealed), nil
}

// DecryptSecret 解密 EncryptSecret 生成的密文。
// 前缀不符、base64 损坏、认证标签校验失败（密钥不匹配或被篡改）都会返回错误；
// 错误信息不含任何明文或密文内容。
func DecryptSecret(key []byte, encoded string) (string, error) {
	if encoded == "" {
		return "", nil
	}
	if !strings.HasPrefix(encoded, secretPrefix) {
		return "", fmt.Errorf("密文格式无效：缺少 %s 前缀", strings.TrimSuffix(secretPrefix, ":"))
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(encoded, secretPrefix))
	if err != nil {
		return "", fmt.Errorf("密文格式无效：base64 解码失败")
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", fmt.Errorf("密文格式无效：数据长度不足")
	}
	nonce, ciphertext := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("解密失败：密文已损坏或密钥不匹配")
	}
	return string(plain), nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("加密密钥长度必须为 32 字节")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("初始化加密器失败: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("初始化 AES-GCM 失败: %w", err)
	}
	return gcm, nil
}

// MaskSecret 返回展示用掩码：保留前 3 与后 4 个字符，中间固定为 "..."；
// 总长不足 8 时一律返回 "****"。任何情况下都不返回完整明文。
func MaskSecret(plain string) string {
	runes := []rune(plain)
	if len(runes) < 8 {
		return "****"
	}
	return string(runes[:3]) + "..." + string(runes[len(runes)-4:])
}
