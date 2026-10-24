package repo

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/great-magician-01/timeline/internal/modules/ai/domain"
)

// profileColumns 是 ai_profiles 的行列清单。
const profileColumns = `background, personality, core_values, language_style, topics,
	target_language, taboos, system_prompt, is_enabled, created_at, updated_at`

// GetProfile 读取 AI 身份资料；不存在时返回 pgx.ErrNoRows。
func (s *Store) GetProfile(ctx context.Context, aiID int64) (domain.Profile, error) {
	profile := domain.Profile{UserID: aiID, TargetLanguage: "zh-CN"}
	err := s.exec(ctx).QueryRow(ctx,
		`SELECT `+profileColumns+` FROM ai_profiles WHERE user_id = $1`, aiID,
	).Scan(&profile.Background, &profile.Personality, &profile.CoreValues, &profile.LanguageStyle,
		&profile.Topics, &profile.TargetLanguage, &profile.Taboos, &profile.SystemPrompt,
		&profile.IsEnabled, &profile.CreatedAt, &profile.UpdatedAt)
	return profile, normalizeNotFound(err)
}

// UpsertProfile 创建或更新 AI 身份资料（updated_at 使用应用时间）。
func (s *Store) UpsertProfile(ctx context.Context, profile domain.Profile, at time.Time) error {
	_, err := s.exec(ctx).Exec(ctx, `
		INSERT INTO ai_profiles (
			user_id, background, personality, core_values, language_style, topics,
			target_language, taboos, system_prompt, is_enabled, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $11)
		ON CONFLICT (user_id) DO UPDATE SET
			background = EXCLUDED.background,
			personality = EXCLUDED.personality,
			core_values = EXCLUDED.core_values,
			language_style = EXCLUDED.language_style,
			topics = EXCLUDED.topics,
			target_language = EXCLUDED.target_language,
			taboos = EXCLUDED.taboos,
			system_prompt = EXCLUDED.system_prompt,
			updated_at = EXCLUDED.updated_at`,
		profile.UserID, profile.Background, profile.Personality, profile.CoreValues,
		profile.LanguageStyle, profile.Topics, profile.TargetLanguage, profile.Taboos,
		profile.SystemPrompt, profile.IsEnabled, at)
	return err
}

// SetProfileEnabled 更新 AI 启用状态（updated_at 使用应用时间）。
func (s *Store) SetProfileEnabled(ctx context.Context, aiID int64, enabled bool, at time.Time) error {
	_, err := s.exec(ctx).Exec(ctx, `
		INSERT INTO ai_profiles (user_id, is_enabled, created_at, updated_at)
		VALUES ($1, $2, $3, $3)
		ON CONFLICT (user_id) DO UPDATE SET is_enabled = EXCLUDED.is_enabled, updated_at = EXCLUDED.updated_at`,
		aiID, enabled, at)
	return err
}

// GetModelSettings 读取 AI 模型设置（联表读预设名、实际模型与预设运行时字段）；
// 不存在返回 pgx.ErrNoRows。预设逻辑删除时不满足 JOIN 条件，ProviderAvailable=false，
// 与单独调用 GetProvider 的不可用语义一致，运行时装配无需再读一次预设行。
func (s *Store) GetModelSettings(ctx context.Context, aiID int64) (domain.ModelSettings, error) {
	settings := domain.ModelSettings{RawParams: json.RawMessage(`{}`), TimeoutSeconds: 60}
	var rawParams []byte
	err := s.exec(ctx).QueryRow(ctx, `
		SELECT ms.provider_id, COALESCE(pr.name, ''), ms.model_override, ms.generation_params,
		       ms.request_timeout_seconds, ms.updated_at,
		       COALESCE(NULLIF(ms.model_override, ''), COALESCE(pr.default_model, '')),
		       (pr.id IS NOT NULL),
		       COALESCE(pr.base_url, ''), COALESCE(pr.api_key_cipher, ''),
		       COALESCE(pr.default_model, ''), COALESCE(pr.is_enabled, FALSE)
		FROM ai_model_settings ms
		LEFT JOIN model_providers pr ON pr.id = ms.provider_id AND NOT pr.is_deleted
		WHERE ms.ai_id = $1`, aiID,
	).Scan(&settings.ProviderID, &settings.ProviderName, &settings.ModelOverride, &rawParams,
		&settings.TimeoutSeconds, &settings.UpdatedAt, &settings.Model,
		&settings.ProviderAvailable,
		&settings.ProviderBaseURL, &settings.ProviderAPIKeyCipher,
		&settings.ProviderDefaultModel, &settings.ProviderIsEnabled)
	if err != nil {
		return domain.ModelSettings{}, normalizeNotFound(err)
	}
	settings.RawParams = domain.JSONOrEmpty(rawParams)
	return settings, nil
}

