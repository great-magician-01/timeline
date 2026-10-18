package http

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/ai/app"
	"github.com/great-magician-01/timeline/internal/modules/ai/domain"
	"github.com/great-magician-01/timeline/internal/platform/clock"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// 说明：view.go 是 ai 模块 HTTP DTO 的纯映射层；本文件锁定字段映射、时间格式、
// 可空字段与「空 JSONB 输出 {}」等对外契约，不依赖服务、数据库或网络。

// testViewTime 是 DTO 测试统一使用的时间。
var testViewTime = time.Date(2026, 10, 18, 12, 0, 0, 0, clock.AsiaShanghai)

// TestProviderListView 校验预设列表项映射（ID/时间字符串化、密钥掩码不泄露）。
func TestProviderListView(t *testing.T) {
	got := providerListView(app.ProviderItem{
		Provider: domain.Provider{
			ID:           7,
			Name:         "预设",
			BaseURL:      "https://api.example.com/v1",
			DefaultModel: "gpt-demo",
			IsEnabled:    true,
			CreatedAt:    testViewTime,
			UpdatedAt:    testViewTime.Add(time.Hour),
		},
		APIKeyMasked: "sk-...abcd",
		AICount:      2,
	})
	if got.ID != "7" {
		t.Errorf("ID 必须字符串化: %q", got.ID)
	}
	if got.Name != "预设" || got.BaseURL != "https://api.example.com/v1" ||
		got.DefaultModel != "gpt-demo" || !got.IsEnabled || got.APIKeyMasked != "sk-...abcd" ||
		got.AICount != 2 {
		t.Errorf("预设列表项字段错误: %+v", got)
	}
	if got.CreatedAt != "2026-10-18T12:00:00+08:00" || got.UpdatedAt != "2026-10-18T13:00:00+08:00" {
		t.Errorf("时间应输出北京时间 RFC3339: %q / %q", got.CreatedAt, got.UpdatedAt)
	}
	if strings.Contains(got.APIKeyMasked, "secret") {
		t.Errorf("密钥掩码不得包含明文片段: %q", got.APIKeyMasked)
	}
}

// TestModelView 校验模型设置映射：可空预设、超时兜底、空 JSONB 输出 {}。
func TestModelView(t *testing.T) {
	providerID := int64(7)
	got := modelView(domain.ModelSettings{
		ProviderID:     &providerID,
		ProviderName:   "预设",
		ModelOverride:  "gpt-demo-mini",
		RawParams:      json.RawMessage(`{"max_tokens":128}`),
		TimeoutSeconds: 0,
		Model:          "gpt-demo-mini",
		UpdatedAt:      testViewTime,
	})
	if got.ProviderID == nil || *got.ProviderID != "7" {
		t.Fatalf("provider_id 必须字符串化: %#v", got.ProviderID)
	}
	if got.ProviderName != "预设" || got.ModelOverride != "gpt-demo-mini" ||
		got.Model != "gpt-demo-mini" {
		t.Fatalf("模型字段映射错误: %+v", got)
	}
	if string(got.GenerationParams) != `{"max_tokens":128}` {
		t.Fatalf("生成参数应透传: %s", got.GenerationParams)
	}
	if got.RequestTimeoutSeconds != 60 {
		t.Fatalf("超时 <=0 时应兜底 60，实际 %d", got.RequestTimeoutSeconds)
	}
	if got.UpdatedAt != "2026-10-18T12:00:00+08:00" {
		t.Fatalf("更新时间格式错误: %q", got.UpdatedAt)
	}

	// 未绑定预设、未配置参数、未写入时间。
	empty := modelView(domain.ModelSettings{TimeoutSeconds: 30, RawParams: nil})
	if empty.ProviderID != nil {
		t.Fatalf("未绑定预设时 provider_id 必须为 nil: %#v", empty.ProviderID)
	}
	if string(empty.GenerationParams) != "{}" {
		t.Fatalf("空生成参数应输出 {}: %s", empty.GenerationParams)
	}
	if empty.RequestTimeoutSeconds != 30 {
		t.Fatalf("正超时应保留，实际 %d", empty.RequestTimeoutSeconds)
	}
	if empty.UpdatedAt != "" {
		t.Fatalf("零值时间应输出空串，实际 %q", empty.UpdatedAt)
	}

	negative := modelView(domain.ModelSettings{TimeoutSeconds: -5})
	if negative.RequestTimeoutSeconds != 60 {
		t.Fatalf("负超时应兜底 60，实际 %d", negative.RequestTimeoutSeconds)
	}
}

