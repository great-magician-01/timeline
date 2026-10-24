package app

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/ai/domain"
	"github.com/great-magician-01/timeline/internal/modules/ai/repo"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/clock"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// AIFilter 是 AI 用户列表过滤条件。
type AIFilter struct {
	Query  string
	Status string // "" / "enabled" / "disabled"
	Cursor int64
	Limit  int
}

// ListAIs 返回 AI 用户列表；账号资料经 contracts.UserReader 读取，模块内配置由 repo 批量读取。
func (s *Service) ListAIs(ctx context.Context, filter AIFilter) (AIList, error) {
	if filter.Limit <= 0 {
		filter.Limit = 20
	}
	ids, err := s.deps.Users.ListUserIDsByAIFlag(ctx, true)
	if err != nil {
		return AIList{}, internalError(err, "查询 AI 用户失败")
	}
	users, err := s.deps.Users.GetUsers(ctx, ids)
	if err != nil {
		return AIList{}, internalError(err, "读取 AI 账号资料失败")
	}
	states, err := s.repo.ListAIStates(ctx, ids)
	if err != nil {
		return AIList{}, internalError(err, "读取 AI 配置状态失败")
	}
	query := strings.ToLower(strings.TrimSpace(filter.Query))
	items := make([]AIListItem, 0, len(ids))
	for _, id := range ids {
		user, ok := users[id]
		if !ok || !user.IsAI {
			continue
		}
		state := states[id]
		if query != "" &&
			!strings.Contains(strings.ToLower(user.Username), query) &&
			!strings.Contains(strings.ToLower(user.Nickname), query) {
			continue
		}
		switch filter.Status {
		case "enabled":
			if !state.IsEnabled {
				continue
			}
		case "disabled":
			if state.IsEnabled {
				continue
			}
		}
		createdAt := user.CreatedAt
		if state.CreatedAt != nil {
			createdAt = *state.CreatedAt
		}
		items = append(items, AIListItem{
			User:         model.UserBriefFromUser(user),
			IsEnabled:    state.IsEnabled,
			ProviderID:   state.ProviderID,
			ProviderName: state.ProviderName,
			Model:        state.Model,
			CreatedAt:    createdAt,
		})
	}
	// 列表按账号 ID 倒序，与旧实现一致。
	sort.Slice(items, func(i, j int) bool { return items[i].User.ID > items[j].User.ID })
	if filter.Cursor > 0 {
		kept := items[:0]
		for _, item := range items {
			if item.User.ID < filter.Cursor {
				kept = append(kept, item)
			}
		}
		items = kept
	}
	hasMore := len(items) > filter.Limit
	if hasMore {
		items = items[:filter.Limit]
	}
	if s.deps.Tasks != nil && len(items) > 0 {
		pageIDs := make([]int64, 0, len(items))
		for _, item := range items {
			pageIDs = append(pageIDs, item.User.ID)
		}
		counts, err := s.deps.Tasks.TodayActionCounts(ctx, pageIDs, clock.DayStart(s.now()))
		if err != nil {
			s.deps.Log.Warn("统计 AI 今日动作失败", "err", err)
		} else {
			for index := range items {
				items[index].TodayActions = counts[items[index].User.ID]
			}
		}
	}
	var lastID int64
	if len(items) > 0 {
		lastID = items[len(items)-1].User.ID
	}
	return AIList{Items: items, LastID: lastID, HasMore: hasMore}, nil
}

// CreateAIInput 是创建 AI 用户的请求数据。
type CreateAIInput struct {
	Identity         domain.CreateInput
	ProviderID       string
	ModelOverride    string
	GenerationParams json.RawMessage
	Behavior         domain.BehaviorPatch
	IsEnabled        *bool
}

