package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/great-magician-01/timeline/internal/modules/ai/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
)

// 说明：ai.go 的写用例校验发生在事务之前，可完整断言字段级错误；
// 合法输入因未接线事务管理器在进入事务前返回「事务管理器未初始化」，不会触库。

// validCreateAIInput 返回一份字段全部合法的创建输入，供各用例按需改写单个字段。
func validCreateAIInput() CreateAIInput {
	return CreateAIInput{
		Identity: domain.CreateInput{
			Username:       "helper_ai",
			Nickname:       "小助手",
			Bio:            "测试 AI",
			TargetLanguage: "zh-CN",
		},
		ProviderID:       "123",
		ModelOverride:    "gpt-demo-mini",
		GenerationParams: json.RawMessage(`{"max_tokens":128,"temperature":0.7}`),
	}
}

// TestMergeProfilePatch 校验资料补丁合并：nil 字段不变、目标语言去空白。
func TestMergeProfilePatch(t *testing.T) {
	profile := domain.Profile{
		UserID:         7,
		Background:     "旧背景",
		Personality:    "旧性格",
		CoreValues:     "旧价值观",
		LanguageStyle:  "旧语言",
		Topics:         "旧话题",
		Taboos:         "旧禁忌",
		SystemPrompt:   "旧提示词",
		TargetLanguage: "旧语言标签",
		IsEnabled:      true,
	}
	patch := domain.UpdateInput{
		Background:     strPtr("新背景"),
		Personality:    strPtr("新性格"),
		CoreValues:     strPtr("新价值观"),
		LanguageStyle:  strPtr("新语言"),
		Topics:         strPtr("新话题"),
		Taboos:         strPtr("新禁忌"),
		SystemPrompt:   strPtr("新提示词"),
		TargetLanguage: strPtr("  en-US  "),
	}
	mergeProfilePatch(&profile, patch)
	want := domain.Profile{
		UserID:         7,
		Background:     "新背景",
		Personality:    "新性格",
		CoreValues:     "新价值观",
		LanguageStyle:  "新语言",
		Topics:         "新话题",
		Taboos:         "新禁忌",
		SystemPrompt:   "新提示词",
		TargetLanguage: "en-US",
		IsEnabled:      true,
	}
	if profile != want {
		t.Fatalf("补丁合并结果错误:\n got=%+v\nwant=%+v", profile, want)
	}

	// nil 补丁不改变任何字段。
	merged := profile
	mergeProfilePatch(&merged, domain.UpdateInput{})
	if merged != profile {
		t.Fatalf("空补丁不应改变资料: %+v", merged)
	}
}

// TestCreateAIValidation 校验创建 AI 的字段校验集合（用户名/预设/生成参数/行为/模型覆盖）。
func TestCreateAIValidation(t *testing.T) {
	tagsBehavior := domain.BehaviorPatch{CandidateScopeType: strPtr("tags")}
	cases := []struct {
		name   string
		mutate func(*CreateAIInput)
		field  string
	}{
		{
			name:   "用户名非法",
			mutate: func(in *CreateAIInput) { in.Identity.Username = "1bad" },
			field:  "username",
		},
		{
			name:   "简介超长",
			mutate: func(in *CreateAIInput) { in.Identity.Bio = strings.Repeat("简", domain.MaxBioRunes+1) },
			field:  "bio",
		},
		{
			name:   "提示词超长",
			mutate: func(in *CreateAIInput) { in.Identity.SystemPrompt = strings.Repeat("x", domain.MaxSystemPromptRunes+1) },
			field:  "system_prompt",
		},
		{
			name:   "预设 ID 缺失",
			mutate: func(in *CreateAIInput) { in.ProviderID = "" },
			field:  "provider_id",
		},
		{
			name:   "预设 ID 非法",
			mutate: func(in *CreateAIInput) { in.ProviderID = "abc" },
			field:  "provider_id",
		},
		{
			name:   "生成参数含未知键",
			mutate: func(in *CreateAIInput) { in.GenerationParams = json.RawMessage(`{"unknown":1}`) },
			field:  "generation_params",
		},
		{
			name:   "生成参数越界",
			mutate: func(in *CreateAIInput) { in.GenerationParams = json.RawMessage(`{"temperature":3}`) },
			field:  "generation_params",
		},
		{
			name:   "候选范围缺少参数",
			mutate: func(in *CreateAIInput) { in.Behavior = tagsBehavior },
			field:  "candidate_scope_params",
		},
		{
			name:   "冷却时间为负",
			mutate: func(in *CreateAIInput) { in.Behavior = domain.BehaviorPatch{CooldownSeconds: intPtr(-1)} },
			field:  "cooldown_seconds",
		},
		{
			name: "模型覆盖名超长",
			mutate: func(in *CreateAIInput) {
				in.ModelOverride = strings.Repeat("m", domain.MaxModelOverrideRunes+1)
			},
			field: "model_override",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := validCreateAIInput()
			tc.mutate(&in)
			svc := testService(Deps{})
			_, err := svc.CreateAI(context.Background(), 1, in)
			details := validationDetails(t, err)
			if _, ok := details[tc.field]; !ok {
				t.Fatalf("应返回 %s 字段错误，实际 %v", tc.field, details)
			}
		})
	}
}

