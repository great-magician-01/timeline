package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/great-magician-01/timeline/internal/modules/ai/domain"
	"github.com/great-magician-01/timeline/internal/modules/ai/repo"
	"github.com/great-magician-01/timeline/internal/platform/aiclient"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
)

// ProviderFilter 是预设列表过滤条件。
type ProviderFilter struct {
	Query  string
	Status string // "" / "enabled" / "disabled"
	Cursor int64
	Limit  int
}

// ListProviders 返回预设列表（游标倒序分页）与每个预设关联的 AI 数量。
func (s *Service) ListProviders(ctx context.Context, filter ProviderFilter) (ProviderList, error) {
	records, err := s.repo.ListProviders(ctx, repo.ListProvidersFilter{
		Query:  strings.TrimSpace(filter.Query),
		Status: strings.TrimSpace(filter.Status),
		Cursor: filter.Cursor,
		Limit:  filter.Limit + 1,
	})
	if err != nil {
		return ProviderList{}, internalError(err, "查询模型接口预设列表失败")
	}
	hasMore := len(records) > filter.Limit
	if hasMore {
		records = records[:filter.Limit]
	}
	ids := make([]int64, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.ID)
	}
	counts, err := s.repo.ProviderAICounts(ctx, ids)
	if err != nil {
		return ProviderList{}, internalError(err, "统计预设关联 AI 数量失败")
	}
	items := make([]ProviderItem, 0, len(records))
	for _, record := range records {
		items = append(items, s.providerItem(record, counts[record.ID]))
	}
	var lastID int64
	if len(records) > 0 {
		lastID = records[len(records)-1].ID
	}
	return ProviderList{Items: items, LastID: lastID, HasMore: hasMore}, nil
}

// CreateProviderInput 是创建预设的请求数据。
type CreateProviderInput struct {
	Name         string
	BaseURL      string
	APIKey       string
	DefaultModel string
}

// CreateProvider 创建模型接口预设；密钥加密存储，审计与写入同事务。
func (s *Service) CreateProvider(ctx context.Context, adminID int64, in CreateProviderInput) (ProviderItem, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.BaseURL = strings.TrimSpace(in.BaseURL)
	in.DefaultModel = strings.TrimSpace(in.DefaultModel)
	in.APIKey = strings.TrimSpace(in.APIKey)
	if details := domain.ValidateProviderFields(in.Name, in.BaseURL, in.DefaultModel); len(details) > 0 {
		return ProviderItem{}, validationError(details)
	}
	cipher, err := encryptSecret(s.deps.SecretKey, in.APIKey)
	if err != nil {
		return ProviderItem{}, internalError(err, "加密模型接口预设密钥失败")
	}
	at := s.now()
	var item ProviderItem
	err = s.withTx(ctx, func(txCtx context.Context) error {
		taken, err := s.repo.ProviderNameTaken(txCtx, in.Name, 0)
		if err != nil {
			return internalError(err, "检查预设名称冲突失败")
		}
		if taken {
			return duplicateNameError()
		}
		id, err := s.newID()
		if err != nil {
			return err
		}
		if err := s.repo.CreateProvider(txCtx, repo.CreateProviderInput{
			ID: id, Name: in.Name, BaseURL: in.BaseURL, APIKeyCipher: cipher,
			DefaultModel: in.DefaultModel, At: at,
		}); err != nil {
			if isUniqueViolation(err) {
				return duplicateNameError()
			}
			return internalError(err, "创建模型接口预设失败")
		}
		// 审计明细只记录名称等非敏感字段。
		if err := s.writeAudit(txCtx, adminID, contracts.AuditActionProviderCreate, targetModelProvider, &id,
			map[string]any{"name": in.Name}, at); err != nil {
			return err
		}
		record, err := s.loadProvider(txCtx, id)
		if err != nil {
			return err
		}
		item = s.providerItem(record, 0)
		return nil
	})
	if err != nil {
		return ProviderItem{}, err
	}
	return item, nil
}

// UpdateProviderInput 是更新预设的补丁；nil 表示不变更。
type UpdateProviderInput struct {
	Name         *string
	BaseURL      *string
	APIKey       *string
	DefaultModel *string
	IsEnabled    *bool
}