// TestBehaviorView 校验行为策略映射（开关、JSONB 兜底、更新时间）。
func TestBehaviorView(t *testing.T) {
	settings := domain.DefaultBehaviorSettings()
	settings.AllowOriginalPost = true
	settings.AllowWebSearch = true
	settings.AllowAIToAI = true
	settings.CandidateScopeType = "tags"
	settings.CandidateScopeParams = json.RawMessage(`{"tags":["go"]}`)
	settings.RateLimits = json.RawMessage(`{"daily_like":10}`)
	settings.Schedule = json.RawMessage(`{"cron":"0 * * * *"}`)
	got := behaviorView(settings, testViewTime)
	if !got.AllowOriginalPost || !got.AllowWebSearch || !got.AllowAIToAI || !got.AllowPassiveReply {
		t.Fatalf("行为开关映射错误: %+v", got)
	}
	if got.CandidateScopeType != "tags" ||
		string(got.CandidateScopeParams) != `{"tags":["go"]}` ||
		string(got.RateLimits) != `{"daily_like":10}` ||
		string(got.Schedule) != `{"cron":"0 * * * *"}` {
		t.Fatalf("JSONB 字段映射错误: %+v", got)
	}
	if got.CooldownSeconds != settings.CooldownSeconds || got.ActiveHoursStart != 8 ||
		got.ActiveHoursEnd != 23 || got.MaxReplyDepth != 2 {
		t.Fatalf("行为数值字段映射错误: %+v", got)
	}
	if got.UpdatedAt != "2026-10-18T12:00:00+08:00" {
		t.Fatalf("更新时间格式错误: %q", got.UpdatedAt)
	}

	// 零值行为设置也应输出合法 DTO：JSONB 字段为 {}、零值时间为空串。
	zero := behaviorView(domain.BehaviorSettings{}, time.Time{})
	if string(zero.CandidateScopeParams) != "{}" || string(zero.RateLimits) != "{}" ||
		string(zero.Schedule) != "{}" {
		t.Fatalf("空 JSONB 应输出 {}: %+v", zero)
	}
	if zero.UpdatedAt != "" {
		t.Fatalf("零值更新时间应输出空串，实际 %q", zero.UpdatedAt)
	}
}

// TestProfileView 校验身份资料映射。
func TestProfileView(t *testing.T) {
	profile := domain.Profile{
		UserID:         7,
		Background:     "背景",
		Personality:    "性格",
		CoreValues:     "价值观",
		LanguageStyle:  "语言风格",
		Topics:         "话题",
		TargetLanguage: "zh-CN",
		Taboos:         "禁忌",
		SystemPrompt:   "提示词",
		IsEnabled:      true,
		CreatedAt:      testViewTime,
		UpdatedAt:      testViewTime,
	}
	got := profileView(profile)
	if got.Background != "背景" || got.Personality != "性格" || got.CoreValues != "价值观" ||
		got.LanguageStyle != "语言风格" || got.Topics != "话题" ||
		got.TargetLanguage != "zh-CN" || got.Taboos != "禁忌" ||
		got.SystemPrompt != "提示词" || !got.IsEnabled {
		t.Fatalf("身份资料映射错误: %+v", got)
	}
	if got.CreatedAt != "2026-10-18T12:00:00+08:00" || got.UpdatedAt != "2026-10-18T12:00:00+08:00" {
		t.Fatalf("身份资料时间格式错误: %+v", got)
	}
	zero := profileView(domain.Profile{})
	if zero.CreatedAt != "" || zero.UpdatedAt != "" {
		t.Fatalf("零值时间应输出空串: %+v", zero)
	}
}