// TestCreateAIValidStopsBeforeRepo 校验合法输入行进到事务入口（不触库）。
func TestCreateAIValidStopsBeforeRepo(t *testing.T) {
	svc := testService(Deps{})
	_, err := svc.CreateAI(context.Background(), 1, validCreateAIInput())
	requireTransactionMissing(t, err)
}

// TestUpdateAIValidation 校验资料更新补丁校验。
func TestUpdateAIValidation(t *testing.T) {
	cases := []struct {
		name     string
		identity domain.UpdateInput
		field    string
	}{
		{
			name:     "用户名非法",
			identity: domain.UpdateInput{Username: strPtr("_bad")},
			field:    "username",
		},
		{
			name:     "昵称为空",
			identity: domain.UpdateInput{Nickname: strPtr(" ")},
			field:    "nickname",
		},
		{
			name:     "昵称超长",
			identity: domain.UpdateInput{Nickname: strPtr(strings.Repeat("昵", domain.MaxNicknameRunes+1))},
			field:    "nickname",
		},
		{
			name:     "简介超长",
			identity: domain.UpdateInput{Bio: strPtr(strings.Repeat("简", domain.MaxBioRunes+1))},
			field:    "bio",
		},
		{
			name:     "身份字段超长",
			identity: domain.UpdateInput{Background: strPtr(strings.Repeat("背", domain.MaxIdentityFieldRunes+1))},
			field:    "background",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := testService(Deps{})
			_, err := svc.UpdateAI(context.Background(), 1, 7, UpdateAIInput{Identity: tc.identity})
			details := validationDetails(t, err)
			if _, ok := details[tc.field]; !ok {
				t.Fatalf("应返回 %s 字段错误，实际 %v", tc.field, details)
			}
		})
	}
}

// TestUpdateAIValidStopsBeforeRepo 校验合法更新补丁行进到事务入口（不触库）。
func TestUpdateAIValidStopsBeforeRepo(t *testing.T) {
	svc := testService(Deps{})
	_, err := svc.UpdateAI(context.Background(), 1, 7, UpdateAIInput{Identity: domain.UpdateInput{
		Nickname: strPtr("新昵称"),
	}})
	requireTransactionMissing(t, err)
}

