package app

import (
	"context"
	"errors"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/ai/domain"
	"github.com/great-magician-01/timeline/internal/modules/ai/repo"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
)

// RuntimeByAI 组装一次 AI 决策所需的配置快照（含解密后的预设 API Key）。消费者：agent。
//
// AI 用户不存在或不是 AI 时返回 not_found；资料/模型/行为缺失时使用安全默认值，
// 由调用方依据 Provider.ID / IsEnabled 判断可用性。
func (s *Service) RuntimeByAI(ctx context.Context, aiID int64) (contracts.Runtime, error) {
	user, err := s.deps.Users.GetUser(ctx, aiID)
	if err != nil {
		if apperr.IsKind(err, apperr.KindNotFound) {
			return contracts.Runtime{}, apperr.NotFound("AI 用户不存在")
		}
		return contracts.Runtime{}, internalError(err, "读取 AI 用户失败")
	}
	if !user.IsAI {
		return contracts.Runtime{}, apperr.NotFound("AI 用户不存在")
	}
	runtime := contracts.Runtime{AIID: aiID, Username: user.Username, Nickname: user.Nickname}

	profile, err := s.repo.GetProfile(ctx, aiID)
	if errors.Is(err, repo.ErrNotFound) {
		profile = domain.Profile{UserID: aiID, TargetLanguage: "zh-CN"}
	} else if err != nil {
		return contracts.Runtime{}, internalError(err, "读取 AI 身份资料失败")
	}
	runtime.Profile = profileToContracts(profile)

	modelSettings, err := s.repo.GetModelSettings(ctx, aiID)
	if errors.Is(err, repo.ErrNotFound) {
		modelSettings = domain.ModelSettings{TimeoutSeconds: 60}
	} else if err != nil {
		return contracts.Runtime{}, internalError(err, "读取 AI 模型设置失败")
	}
	params, err := domain.GenerationParamsFromJSON(modelSettings.RawParams)
	if err != nil {
		return contracts.Runtime{}, internalError(err, "解析 AI 生成参数失败")
	}
	runtime.Params = generationParamsToContracts(params)

	model := modelSettings.Model
	if modelSettings.ProviderID != nil && modelSettings.ProviderAvailable {
		// 预设的运行时字段由 GetModelSettings 一次联表读出；预设缺失/逻辑删除时
		// ProviderAvailable=false，保留零值，调用方按 Provider.ID == 0 判定不可用。
		apiKey, decryptErr := decryptSecret(s.deps.SecretKey, modelSettings.ProviderAPIKeyCipher)
		if decryptErr != nil {
			return contracts.Runtime{}, internalError(decryptErr, "解密模型接口预设的 API Key 失败")
		}
		runtime.Provider = providerConfigToContracts(domain.ProviderConfig{
			ID:           *modelSettings.ProviderID,
			Name:         modelSettings.ProviderName,
			BaseURL:      modelSettings.ProviderBaseURL,
			APIKey:       apiKey,
			DefaultModel: modelSettings.ProviderDefaultModel,
			IsEnabled:    modelSettings.ProviderIsEnabled,
		})
		if model == "" {
			model = modelSettings.ProviderDefaultModel
		}
	}
	runtime.Model = model

	behavior, _, err := s.repo.GetBehavior(ctx, aiID)
	if errors.Is(err, repo.ErrNotFound) {
		behavior = domain.DefaultBehaviorSettings()
	} else if err != nil {
		return contracts.Runtime{}, internalError(err, "读取 AI 行为设置失败")
	}
	runtime.Behavior = behaviorSettingsToContracts(behavior)

	timeout := modelSettings.TimeoutSeconds
	if timeout < 5 || timeout > 600 {
		timeout = 60
	}
	runtime.Timeout = time.Duration(timeout) * time.Second
	return runtime, nil
}

// EnabledAIIDs 返回所有启用中的 AI 用户 ID（调度器每轮读取）。消费者：agent。
func (s *Service) EnabledAIIDs(ctx context.Context) ([]int64, error) {
	ids, err := s.deps.Users.ListUserIDsByAIFlag(ctx, true)
	if err != nil {
		return nil, internalError(err, "读取 AI 用户列表失败")
	}
	enabled, err := s.repo.EnabledAIIDs(ctx, ids)
	if err != nil {
		return nil, internalError(err, "读取启用中的 AI 失败")
	}
	return enabled, nil
}