// UpdateProvider 更新预设；api_key 缺省或为空表示不变更密钥。
func (s *Service) UpdateProvider(
	ctx context.Context,
	adminID, providerID int64,
	in UpdateProviderInput,
) (ProviderItem, error) {
	at := s.now()
	var item ProviderItem
	err := s.withTx(ctx, func(txCtx context.Context) error {
		current, err := s.loadProvider(txCtx, providerID)
		if err != nil {
			return err
		}
		name := current.Name
		if in.Name != nil {
			name = strings.TrimSpace(*in.Name)
		}
		baseURL := current.BaseURL
		if in.BaseURL != nil {
			baseURL = strings.TrimSpace(*in.BaseURL)
		}
		defaultModel := current.DefaultModel
		if in.DefaultModel != nil {
			defaultModel = strings.TrimSpace(*in.DefaultModel)
		}
		if details := domain.ValidateProviderFields(name, baseURL, defaultModel); len(details) > 0 {
			return validationError(details)
		}
		if name != current.Name {
			taken, err := s.repo.ProviderNameTaken(txCtx, name, providerID)
			if err != nil {
				return internalError(err, "检查预设名称冲突失败")
			}
			if taken {
				return duplicateNameError()
			}
		}
		cipher := current.APIKeyCipher
		keyChanged := false
		if in.APIKey != nil && strings.TrimSpace(*in.APIKey) != "" {
			cipher, err = encryptSecret(s.deps.SecretKey, strings.TrimSpace(*in.APIKey))
			if err != nil {
				return internalError(err, "加密模型接口预设密钥失败")
			}
			keyChanged = true
		}
		isEnabled := current.IsEnabled
		if in.IsEnabled != nil {
			isEnabled = *in.IsEnabled
		}
		// 停用预设前检查关联 AI：仍有依赖时返回 409，避免后台任务在配置缺失时反复失败。
		if current.IsEnabled && !isEnabled {
			counts, err := s.repo.ProviderAICounts(txCtx, []int64{providerID})
			if err != nil {
				return internalError(err, "统计预设关联 AI 数量失败")
			}
			if aiCount, blocked := domain.ProviderDisableConflict(current.IsEnabled, isEnabled, counts[providerID]); blocked {
				return apperr.Conflict("duplicate", domain.ProviderDisableConflictMessage(aiCount)).
					WithDetails(map[string]int{"ai_count": aiCount})
			}
		}
		if err := s.repo.UpdateProvider(txCtx, repo.UpdateProviderInput{
			ID: providerID, Name: name, BaseURL: baseURL, APIKeyCipher: cipher,
			DefaultModel: defaultModel, IsEnabled: isEnabled, At: at,
		}); err != nil {
			if isUniqueViolation(err) {
				return duplicateNameError()
			}
			return internalError(err, "更新模型接口预设失败")
		}
		if err := s.writeAudit(txCtx, adminID, contracts.AuditActionProviderUpdate, targetModelProvider, &providerID,
			map[string]any{"name": name, "api_key_changed": keyChanged, "is_enabled": isEnabled}, at); err != nil {
			return err
		}
		record, err := s.loadProvider(txCtx, providerID)
		if err != nil {
			return err
		}
		counts, err := s.repo.ProviderAICounts(txCtx, []int64{providerID})
		if err != nil {
			return internalError(err, "统计预设关联 AI 数量失败")
		}
		item = s.providerItem(record, counts[providerID])
		return nil
	})
	if err != nil {
		return ProviderItem{}, err
	}
	return item, nil
}

// DeleteProvider 逻辑删除预设；仍有关联 AI 时返回 409 duplicate。
func (s *Service) DeleteProvider(ctx context.Context, adminID, providerID int64) error {
	at := s.now()
	return s.withTx(ctx, func(txCtx context.Context) error {
		if _, err := s.loadProvider(txCtx, providerID); err != nil {
			return err
		}
		counts, err := s.repo.ProviderAICounts(txCtx, []int64{providerID})
		if err != nil {
			return internalError(err, "统计预设关联 AI 数量失败")
		}
		if aiCount := counts[providerID]; aiCount > 0 {
			return apperr.Conflict("duplicate", domain.ProviderDisableConflictMessage(aiCount)).
				WithDetails(map[string]int{"ai_count": aiCount})
		}
		if err := s.repo.SoftDeleteProvider(txCtx, providerID, at); err != nil {
			return internalError(err, "删除模型接口预设失败")
		}
		return s.writeAudit(txCtx, adminID, contracts.AuditActionProviderDelete, targetModelProvider, &providerID,
			map[string]any{}, at)
	})
}

// ProviderTestResult 是连接测试结果；无论成功失败 HTTP 都为 200（错误信息经脱敏）。
type ProviderTestResult struct {
	OK        bool
	LatencyMS int64
	Error     string
	Models    []string
}