// TestDetailView 校验 AI 详情映射：作者摘要、近期任务、记忆条数与时间。
func TestDetailView(t *testing.T) {
	providerID := int64(7)
	detail := app.AIDetail{
		User: model.UserBrief{
			ID: 9, Username: "ai_demo", Nickname: "演示 AI", AvatarURL: "/a.png", IsAI: true,
		},
		Bio: "简介",
		Profile: domain.Profile{
			UserID: 9, Background: "背景", TargetLanguage: "zh-CN", IsEnabled: true,
			CreatedAt: testViewTime, UpdatedAt: testViewTime,
		},
		Model: domain.ModelSettings{
			ProviderID: &providerID, ProviderName: "预设", ModelOverride: "m1",
			RawParams: json.RawMessage(`{}`), TimeoutSeconds: 60, Model: "m1", UpdatedAt: testViewTime,
		},
		Behavior:   domain.DefaultBehaviorSettings(),
		BehaviorAt: testViewTime,
		RecentTasks: []app.TaskBrief{{
			ID: 33, TriggerType: "schedule", ActionType: "like", Status: "succeeded", CreatedAt: testViewTime,
		}},
		MemoryCounts: app.MemoryCounts{Recent: 3, LongTerm: 1},
		CreatedAt:    testViewTime,
		UpdatedAt:    testViewTime,
	}
	got := detailView(detail)
	if got.ID != "9" {
		t.Fatalf("详情 ID 必须字符串化: %q", got.ID)
	}
	if got.User.Username != "ai_demo" || got.User.ID != "9" || !got.User.IsAI {
		t.Fatalf("作者摘要映射错误: %+v", got.User)
	}
	if got.Bio != "简介" || !got.Profile.IsEnabled || got.Profile.Background != "背景" {
		t.Fatalf("身份资料映射错误: %+v / %+v", got.Bio, got.Profile)
	}
	if got.Model.ProviderID == nil || *got.Model.ProviderID != "7" || got.Model.Model != "m1" {
		t.Fatalf("模型映射错误: %+v", got.Model)
	}
	if len(got.RecentTasks) != 1 {
		t.Fatalf("近期任务数量错误: %d", len(got.RecentTasks))
	}
	task := got.RecentTasks[0]
	if task.ID != "33" || task.TriggerType != "schedule" || task.ActionType != "like" ||
		task.Status != "succeeded" || task.CreatedAt != "2026-10-18T12:00:00+08:00" {
		t.Fatalf("近期任务映射错误: %+v", task)
	}
	if got.MemoryCounts.Recent != 3 || got.MemoryCounts.LongTerm != 1 {
		t.Fatalf("记忆条数映射错误: %+v", got.MemoryCounts)
	}
	if got.CreatedAt != "2026-10-18T12:00:00+08:00" || got.UpdatedAt != "2026-10-18T12:00:00+08:00" {
		t.Fatalf("详情时间格式错误: %+v", got)
	}

	// 无近期任务时输出空数组而不是 null。
	empty := detailView(app.AIDetail{User: model.UserBrief{ID: 1}})
	if empty.RecentTasks == nil || len(empty.RecentTasks) != 0 {
		t.Fatalf("无近期任务应为空数组: %#v", empty.RecentTasks)
	}
}