// ProfileEnabled 轻量读取某个 AI 的启用状态（不装配完整运行时快照）。
// 消费者：agent（模型返回后复核配置，保留「等待模型期间配置可能变化」的安全语义）。
func (s *Service) ProfileEnabled(ctx context.Context, aiID int64) (bool, error) {
	if aiID <= 0 {
		return false, nil
	}
	enabled, err := s.repo.GetProfileEnabled(ctx, aiID)
	if err != nil {
		return false, internalError(err, "读取 AI 启用状态失败")
	}
	return enabled, nil
}

// WebSearchConfig 读取并解密全局联网搜索配置。消费者：agent。
func (s *Service) WebSearchConfig(ctx context.Context) (contracts.WebSearchConfig, error) {
	values, err := s.deps.Settings.WebSearch(ctx)
	if err != nil {
		return contracts.WebSearchConfig{}, internalError(err, "读取全局网页搜索设置失败")
	}
	if !values.Configured() {
		return contracts.WebSearchConfig{BaseURL: values.BaseURL}, nil
	}
	if !values.Ready() {
		return contracts.WebSearchConfig{}, internalError(
			errors.New("搜索密钥已保存但 Base URL 缺失"), "全局网页搜索配置无效")
	}
	apiKey, err := decryptSecret(s.deps.SecretKey, values.APIKeyCipher)
	if err != nil {
		return contracts.WebSearchConfig{BaseURL: values.BaseURL},
			internalError(err, "解密全局网页搜索密钥失败")
	}
	return contracts.WebSearchConfig{BaseURL: values.BaseURL, APIKey: apiKey}, nil
}

// EncryptSecret 使用服务持有的 32 字节密钥加密敏感配置。消费者：admin。
func (s *Service) EncryptSecret(plain string) (string, error) {
	return encryptSecret(s.deps.SecretKey, plain)
}

// CanDecryptSecret 只验证密文能否用当前主密钥解开；不返回明文。消费者：admin。
func (s *Service) CanDecryptSecret(encoded string) bool {
	if encoded == "" {
		return true
	}
	_, err := decryptSecret(s.deps.SecretKey, encoded)
	return err == nil
}

// profileToContracts 映射身份资料视图。
func profileToContracts(profile domain.Profile) contracts.Profile {
	return contracts.Profile{
		UserID:         profile.UserID,
		Background:     profile.Background,
		Personality:    profile.Personality,
		CoreValues:     profile.CoreValues,
		LanguageStyle:  profile.LanguageStyle,
		Topics:         profile.Topics,
		TargetLanguage: profile.TargetLanguage,
		Taboos:         profile.Taboos,
		SystemPrompt:   profile.SystemPrompt,
		IsEnabled:      profile.IsEnabled,
	}
}

// providerConfigToContracts 映射预设运行时视图。
func providerConfigToContracts(provider domain.ProviderConfig) contracts.ProviderConfig {
	return contracts.ProviderConfig{
		ID:           provider.ID,
		Name:         provider.Name,
		BaseURL:      provider.BaseURL,
		APIKey:       provider.APIKey,
		DefaultModel: provider.DefaultModel,
		IsEnabled:    provider.IsEnabled,
	}
}

// generationParamsToContracts 映射生成参数视图。
func generationParamsToContracts(params domain.GenerationParams) contracts.GenerationParams {
	return contracts.GenerationParams{
		MaxTokens:   params.MaxTokens,
		Temperature: params.Temperature,
		TopP:        params.TopP,
		Stop:        params.Stop,
	}
}

// behaviorSettingsToContracts 映射行为策略视图。
func behaviorSettingsToContracts(settings domain.BehaviorSettings) contracts.BehaviorSettings {
	return contracts.BehaviorSettings{
		AllowOriginalPost:    settings.AllowOriginalPost,
		AllowActiveComment:   settings.AllowActiveComment,
		AllowPassiveReply:    settings.AllowPassiveReply,
		AllowLike:            settings.AllowLike,
		AllowFollow:          settings.AllowFollow,
		AllowUnfollow:        settings.AllowUnfollow,
		AllowRepost:          settings.AllowRepost,
		AllowQuote:           settings.AllowQuote,
		AllowAIToAI:          settings.AllowAIToAI,
		AllowWebSearch:       settings.AllowWebSearch,
		CandidateScopeType:   settings.CandidateScopeType,
		CandidateScopeParams: settings.CandidateScopeParams,
		RateLimits:           settings.RateLimits,
		CooldownSeconds:      settings.CooldownSeconds,
		ActiveHoursStart:     settings.ActiveHoursStart,
		ActiveHoursEnd:       settings.ActiveHoursEnd,
		Schedule:             settings.Schedule,
		MaxReplyDepth:        settings.MaxReplyDepth,
	}
}