// CreateAI 创建 AI 用户：账号（经 auth 端口）、身份资料、模型设置、行为策略与审计同事务完成。
func (s *Service) CreateAI(ctx context.Context, adminID int64, in CreateAIInput) (AIDetail, error) {
	identity := domain.TrimCreateInput(in.Identity)
	details := domain.ValidateCreate(identity)
	providerID, err := parseProviderIDValue(in.ProviderID)
	if err != nil {
		details["provider_id"] = err.Error()
	}
	params, err := domain.ParseGenerationParamsInput(in.GenerationParams)
	if err != nil {
		details["generation_params"] = err.Error()
	}
	behavior, behaviorDetails := domain.PatchAndValidateBehavior(domain.DefaultBehaviorSettings(), in.Behavior)
	for key, message := range behaviorDetails {
		details[key] = message
	}
	if err := domain.ValidateModelOverride(in.ModelOverride); err != nil {
		details["model_override"] = err.Error()
	}
	if len(details) > 0 {
		return AIDetail{}, validationError(details)
	}
	paramsJSON, err := domain.GenerationParamsJSON(params)
	if err != nil {
		return AIDetail{}, internalError(err, "序列化生成参数失败")
	}
	isEnabled := false
	if in.IsEnabled != nil {
		isEnabled = *in.IsEnabled
	}
	at := s.now()
	var detail AIDetail
	err = s.withTx(ctx, func(txCtx context.Context) error {
		providerOK, err := s.repo.EnabledProviderExists(txCtx, providerID)
		if err != nil {
			return internalError(err, "校验模型接口预设失败")
		}
		if !providerOK {
			return apperr.Validation("模型接口预设不存在或未启用").
				WithDetails(map[string]string{"provider_id": "模型接口预设不存在或未启用"})
		}
		user, err := s.deps.Auth.CreateAIUser(txCtx, contracts.CreateAIUserInput{
			Username: identity.Username,
			Nickname: identity.Nickname,
			Bio:      identity.Bio,
			At:       at,
		})
		if err != nil {
			if apperr.IsKind(err, apperr.KindConflict) {
				return apperr.Conflict("duplicate", "用户名已被占用").
					WithDetails(map[string]string{"username": "用户名已被占用"})
			}
			return internalError(err, "创建 AI 账号失败")
		}
		aiID := user.ID
		if err := s.repo.UpsertProfile(txCtx, domain.Profile{
			UserID:         aiID,
			Background:     identity.Background,
			Personality:    identity.Personality,
			CoreValues:     identity.CoreValues,
			LanguageStyle:  identity.LanguageStyle,
			Topics:         identity.Topics,
			TargetLanguage: identity.TargetLanguage,
			Taboos:         identity.Taboos,
			SystemPrompt:   identity.SystemPrompt,
			IsEnabled:      isEnabled,
		}, at); err != nil {
			return internalError(err, "创建 AI 身份资料失败")
		}
		if err := s.repo.UpsertModelSettings(txCtx, aiID, domain.ModelSettings{
			ProviderID:     &providerID,
			ModelOverride:  strings.TrimSpace(in.ModelOverride),
			TimeoutSeconds: 60,
		}, paramsJSON, at); err != nil {
			return internalError(err, "创建 AI 模型设置失败")
		}
		if err := s.repo.UpsertBehavior(txCtx, aiID, behavior, at); err != nil {
			return internalError(err, "创建 AI 行为设置失败")
		}
		if err := s.writeAudit(txCtx, adminID, contracts.AuditActionAICreate, targetAI, &aiID,
			map[string]any{"username": identity.Username, "provider_id": formatID(providerID)}, at); err != nil {
			return err
		}
		detail, err = s.loadAIDetail(txCtx, aiID)
		return err
	})
	if err != nil {
		return AIDetail{}, err
	}
	return detail, nil
}

// GetAI 返回 AI 详情；非 AI 用户 id 返回 404。
func (s *Service) GetAI(ctx context.Context, aiID int64) (AIDetail, error) {
	return s.loadAIDetail(ctx, aiID)
}

// UpdateAIInput 是更新 AI 资料与身份的补丁。
type UpdateAIInput struct {
	Identity domain.UpdateInput
}

