package ai

import (
	"context"
	"fmt"
	"strings"

	"github.com/great-magician-01/timeline/internal/access"
	"github.com/great-magician-01/timeline/internal/settings"
)

// WebSearchConfig 是 Agent 执行联网搜索所需的全局配置。
// APIKey 只在服务端内存中使用，不得写入响应、日志、审计或调用记录。
type WebSearchConfig struct {
	BaseURL string
	APIKey  string
}

// WebSearchConfig 读取全局独立搜索密钥并解密；与任何 AI 的模型接口预设无关。
func (s *Service) WebSearchConfig(ctx context.Context, q access.Querier) (WebSearchConfig, error) {
	values, err := settings.LoadWebSearch(ctx, q)
	if err != nil {
		return WebSearchConfig{}, err
	}
	return decodeWebSearchConfig(s.secretKey, values)
}

// CanDecryptSecret 只验证密文能否用当前主密钥解开；不返回明文。
func (s *Service) CanDecryptSecret(encoded string) bool {
	if encoded == "" {
		return true
	}
	_, err := DecryptSecret(s.secretKey, encoded)
	return err == nil
}

// decodeWebSearchConfig 把数据库快照解密为运行期配置；空密文表示未配置。
func decodeWebSearchConfig(secretKey []byte, values settings.WebSearchValues) (WebSearchConfig, error) {
	if !values.Configured() {
		return WebSearchConfig{BaseURL: values.BaseURL}, nil
	}
	if strings.TrimSpace(values.BaseURL) == "" {
		return WebSearchConfig{}, fmt.Errorf("网页搜索密钥已保存，但 Base URL 缺失")
	}
	apiKey, err := DecryptSecret(secretKey, values.APIKeyCipher)
	if err != nil {
		return WebSearchConfig{BaseURL: values.BaseURL}, fmt.Errorf("解密全局网页搜索密钥失败: %w", err)
	}
	return WebSearchConfig{BaseURL: values.BaseURL, APIKey: apiKey}, nil
}

// EncryptSecret 使用服务持有的 32 字节密钥加密敏感配置，供管理后台保存
// 全局网页搜索密钥，避免 admin 包直接接触 secret key。
func (s *Service) EncryptSecret(plain string) (string, error) {
	return EncryptSecret(s.secretKey, plain)
}
