package admin

import (
	"errors"
	"net/http"
	"strings"
	"unicode"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/audit"
	"github.com/great-magician-01/timeline/internal/auth"
	"github.com/great-magician-01/timeline/internal/settings"
	"github.com/great-magician-01/timeline/internal/websearch"
)

const maxWebSearchAPIKeyBytes = 2048

var errSearchCipherUnavailable = errors.New("网页搜索密钥加密器不可用")

// webSearchSettingsPatch 是全局网页搜索配置的部分更新请求。
// api_key 是只写字段；clear_api_key 用于逻辑清除已保存的密钥。
type webSearchSettingsPatch struct {
	BaseURL     *string `json:"base_url"`
	APIKey      *string `json:"api_key"`
	ClearAPIKey *bool   `json:"clear_api_key"`
}

func (p webSearchSettingsPatch) empty() bool {
	return p.BaseURL == nil && p.APIKey == nil && p.ClearAPIKey == nil
}

type webSearchSettingsResponse struct {
	BaseURL      string `json:"base_url"`
	Configured   bool   `json:"configured"`
	Usable       bool   `json:"usable"`
	APIKeyMasked string `json:"api_key_masked"`
}

func webSearchResponse(values settings.WebSearchValues, usable bool) webSearchSettingsResponse {
	masked := ""
	if values.Configured() {
		masked = "••••••••"
	}
	return webSearchSettingsResponse{
		BaseURL:      values.BaseURL,
		Configured:   values.Configured(),
		Usable:       usable,
		APIKeyMasked: masked,
	}
}

// webSearchConfigured 判断配置是否真正可用：Base URL 存在且密钥能由当前主密钥解密。
func (s *Service) webSearchConfigured(values settings.WebSearchValues) bool {
	if !values.Ready() {
		return false
	}
	if s.secretCipher != nil && !s.secretCipher.CanDecryptSecret(values.APIKeyCipher) {
		return false
	}
	return true
}

// GetWebSearchSettings 返回全局网页搜索配置；永不返回明文或密文。
func (s *Service) GetWebSearchSettings(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	values, err := settings.LoadWebSearch(c.Request.Context(), pool)
	if err != nil {
		s.internalError(c, "读取网页搜索设置失败", err)
		return
	}
	c.JSON(http.StatusOK, webSearchResponse(values, s.webSearchConfigured(values)))
}

// UpdateWebSearchSettings 保存全局网页搜索 Base URL 与独立 API Key。
// 密钥使用 ENCRYPTION_KEY 派生的 AES-256-GCM 密钥加密；审计不含密钥或密文。
func (s *Service) UpdateWebSearchSettings(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	var in webSearchSettingsPatch
	if err := api.DecodeJSON(c, &in); err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", "请求体必须是有效的 JSON 对象")
		return
	}
	if in.empty() {
		api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", "至少需要修改一项搜索设置",
			gin.H{"settings": "请求没有包含任何搜索设置项"})
		return
	}

	current, err := settings.LoadWebSearch(c.Request.Context(), pool)
	if err != nil {
		s.internalError(c, "读取网页搜索设置失败", err)
		return
	}
	next, details, patchErr := applyWebSearchPatch(current, in, s.secretCipher)
	if patchErr != nil {
		s.internalError(c, "处理网页搜索设置失败", patchErr)
		return
	}
	if len(details) > 0 {
		api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", "网页搜索设置不符合要求", details)
		return
	}

	adminUser, _ := auth.CurrentUser(c)
	ctx := c.Request.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		s.internalError(c, "开启网页搜索设置事务失败", err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	keyChanged := next.APIKeyCipher != current.APIKeyCipher
	if next.BaseURL != current.BaseURL || (keyChanged && strings.TrimSpace(next.BaseURL) != "") {
		if err := settings.Set(ctx, tx, settings.KeyWebSearchBaseURL, next.BaseURL, adminUser.ID); err != nil {
			s.internalError(c, "保存网页搜索 Base URL 失败", err)
			return
		}
	}
	if keyChanged {
		if err := settings.Set(ctx, tx, settings.KeyWebSearchAPIKeyCipher, next.APIKeyCipher, adminUser.ID); err != nil {
			s.internalError(c, "保存网页搜索密钥失败", err)
			return
		}
	}
	if err := s.insertAudit(ctx, tx, adminUser.ID, audit.ActionSettingsUpdate, "web_search_settings", nil, gin.H{
		"base_url":   next.BaseURL,
		"configured": next.Configured(),
	}); err != nil {
		s.internalError(c, "写入网页搜索设置审计失败", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		s.internalError(c, "提交网页搜索设置事务失败", err)
		return
	}

	c.JSON(http.StatusOK, webSearchResponse(next, s.webSearchConfigured(next)))
}

// applyWebSearchPatch 校验并应用部分更新（纯逻辑，便于单元测试）。
// 空 api_key 表示保留原密钥；clear_api_key 把密文逻辑清除为空字符串。
func applyWebSearchPatch(
	current settings.WebSearchValues,
	in webSearchSettingsPatch,
	cipher SecretCipher,
) (settings.WebSearchValues, map[string]string, error) {
	next := current
	details := map[string]string{}
	if in.BaseURL != nil {
		normalized, err := websearch.NormalizeBaseURL(*in.BaseURL)
		if err != nil {
			details["base_url"] = err.Error()
		} else {
			next.BaseURL = normalized
		}
	}

	apiKey := ""
	if in.APIKey != nil {
		apiKey = strings.TrimSpace(*in.APIKey)
		if len(apiKey) > maxWebSearchAPIKeyBytes {
			details["api_key"] = "搜索 API Key 长度超出限制"
		} else if strings.IndexFunc(apiKey, unicode.IsControl) >= 0 {
			details["api_key"] = "搜索 API Key 不能包含换行或控制字符"
		}
	}
	clearAPIKey := in.ClearAPIKey != nil && *in.ClearAPIKey
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
	if in.BaseURL == nil && !keyChanged {
		details["settings"] = "输入为空或未发生变更"
	}
	return next, details, nil
}
