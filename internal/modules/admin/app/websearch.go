package app

import (
	"context"
	"errors"
	"strings"
	"unicode"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/websearch"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// maxWebSearchAPIKeyBytes 是搜索 API Key 的长度上限。
const maxWebSearchAPIKeyBytes = 2048

// errSearchCipherUnavailable 表示保存搜索密钥所需的加密端口未装配。
var errSearchCipherUnavailable = errors.New("网页搜索密钥加密器不可用")

// GetWebSearchSettings 返回全局网页搜索配置；永不返回明文或密文。
func (s *Service) GetWebSearchSettings(ctx context.Context) (WebSearchSnapshot, error) {
	values, err := s.settings.WebSearch(ctx)
	if err != nil {
		return WebSearchSnapshot{}, wrapInternal(err)
	}
	return webSearchSnapshot(values, s.webSearchUsable(values)), nil
}

// UpdateWebSearchSettings 保存全局网页搜索 Base URL 与独立 API Key：
// 密钥经 contracts.SecretCipher 加密，审计与写入同事务且不包含密钥/密文。
func (s *Service) UpdateWebSearchSettings(ctx context.Context, actor model.Viewer, patch WebSearchPatch) (WebSearchSnapshot, error) {
	if patch.Empty() {
		return WebSearchSnapshot{}, apperr.Validation("至少需要修改一项搜索设置").
			WithDetails(map[string]string{"settings": "请求没有包含任何搜索设置项"})
	}
	now := s.now()
	err := s.tx.WithTx(ctx, func(txCtx context.Context) error {
		current, err := s.settings.WebSearch(txCtx)
		if err != nil {
			return wrapInternal(err)
		}
		next, details, patchErr := applyWebSearchPatch(current, patch, s.cipher)
		if patchErr != nil {
			return apperr.Wrap(patchErr, apperr.KindInternal, "")
		}
		if len(details) > 0 {
			return apperr.Validation("网页搜索设置不符合要求").WithDetails(details)
		}
		keyChanged := next.APIKeyCipher != current.APIKeyCipher
		if next.BaseURL != current.BaseURL || (keyChanged && strings.TrimSpace(next.BaseURL) != "") {
			if err := s.settings.Set(txCtx, contracts.SettingsWrite{
				Key: contracts.SettingKeyWebSearchBaseURL, Value: next.BaseURL,
				AdminID: actor.ID, At: now,
			}); err != nil {
				return wrapInternal(err)
			}
		}
		if keyChanged {
			if err := s.settings.Set(txCtx, contracts.SettingsWrite{
				Key: contracts.SettingKeyWebSearchAPIKeyCipher, Value: next.APIKeyCipher,
				AdminID: actor.ID, At: now,
			}); err != nil {
				return wrapInternal(err)
			}
		}
		return s.audit.Write(txCtx, contracts.AuditEntry{
			AdminID:    actor.ID,
			Action:     contracts.AuditActionSettingsUpdate,
			TargetType: "web_search_settings",
			Detail: map[string]any{
				"base_url":   next.BaseURL,
				"configured": next.Configured(),
			},
			At: now,
		})
	})
	if err != nil {
		return WebSearchSnapshot{}, err
	}
	// 提交后重新读取，保证 configured/usable 反映落库后的真实状态。
	values, err := s.settings.WebSearch(ctx)
	if err != nil {
		return WebSearchSnapshot{}, wrapInternal(err)
	}
	return webSearchSnapshot(values, s.webSearchUsable(values)), nil
}

// webSearchUsable 判断配置是否真正可用：Base URL 存在且密钥能被当前主密钥解密。
func (s *Service) webSearchUsable(values contracts.WebSearchSettings) bool {
	if !values.Ready() {
		return false
	}
	if s.cipher == nil {
		return true
	}
	return s.cipher.CanDecryptSecret(values.APIKeyCipher)
}

// webSearchSnapshot 组装管理端读取结果；只返回是否配置的掩码，不返回密钥内容。
func webSearchSnapshot(values contracts.WebSearchSettings, usable bool) WebSearchSnapshot {
	masked := ""
	if values.Configured() {
		masked = "••••••••"
	}
	return WebSearchSnapshot{
		BaseURL:      values.BaseURL,
		Configured:   values.Configured(),
		Usable:       usable,
		APIKeyMasked: masked,
	}
}

// applyWebSearchPatch 校验并应用部分更新（纯逻辑，便于单元测试）：
// 空 api_key 表示保留原密钥；clear_api_key 把密文逻辑清除为空字符串。
func applyWebSearchPatch(
	current contracts.WebSearchSettings,
	patch WebSearchPatch,
	cipher contracts.SecretCipher,
) (contracts.WebSearchSettings, map[string]string, error) {
	next := current
	details := map[string]string{}
	if patch.BaseURL != nil {
		normalized, err := websearch.NormalizeBaseURL(*patch.BaseURL)
		if err != nil {
			details["base_url"] = err.Error()
		} else {
			next.BaseURL = normalized
		}
	}

	apiKey := ""
	if patch.APIKey != nil {
		apiKey = strings.TrimSpace(*patch.APIKey)
		if len(apiKey) > maxWebSearchAPIKeyBytes {
			details["api_key"] = "搜索 API Key 长度超出限制"
		} else if strings.IndexFunc(apiKey, unicode.IsControl) >= 0 {
			details["api_key"] = "搜索 API Key 不能包含换行或控制字符"
		}
	}
	clearAPIKey := patch.ClearAPIKey != nil && *patch.ClearAPIKey
	if clearAPIKey && apiKey != "" {
		details["api_key"] = "不能同时保存新密钥和清除旧密钥"
	}
	if len(details) > 0 {
		return next, details, nil
	}

	keyChanged := false
	switch {
	case clearAPIKey:
		next.APIKeyCipher = ""
		keyChanged = true
	case apiKey != "":
		if cipher == nil {
			return next, nil, errSearchCipherUnavailable
		}
		encrypted, err := cipher.EncryptSecret(apiKey)
		if err != nil {
			return next, nil, err
		}
		next.APIKeyCipher = encrypted
		keyChanged = true
	}
	if strings.TrimSpace(next.BaseURL) == "" {
		next.BaseURL = websearch.DefaultBaseURL
	}
	if patch.BaseURL == nil && !keyChanged {
		details["settings"] = "输入为空或未发生变更"
	}
	return next, details, nil
}