// TestProvider 用 ListModels 做连接测试。
func (s *Service) TestProvider(ctx context.Context, adminID, providerID int64) (ProviderTestResult, error) {
	if allowed, retryAfter := s.providerTestLimiter.Consume("admin:" + formatID(adminID)); !allowed {
		return ProviderTestResult{}, apperr.RateLimited(retryAfter)
	}
	record, err := s.loadProvider(ctx, providerID)
	if err != nil {
		return ProviderTestResult{}, err
	}
	apiKey, err := decryptSecret(s.deps.SecretKey, record.APIKeyCipher)
	if err != nil {
		s.deps.Log.Warn("解密模型接口预设密钥失败", "provider_id", record.ID, "err", err)
		return ProviderTestResult{OK: false, Error: "API Key 解密失败，请在预设中重新保存密钥"}, nil
	}
	client := aiclient.NewChatClient(aiclient.ProviderConfig{
		ID:           record.ID,
		Name:         record.Name,
		BaseURL:      record.BaseURL,
		APIKey:       apiKey,
		DefaultModel: record.DefaultModel,
		IsEnabled:    record.IsEnabled,
	}, aiclient.GenerationParams{}, aiclient.ProviderTestTimeout)
	testCtx, cancel := context.WithTimeout(ctx, aiclient.ProviderTestTimeout)
	defer cancel()

	started := s.now()
	models, callErr := client.ListModels(testCtx)
	latency := s.now().Sub(started).Milliseconds()
	if callErr != nil {
		return ProviderTestResult{OK: false, LatencyMS: latency, Error: providerTestErrorMessage(callErr, apiKey)}, nil
	}
	const maxModels = 200
	names := make([]string, 0, len(models))
	for _, info := range models {
		if info.ID == "" {
			continue
		}
		names = append(names, info.ID)
		if len(names) >= maxModels {
			break
		}
	}
	return ProviderTestResult{OK: true, LatencyMS: latency, Models: names}, nil
}

// providerItem 组装预设数据视图（密钥只返回掩码）。
func (s *Service) providerItem(record domain.Provider, aiCount int) ProviderItem {
	return ProviderItem{Provider: record, APIKeyMasked: s.maskProviderKey(record), AICount: aiCount}
}

// maskProviderKey 只返回掩码；密文损坏或密钥不匹配时降级为 "****" 并告警。
func (s *Service) maskProviderKey(record domain.Provider) string {
	if record.APIKeyCipher == "" {
		return ""
	}
	plain, err := decryptSecret(s.deps.SecretKey, record.APIKeyCipher)
	if err != nil {
		s.deps.Log.Warn("解密模型接口预设密钥失败", "provider_id", record.ID, "err", err)
		return "****"
	}
	return maskSecret(plain)
}

// loadProvider 读取未删除的预设；不存在时返回 not_found，其它错误不得吞掉。
func (s *Service) loadProvider(ctx context.Context, providerID int64) (domain.Provider, error) {
	record, err := s.repo.GetProvider(ctx, providerID)
	if err != nil {
		if notFound := notFoundIfNoRows(err, "模型接口预设不存在"); notFound != nil {
			return domain.Provider{}, notFound
		}
		return domain.Provider{}, internalError(err, "读取模型接口预设失败")
	}
	return record, nil
}

// duplicateNameError 返回预设重名的 409。
func duplicateNameError() error {
	return apperr.Conflict("duplicate", "同名模型接口预设已存在").
		WithDetails(map[string]string{"name": "名称已被占用"})
}

// providerTestErrorMessage 把底层错误映射为不含密钥的提示。
func providerTestErrorMessage(err error, apiKey string) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "连接超时（超过 15 秒）"
	}
	// 先脱敏再截断，避免密钥正好跨越截断边界而漏出。
	raw := err.Error()
	if apiKey != "" {
		raw = strings.ReplaceAll(raw, apiKey, "***")
	}
	message := truncateRunes(raw, 300)
	var apiErr *aiclient.APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.StatusCode == 401 || apiErr.StatusCode == 403:
			return "连接失败：认证被拒绝，请检查 API Key"
		case apiErr.StatusCode == 404:
			return "连接失败：接口地址不正确（404）"
		case apiErr.StatusCode == 429:
			return "连接失败：请求被上游限流（429）"
		case apiErr.StatusCode >= 500:
			return fmt.Sprintf("连接失败：上游服务错误（%d）", apiErr.StatusCode)
		case apiErr.StatusCode == 0:
			return "连接失败：无法访问接口地址（" + message + "）"
		default:
			return fmt.Sprintf("连接失败：HTTP %d", apiErr.StatusCode)
		}
	}
	return "连接失败：" + message
}

// truncateRunes 按字符截断，避免把超长错误信息写入响应或日志。
func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}