// UpdateAI 更新 AI 资料与身份字段（不含模型与行为策略）。
//
// 账号字段（username/nickname/bio）由 auth 模块拥有，经 contracts.AuthUserWriter 更新；
// 身份资料写入 ai_profiles。
func (s *Service) UpdateAI(ctx context.Context, adminID, aiID int64, in UpdateAIInput) (AIDetail, error) {
	if details := domain.ValidateUpdate(in.Identity); len(details) > 0 {
		return AIDetail{}, validationError(details)
	}
	at := s.now()
	var detail AIDetail
	err := s.withTx(ctx, func(txCtx context.Context) error {
		user, err := s.deps.Users.GetUser(txCtx, aiID)
		if err != nil {
			if apperr.IsKind(err, apperr.KindNotFound) {
				return apperr.NotFound("AI 用户不存在")
			}
			return internalError(err, "读取 AI 用户失败")
		}
		if !user.IsAI {
			return apperr.NotFound("AI 用户不存在")
		}
		profile, err := s.repo.GetProfile(txCtx, aiID)
		if err != nil && !errors.Is(err, repo.ErrNotFound) {
			return internalError(err, "读取 AI 身份资料失败")
		}
		profile.UserID = aiID
		mergeProfilePatch(&profile, in.Identity)
		if in.Identity.Username != nil || in.Identity.Nickname != nil || in.Identity.Bio != nil {
			if err := s.deps.Auth.UpdateUserProfile(txCtx, contracts.UpdateUserProfileInput{
				UserID:   aiID,
				Username: in.Identity.Username,
				Nickname: in.Identity.Nickname,
				Bio:      in.Identity.Bio,
				At:       at,
			}); err != nil {
				return err
			}
		}
		if err := s.repo.UpsertProfile(txCtx, profile, at); err != nil {
			return internalError(err, "更新 AI 身份资料失败")
		}
		if err := s.writeAudit(txCtx, adminID, contracts.AuditActionAIUpdate, targetAI, &aiID,
			map[string]any{"username": user.Username}, at); err != nil {
			return err
		}
		detail, err = s.loadAIDetail(txCtx, aiID)
		return err
	})
	if err != nil {
		return AIDetail{}, err
	}
	return detail, nil
}

// UpdateAIModelInput 是更新 AI 模型设置的补丁。
type UpdateAIModelInput struct {
	ProviderID            *string
	ModelOverride         *string
	GenerationParams      json.RawMessage
	RequestTimeoutSeconds *int
}

// UpdateAIModel 更新接口预设、模型覆盖、生成参数与请求超时。
func (s *Service) UpdateAIModel(ctx context.Context, adminID, aiID int64, in UpdateAIModelInput) (ModelResponse, error) {
	details := map[string]string{}
	if in.RequestTimeoutSeconds != nil && (*in.RequestTimeoutSeconds < 5 || *in.RequestTimeoutSeconds > 600) {
		details["request_timeout_seconds"] = "请求超时必须在 5-600 秒之间"
	}
	paramsProvided := len(in.GenerationParams) > 0
	var params domain.GenerationParams
	if paramsProvided {
		parsed, err := domain.ParseGenerationParamsInput(in.GenerationParams)
		if err != nil {
			details["generation_params"] = err.Error()
		} else {
			params = parsed
		}
	}
	var providerID *int64
	if in.ProviderID != nil {
		parsed, err := parseProviderIDValue(*in.ProviderID)
		if err != nil {
			details["provider_id"] = err.Error()
		} else {
			providerID = &parsed
		}
	}
	if in.ModelOverride != nil {
		if err := domain.ValidateModelOverride(*in.ModelOverride); err != nil {
			details["model_override"] = err.Error()
		}
	}
	if len(details) > 0 {
		return ModelResponse{}, validationError(details)
	}
	at := s.now()
	var response ModelResponse
	err := s.withTx(ctx, func(txCtx context.Context) error {
		if err := s.ensureAIUser(txCtx, aiID); err != nil {
			return err
		}
		current, err := s.repo.GetModelSettings(txCtx, aiID)
		if err != nil && !errors.Is(err, repo.ErrNotFound) {
			return internalError(err, "读取 AI 模型设置失败")
		}
		if providerID != nil {
			providerOK, err := s.repo.EnabledProviderExists(txCtx, *providerID)
			if err != nil {
				return internalError(err, "校验模型接口预设失败")
			}
			if !providerOK {
				return apperr.Validation("模型接口预设不存在或未启用").
					WithDetails(map[string]string{"provider_id": "模型接口预设不存在或未启用"})
			}
			current.ProviderID = providerID
		}
		if in.ModelOverride != nil {
			current.ModelOverride = strings.TrimSpace(*in.ModelOverride)
		}
		if !paramsProvided {
			params, err = domain.GenerationParamsFromJSON(current.RawParams)
			if err != nil {
				return internalError(err, "解析既有生成参数失败")
			}
		}
		paramsJSON, err := domain.GenerationParamsJSON(params)
		if err != nil {
			return internalError(err, "序列化生成参数失败")
		}
		timeout := current.TimeoutSeconds
		if in.RequestTimeoutSeconds != nil {
			timeout = *in.RequestTimeoutSeconds
		}
		if timeout <= 0 {
			timeout = 60
		}
		current.TimeoutSeconds = timeout
		if err := s.repo.UpsertModelSettings(txCtx, aiID, current, paramsJSON, at); err != nil {
			return internalError(err, "更新 AI 模型设置失败")
		}
		providerDetail := ""
		if current.ProviderID != nil {
			providerDetail = formatID(*current.ProviderID)
		}
		if err := s.writeAudit(txCtx, adminID, contracts.AuditActionAIModelUpdate, targetAI, &aiID,
			map[string]any{"provider_id": providerDetail}, at); err != nil {
			return err
		}
		updated, err := s.repo.GetModelSettings(txCtx, aiID)
		if err != nil {
			return internalError(err, "读取更新后的 AI 模型设置失败")
		}
		response = ModelResponse{Settings: updated}
		return nil
	})
	if err != nil {
		return ModelResponse{}, err
	}
	return response, nil
}

