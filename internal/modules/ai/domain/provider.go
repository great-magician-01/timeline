package domain

import (
	"fmt"
	"net/url"
	"unicode/utf8"
)

// Provider 字段约束。
const (
	MaxProviderNameRunes    = 100
	MaxProviderBaseURLRunes = 500
	MaxProviderModelRunes   = 200
)

// ValidateProviderFields 校验预设的公共字段（不含密钥策略），返回字段级详情。
func ValidateProviderFields(name, baseURL, defaultModel string) map[string]string {
	details := map[string]string{}
	if runes := utf8.RuneCountInString(name); runes < 1 || runes > MaxProviderNameRunes {
		details["name"] = "名称需为 1-100 个字符"
	}
	if err := ValidateProviderBaseURL(baseURL); err != nil {
		details["base_url"] = err.Error()
	}
	if runes := utf8.RuneCountInString(defaultModel); runes < 1 || runes > MaxProviderModelRunes {
		details["default_model"] = "默认模型不能为空且不超过 200 个字符"
	}
	return details
}

// ValidateProviderBaseURL 校验接口地址：必须是 http/https 完整地址。
func ValidateProviderBaseURL(baseURL string) error {
	if baseURL == "" {
		return fmt.Errorf("接口地址不能为空")
	}
	if utf8.RuneCountInString(baseURL) > MaxProviderBaseURLRunes {
		return fmt.Errorf("接口地址不能超过 500 个字符")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("接口地址必须是 http 或 https 开头的完整地址")
	}
	return nil
}

// ProviderDisableConflict 判断停用预设是否应被拒绝（纯函数，便于单元测试）：
// 只有从启用改为停用且仍有 AI 关联时返回 (关联数量, true)。
func ProviderDisableConflict(enabledBefore, enabledAfter bool, aiCount int) (int, bool) {
	if enabledBefore && !enabledAfter && aiCount > 0 {
		return aiCount, true
	}
	return 0, false
}

// ProviderDisableConflictMessage 生成停用/删除预设被拒绝时的用户文案。
func ProviderDisableConflictMessage(aiCount int) string {
	return fmt.Sprintf("仍有 %d 个 AI 关联该预设，请先解除关联或停用对应 AI", aiCount)
}