// GetProfileEnabled 轻量读取 AI 启用状态；资料行不存在视为未启用（不返回 ErrNoRows）。
// 供 agent 在模型返回后复核配置（避免重新装配完整运行时快照）。
func (s *Store) GetProfileEnabled(ctx context.Context, aiID int64) (bool, error) {
	var enabled bool
	err := s.exec(ctx).QueryRow(ctx, `
		SELECT is_enabled FROM ai_profiles WHERE user_id = $1`, aiID).Scan(&enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return enabled, err
}

// UpsertModelSettings 创建或更新 AI 模型设置。
func (s *Store) UpsertModelSettings(
	ctx context.Context,
	aiID int64,
	settings domain.ModelSettings,
	paramsJSON json.RawMessage,
	at time.Time,
) error {
	_, err := s.exec(ctx).Exec(ctx, `
		INSERT INTO ai_model_settings (
			ai_id, provider_id, model_override, generation_params, request_timeout_seconds, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (ai_id) DO UPDATE SET
			provider_id = EXCLUDED.provider_id,
			model_override = EXCLUDED.model_override,
			generation_params = EXCLUDED.generation_params,
			request_timeout_seconds = EXCLUDED.request_timeout_seconds,
			updated_at = EXCLUDED.updated_at`,
		aiID, settings.ProviderID, settings.ModelOverride, paramsJSON, settings.TimeoutSeconds, at)
	return err
}

// GetBehavior 读取 AI 行为设置；不存在时返回 pgx.ErrNoRows。
func (s *Store) GetBehavior(ctx context.Context, aiID int64) (domain.BehaviorSettings, time.Time, error) {
	var settings domain.BehaviorSettings
	var scopeParams, rateLimits, schedule []byte
	var updatedAt time.Time
	err := s.exec(ctx).QueryRow(ctx, `
		SELECT allow_original_post, allow_active_comment, allow_passive_reply, allow_like,
		       allow_follow, allow_unfollow, allow_repost, allow_quote,
		       candidate_scope_type, candidate_scope_params, allow_ai_to_ai, rate_limits,
		       cooldown_seconds, active_hours_start, active_hours_end, schedule,
		       max_reply_depth, allow_web_search, allow_group_message, updated_at
		FROM ai_behavior_settings WHERE ai_id = $1`, aiID,
	).Scan(&settings.AllowOriginalPost, &settings.AllowActiveComment, &settings.AllowPassiveReply,
		&settings.AllowLike, &settings.AllowFollow, &settings.AllowUnfollow, &settings.AllowRepost,
		&settings.AllowQuote, &settings.CandidateScopeType, &scopeParams, &settings.AllowAIToAI,
		&rateLimits, &settings.CooldownSeconds, &settings.ActiveHoursStart, &settings.ActiveHoursEnd,
		&schedule, &settings.MaxReplyDepth, &settings.AllowWebSearch, &settings.AllowGroupMessage, &updatedAt)
	if err != nil {
		return domain.BehaviorSettings{}, time.Time{}, normalizeNotFound(err)
	}
	settings.CandidateScopeParams = domain.JSONOrEmpty(scopeParams)
	settings.RateLimits = domain.JSONOrEmpty(rateLimits)
	settings.Schedule = domain.JSONOrEmpty(schedule)
	return settings, updatedAt, nil
}

// UpsertBehavior 创建或更新 AI 行为设置。
func (s *Store) UpsertBehavior(ctx context.Context, aiID int64, settings domain.BehaviorSettings, at time.Time) error {
	_, err := s.exec(ctx).Exec(ctx, `
		INSERT INTO ai_behavior_settings (
			ai_id, allow_original_post, allow_active_comment, allow_passive_reply, allow_like,
			allow_follow, allow_unfollow, allow_repost, allow_quote, candidate_scope_type,
			candidate_scope_params, allow_ai_to_ai, rate_limits, cooldown_seconds,
			active_hours_start, active_hours_end, schedule, max_reply_depth, allow_web_search,
			allow_group_message, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)
		ON CONFLICT (ai_id) DO UPDATE SET
			allow_original_post = EXCLUDED.allow_original_post,
			allow_active_comment = EXCLUDED.allow_active_comment,
			allow_passive_reply = EXCLUDED.allow_passive_reply,
			allow_like = EXCLUDED.allow_like,
			allow_follow = EXCLUDED.allow_follow,
			allow_unfollow = EXCLUDED.allow_unfollow,
			allow_repost = EXCLUDED.allow_repost,
			allow_quote = EXCLUDED.allow_quote,
			candidate_scope_type = EXCLUDED.candidate_scope_type,
			candidate_scope_params = EXCLUDED.candidate_scope_params,
			allow_ai_to_ai = EXCLUDED.allow_ai_to_ai,
			rate_limits = EXCLUDED.rate_limits,
			cooldown_seconds = EXCLUDED.cooldown_seconds,
			active_hours_start = EXCLUDED.active_hours_start,
			active_hours_end = EXCLUDED.active_hours_end,
			schedule = EXCLUDED.schedule,
			max_reply_depth = EXCLUDED.max_reply_depth,
			allow_web_search = EXCLUDED.allow_web_search,
			allow_group_message = EXCLUDED.allow_group_message,
			updated_at = EXCLUDED.updated_at`,
		aiID, settings.AllowOriginalPost, settings.AllowActiveComment, settings.AllowPassiveReply,
		settings.AllowLike, settings.AllowFollow, settings.AllowUnfollow, settings.AllowRepost,
		settings.AllowQuote, settings.CandidateScopeType, domain.JSONOrEmpty(settings.CandidateScopeParams),
		settings.AllowAIToAI, domain.JSONOrEmpty(settings.RateLimits), settings.CooldownSeconds,
		settings.ActiveHoursStart, settings.ActiveHoursEnd, domain.JSONOrEmpty(settings.Schedule),
		settings.MaxReplyDepth, settings.AllowWebSearch, settings.AllowGroupMessage, at)
	return err
}

// ListAIStates 批量读取 AI 的启用状态、创建时间与当前模型配置。
func (s *Store) ListAIStates(ctx context.Context, aiIDs []int64) (map[int64]AIState, error) {
	states := make(map[int64]AIState, len(aiIDs))
	if len(aiIDs) == 0 {
		return states, nil
	}
	rows, err := s.exec(ctx).Query(ctx, `
		SELECT u.id, COALESCE(p.is_enabled, FALSE), p.created_at,
		       ms.provider_id, COALESCE(pr.name, ''),
		       COALESCE(NULLIF(ms.model_override, ''), COALESCE(pr.default_model, ''))
		FROM ai_profiles p
		RIGHT JOIN unnest($1::bigint[]) AS u(id) ON u.id = p.user_id
		LEFT JOIN ai_model_settings ms ON ms.ai_id = u.id
		LEFT JOIN model_providers pr ON pr.id = ms.provider_id
		WHERE TRUE`, aiIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var state AIState
		if err := rows.Scan(&state.AIID, &state.IsEnabled, &state.CreatedAt,
			&state.ProviderID, &state.ProviderName, &state.Model); err != nil {
			return nil, err
		}
		states[state.AIID] = state
	}
	return states, rows.Err()
}

// EnabledAIIDs 返回给定候选 AI 中启用的 ID（调度器使用）。
func (s *Store) EnabledAIIDs(ctx context.Context, aiIDs []int64) ([]int64, error) {
	if len(aiIDs) == 0 {
		return nil, nil
	}
	rows, err := s.exec(ctx).Query(ctx, `
		SELECT user_id FROM ai_profiles
		WHERE user_id = ANY($1) AND is_enabled
		ORDER BY user_id DESC`, aiIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]int64, 0, len(aiIDs))
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