// UpdateAIBehavior 更新行为开关、候选范围、AI 间互动、频率与冷却。
func (s *Service) UpdateAIBehavior(
	ctx context.Context,
	adminID, aiID int64,
	patch domain.BehaviorPatch,
) (domain.BehaviorSettings, time.Time, error) {
	at := s.now()
	var settings domain.BehaviorSettings
	err := s.withTx(ctx, func(txCtx context.Context) error {
		if err := s.ensureAIUser(txCtx, aiID); err != nil {
			return err
		}
		current, _, err := s.repo.GetBehavior(txCtx, aiID)
		if errors.Is(err, repo.ErrNotFound) {
			current = domain.DefaultBehaviorSettings()
		} else if err != nil {
			return internalError(err, "读取 AI 行为设置失败")
		}
		final, details := domain.PatchAndValidateBehavior(current, patch)
		if len(details) > 0 {
			return validationError(details)
		}
		if err := s.repo.UpsertBehavior(txCtx, aiID, final, at); err != nil {
			return internalError(err, "更新 AI 行为设置失败")
		}
		if err := s.writeAudit(txCtx, adminID, contracts.AuditActionAIBehaviorUpdate, targetAI, &aiID,
			map[string]any{"candidate_scope_type": final.CandidateScopeType,
				"allow_web_search": final.AllowWebSearch, "allow_group_message": final.AllowGroupMessage}, at); err != nil {
			return err
		}
		settings = final
		return nil
	})
	if err != nil {
		return domain.BehaviorSettings{}, time.Time{}, err
	}
	return settings, at, nil
}

// SetAIEnabled 启用/停用单个 AI；停用时暂停该 AI 的 pending 后台作业并取消待执行任务。
func (s *Service) SetAIEnabled(ctx context.Context, adminID, aiID int64, enabled bool) error {
	at := s.now()
	return s.withTx(ctx, func(txCtx context.Context) error {
		if err := s.ensureAIUser(txCtx, aiID); err != nil {
			return err
		}
		if err := s.repo.SetProfileEnabled(txCtx, aiID, enabled, at); err != nil {
			return internalError(err, "更新 AI 启用状态失败")
		}
		// 停用 AI 时先取消待执行任务（ai_task 作业的 ref_type='ai_task'、ref_id 是任务 ID，
		// 无法按 AI ID 批量暂停，取消任务后作业执行时不会再领取任务），再暂停该 AI 的
		// 记忆沉淀作业（ref_type='ai'、ref_id 是 AI ID，按正确引用暂停）。
		if !enabled && s.deps.Agent != nil {
			if _, err := s.deps.Agent.CancelPendingTasksForActor(txCtx, contracts.CancelTasksInput{
				ActorID: adminID,
				AIID:    aiID,
				At:      at,
			}); err != nil {
				return internalError(err, "取消 AI 待执行任务失败")
			}
		}
		if !enabled && s.deps.Jobs != nil {
			// 该 AI 的调度（ai_schedule）与记忆沉淀作业 ref_type='ai'、ref_id=AI ID；
			// JobType 为空表示暂停该 AI 名下所有 pending 作业（等价旧实现）。
			if _, err := s.deps.Jobs.PausePending(txCtx, contracts.PausePendingJobsInput{
				RefType: "ai",
				RefIDs:  []int64{aiID},
				At:      at,
			}); err != nil {
				return internalError(err, "暂停 AI 待执行作业失败")
			}
		}
		action := contracts.AuditActionAIEnable
		if !enabled {
			action = contracts.AuditActionAIDisable
		}
		return s.writeAudit(txCtx, adminID, action, targetAI, &aiID, map[string]any{}, at)
	})
}