// TestMemoryView 校验记忆 DTO：关键词空数组、可空 ID/时间。
func TestMemoryView(t *testing.T) {
	sourceRefID := int64(321)
	supersededBy := int64(456)
	lastUsed := testViewTime.Add(time.Hour)
	record := domain.MemoryRecord{
		Memory: domain.Memory{
			ID:             11,
			AID:            7,
			MemoryType:     domain.MemoryTypeLongTerm,
			Status:         domain.MemoryStatusActive,
			Content:        "长期记忆",
			Category:       domain.MemoryCategoryFact,
			Keywords:       []string{"go", "记忆"},
			Importance:     5,
			SourceType:     domain.MemorySourceConsolidation,
			SourceRefType:  domain.MemorySourceTypePost,
			SourceRefID:    &sourceRefID,
			InferenceLevel: domain.MemoryInferenceSummary,
			CreatedAt:      testViewTime,
			UpdatedAt:      testViewTime,
			LastUsedAt:     &lastUsed,
		},
		SupersededByID: &supersededBy,
	}
	got := memoryView(record)
	if got.ID != "11" || got.MemoryType != domain.MemoryTypeLongTerm ||
		got.Status != domain.MemoryStatusActive || got.Content != "长期记忆" ||
		got.Category != domain.MemoryCategoryFact || got.Importance != 5 ||
		got.SourceType != domain.MemorySourceConsolidation ||
		got.SourceRefType != domain.MemorySourceTypePost ||
		got.InferenceLevel != domain.MemoryInferenceSummary {
		t.Fatalf("记忆字段映射错误: %+v", got)
	}
	if got.SourceRefID == nil || *got.SourceRefID != "321" ||
		got.SupersededByID == nil || *got.SupersededByID != "456" ||
		got.LastUsedAt == nil || *got.LastUsedAt != "2026-10-18T13:00:00+08:00" {
		t.Fatalf("可空字段映射错误: %+v", got)
	}
	if got.CreatedAt != "2026-10-18T12:00:00+08:00" || got.UpdatedAt != "2026-10-18T12:00:00+08:00" {
		t.Fatalf("记忆时间格式错误: %+v", got)
	}

	// 关键词为 nil 时输出空数组 JSON，而不是 null。
	minimal := memoryView(domain.MemoryRecord{Memory: domain.Memory{ID: 1}})
	if minimal.Keywords == nil || len(minimal.Keywords) != 0 {
		t.Fatalf("关键词应为空数组: %#v", minimal.Keywords)
	}
	if minimal.SourceRefID != nil || minimal.SupersededByID != nil || minimal.LastUsedAt != nil {
		t.Fatalf("可空字段应为 nil: %+v", minimal)
	}
}

// TestFormatHelpers 校验时间/ID 可空格式化辅助函数。
func TestFormatHelpers(t *testing.T) {
	if got := formatTimeOrEmpty(time.Time{}); got != "" {
		t.Fatalf("零值时间应输出空串，实际 %q", got)
	}
	if got := formatTimeOrEmpty(testViewTime); got != "2026-10-18T12:00:00+08:00" {
		t.Fatalf("非零时间格式错误: %q", got)
	}

	if got := formatTimePtr(nil); got != nil {
		t.Fatalf("nil 时间应返回 nil，实际 %#v", got)
	}
	value := testViewTime
	got := formatTimePtr(&value)
	if got == nil || *got != "2026-10-18T12:00:00+08:00" {
		t.Fatalf("可空时间格式错误: %#v", got)
	}

	if got := formatIDPtr(nil); got != nil {
		t.Fatalf("nil ID 应返回 nil，实际 %#v", got)
	}
	id := int64(1234567890123456789)
	gotID := formatIDPtr(&id)
	if gotID == nil || *gotID != "1234567890123456789" {
		t.Fatalf("可空 ID 格式错误: %#v", gotID)
	}

	brief := userBriefView(model.UserBrief{
		ID: 5, Username: "alice", Nickname: "爱丽丝", AvatarURL: "/a.png", IsSuspended: true,
	})
	if brief.ID != "5" || brief.Username != "alice" || brief.Nickname != "爱丽丝" ||
		brief.AvatarURL != "/a.png" || !brief.IsSuspended {
		t.Fatalf("作者摘要视图映射错误: %+v", brief)
	}
}
