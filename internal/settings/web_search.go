package settings

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/great-magician-01/timeline/internal/websearch"
)

// 全局网页搜索设置键。搜索能力独立于模型接口预设：所有开启联网搜索的 AI
// 共用这里的 Base URL 与加密密钥。
const (
	KeyWebSearchBaseURL      = "web_search.base_url"
	KeyWebSearchAPIKeyCipher = "web_search.api_key_cipher"
)

// WebSearchValues 是全局搜索配置的读取快照。
// APIKeyCipher 只允许在服务端内部解密，不得进入管理接口响应或日志。
type WebSearchValues struct {
	BaseURL      string
	APIKeyCipher string
}

// DefaultWebSearch 返回未配置时使用的安全默认值。
func DefaultWebSearch() WebSearchValues {
	return WebSearchValues{BaseURL: websearch.DefaultBaseURL}
}

// LoadWebSearch 读取全局网页搜索设置。
// 只有“没有密钥且没有显式 Base URL”时才回退默认地址；若存在密钥但 Base URL
// 缺失或为空，则保持 BaseURL 为空，由调用方失败关闭，避免把密钥发往默认服务。
func LoadWebSearch(ctx context.Context, q Querier) (WebSearchValues, error) {
	values := WebSearchValues{}
	baseURLSet := false
	rows, err := q.Query(ctx, `
		SELECT key, value FROM system_settings
		WHERE key = ANY($1)`, []string{KeyWebSearchBaseURL, KeyWebSearchAPIKeyCipher})
	if err != nil {
		return values, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var raw []byte
		if err := rows.Scan(&key, &raw); err != nil {
			return values, err
		}
		switch key {
		case KeyWebSearchBaseURL:
			if value, ok := stringValue(raw); ok && strings.TrimSpace(value) != "" {
				values.BaseURL = strings.TrimSpace(value)
				baseURLSet = true
			}
		case KeyWebSearchAPIKeyCipher:
			if value, ok := stringValue(raw); ok {
				values.APIKeyCipher = value
			}
		}
	}
	if err := rows.Err(); err != nil {
		return values, err
	}
	if !baseURLSet {
		if values.Configured() {
			values.BaseURL = ""
		} else {
			values.BaseURL = websearch.DefaultBaseURL
		}
	}
	return values, nil
}

// Configured 判断是否已保存搜索密钥。
func (v WebSearchValues) Configured() bool {
	return strings.TrimSpace(v.APIKeyCipher) != ""
}

// Ready 判断密钥与 Base URL 是否同时可用。
func (v WebSearchValues) Ready() bool {
	return v.Configured() && strings.TrimSpace(v.BaseURL) != ""
}

func stringValue(raw []byte) (string, bool) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	return value, true
}