// loadAIDetail 组装 AI 详情：账号资料、身份资料、模型设置、行为设置、近期任务与记忆条数。
func (s *Service) loadAIDetail(ctx context.Context, aiID int64) (AIDetail, error) {
	user, err := s.deps.Users.GetUser(ctx, aiID)
	if err != nil {
		if apperr.IsKind(err, apperr.KindNotFound) {
			return AIDetail{}, apperr.NotFound("AI 用户不存在")
		}
		return AIDetail{}, internalError(err, "读取 AI 用户失败")
	}
	if !user.IsAI {
		return AIDetail{}, apperr.NotFound("AI 用户不存在")
	}
	detail := AIDetail{
		User:      model.UserBriefFromUser(user),
		Bio:       user.Bio,
		CreatedAt: user.CreatedAt,
		UpdatedAt: user.UpdatedAt,
	}
	profile, err := s.repo.GetProfile(ctx, aiID)
	if errors.Is(err, repo.ErrNotFound) {
		profile = domain.Profile{UserID: aiID, TargetLanguage: "zh-CN"}
	} else if err != nil {
		return AIDetail{}, internalError(err, "读取 AI 身份资料失败")
	}
	detail.Profile = profile

	modelSettings, err := s.repo.GetModelSettings(ctx, aiID)
	if errors.Is(err, repo.ErrNotFound) {
		modelSettings = domain.ModelSettings{RawParams: json.RawMessage(`{}`), TimeoutSeconds: 60}
	} else if err != nil {
		return AIDetail{}, internalError(err, "读取 AI 模型设置失败")
	}
	detail.Model = modelSettings

	behavior, behaviorAt, err := s.repo.GetBehavior(ctx, aiID)
	if errors.Is(err, repo.ErrNotFound) {
		behavior = domain.DefaultBehaviorSettings()
	} else if err != nil {
		return AIDetail{}, internalError(err, "读取 AI 行为设置失败")
	}
	detail.Behavior = behavior
	detail.BehaviorAt = behaviorAt

	if s.deps.Tasks != nil {
		tasks, err := s.deps.Tasks.RecentTasks(ctx, aiID, 5)
		if err != nil {
			s.deps.Log.Warn("读取 AI 近期任务失败", "ai_id", aiID, "err", err)
		} else {
			detail.RecentTasks = tasks
		}
	}

	recent, longTerm, err := s.repo.CountMemoriesByType(ctx, aiID)
	if err != nil {
		return AIDetail{}, internalError(err, "统计 AI 记忆条数失败")
	}
	detail.MemoryCounts = MemoryCounts{Recent: recent, LongTerm: longTerm}
	return detail, nil
}

// parseProviderIDValue 解析请求中的 provider_id（雪花 ID 字符串）。
func parseProviderIDValue(raw string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("请选择有效的模型接口预设")
	}
	return id, nil
}

// mergeProfilePatch 把资料补丁合并进 profile（nil 字段不变）。
func mergeProfilePatch(profile *domain.Profile, in domain.UpdateInput) {
	if in.Background != nil {
		profile.Background = *in.Background
	}
	if in.Personality != nil {
		profile.Personality = *in.Personality
	}
	if in.CoreValues != nil {
		profile.CoreValues = *in.CoreValues
	}
	if in.LanguageStyle != nil {
		profile.LanguageStyle = *in.LanguageStyle
	}
	if in.Topics != nil {
		profile.Topics = *in.Topics
	}
	if in.Taboos != nil {
		profile.Taboos = *in.Taboos
	}
	if in.SystemPrompt != nil {
		profile.SystemPrompt = *in.SystemPrompt
	}
	if in.TargetLanguage != nil {
		profile.TargetLanguage = strings.TrimSpace(*in.TargetLanguage)
	}
}