// TestUpdateAIModelValidation 校验模型设置补丁的字段校验。
func TestUpdateAIModelValidation(t *testing.T) {
	tooLongModel := strings.Repeat("m", domain.MaxModelOverrideRunes+1)
	cases := []struct {
		name   string
		input  UpdateAIModelInput
		fields []string
	}{
		{
			name:   "超时过小",
			input:  UpdateAIModelInput{RequestTimeoutSeconds: intPtr(4)},
			fields: []string{"request_timeout_seconds"},
		},
		{
			name:   "超时过大",
			input:  UpdateAIModelInput{RequestTimeoutSeconds: intPtr(601)},
			fields: []string{"request_timeout_seconds"},
		},
		{
			name:   "预设 ID 非法",
			input:  UpdateAIModelInput{ProviderID: strPtr("bad")},
			fields: []string{"provider_id"},
		},
		{
			name:   "生成参数非法",
			input:  UpdateAIModelInput{GenerationParams: json.RawMessage(`{"max_tokens":0}`)},
			fields: []string{"generation_params"},
		},
		{
			name:   "模型覆盖名超长",
			input:  UpdateAIModelInput{ModelOverride: strPtr(tooLongModel)},
			fields: []string{"model_override"},
		},
		{
			name: "多个字段同时非法",
			input: UpdateAIModelInput{
				RequestTimeoutSeconds: intPtr(1),
				ProviderID:            strPtr("0"),
				ModelOverride:         strPtr(tooLongModel),
			},
			fields: []string{"request_timeout_seconds", "provider_id", "model_override"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := testService(Deps{})
			_, err := svc.UpdateAIModel(context.Background(), 1, 7, tc.input)
			details := validationDetails(t, err)
			for _, field := range tc.fields {
				if _, ok := details[field]; !ok {
					t.Fatalf("应返回 %s 字段错误，实际 %v", field, details)
				}
			}
		})
	}
}

// TestUpdateAIModelValidStopsBeforeRepo 校验合法模型补丁行进到事务入口（不触库）。
func TestUpdateAIModelValidStopsBeforeRepo(t *testing.T) {
	svc := testService(Deps{})
	_, err := svc.UpdateAIModel(context.Background(), 1, 7, UpdateAIModelInput{
		RequestTimeoutSeconds: intPtr(60),
		ModelOverride:         strPtr("gpt-demo"),
	})
	requireTransactionMissing(t, err)
}

// TestUpdateAIBehaviorStopsBeforeRepo 校验行为策略补丁在事务入口前不触库。
func TestUpdateAIBehaviorStopsBeforeRepo(t *testing.T) {
	svc := testService(Deps{})
	_, _, err := svc.UpdateAIBehavior(context.Background(), 1, 7, domain.BehaviorPatch{
		AllowLike: boolPtr(true),
	})
	requireTransactionMissing(t, err)
}

// TestSetAIEnabledStopsBeforeRepo 校验启用/停用在事务入口前不触库。
func TestSetAIEnabledStopsBeforeRepo(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		svc := testService(Deps{})
		err := svc.SetAIEnabled(context.Background(), 1, 7, enabled)
		requireTransactionMissing(t, err)
	}
}

// TestListMemoriesValidation 校验记忆列表的 type/status 必填与取值校验（AI 校验通过后即返回）。
func TestListMemoriesValidation(t *testing.T) {
	cases := []struct {
		name   string
		filter MemoryFilter
		field  string
	}{
		{
			name:   "type 缺失",
			filter: MemoryFilter{Status: "active"},
			field:  "type",
		},
		{
			name:   "type 非法",
			filter: MemoryFilter{MemoryType: "bad"},
			field:  "type",
		},
		{
			name:   "status 非法",
			filter: MemoryFilter{MemoryType: domain.MemoryTypeRecent, Status: "bad"},
			field:  "status",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := testService(Deps{Users: fakeUsers{user: aiUser(7)}})
			_, err := svc.ListMemories(context.Background(), 7, tc.filter)
			e := requireKind(t, err, apperr.KindValidation)
			if e.Details == nil {
				t.Fatalf("校验错误应携带字段详情: %v", err)
			}
			details, ok := e.Details.(map[string]string)
			if !ok || details[tc.field] == "" {
				t.Fatalf("应返回 %s 字段详情，实际 %#v", tc.field, e.Details)
			}
		})
	}
}

// TestListMemoriesRejectsNonAI 校验记忆列表先做 AI 身份校验。
func TestListMemoriesRejectsNonAI(t *testing.T) {
	svc := testService(Deps{Users: fakeUsers{user: humanUser(7)}})
	_, err := svc.ListMemories(context.Background(), 7, MemoryFilter{MemoryType: domain.MemoryTypeRecent})
	requireKind(t, err, apperr.KindNotFound)
}
