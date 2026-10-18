package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/ai/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/clock"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
)

// 说明：记忆管理写用例的字段校验在事务/触库之前完成；合法的「成功路径」需要真实 repo，
// 这里只覆盖校验、空入参短路、事务缺失时的错误传播与跨模块视图映射。

// TestCreateAIMemoryValidation 校验手工添加记忆的字段校验集合。
func TestCreateAIMemoryValidation(t *testing.T) {
	base := CreateMemoryRequest{
		MemoryType:     domain.MemoryTypeRecent,
		Content:        "用户喜欢 Go 语言",
		Category:       domain.MemoryCategoryPreference,
		Keywords:       []string{"go", "偏好"},
		Importance:     intPtr(4),
		InferenceLevel: domain.MemoryInferenceFact,
	}
	cases := []struct {
		name   string
		mutate func(*CreateMemoryRequest)
		field  string
	}{
		{
			name:   "记忆类型非法",
			mutate: func(in *CreateMemoryRequest) { in.MemoryType = "bad" },
			field:  "memory_type",
		},
		{
			name:   "正文为空",
			mutate: func(in *CreateMemoryRequest) { in.Content = "   " },
			field:  "content",
		},
		{
			name:   "正文超长",
			mutate: func(in *CreateMemoryRequest) { in.Content = strings.Repeat("记", domain.MaxMemoryContentRunes+1) },
			field:  "content",
		},
		{
			name:   "分类非法",
			mutate: func(in *CreateMemoryRequest) { in.Category = "bad" },
			field:  "category",
		},
		{
			name:   "重要性越界",
			mutate: func(in *CreateMemoryRequest) { in.Importance = intPtr(6) },
			field:  "importance",
		},
		{
			name: "关键词超长",
			mutate: func(in *CreateMemoryRequest) {
				in.Keywords = []string{strings.Repeat("k", domain.MaxMemoryKeywordRunes+1)}
			},
			field: "keywords",
		},
		{
			name:   "推断级别非法",
			mutate: func(in *CreateMemoryRequest) { in.InferenceLevel = "bad" },
			field:  "inference_level",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			if in.Keywords == nil {
				in.Keywords = []string{"go"}
			}
			tc.mutate(&in)
			svc := testService(Deps{})
			_, err := svc.CreateAIMemory(context.Background(), 1, 7, in)
			details := validationDetails(t, err)
			if _, ok := details[tc.field]; !ok {
				t.Fatalf("应返回 %s 字段错误，实际 %v", tc.field, details)
			}
		})
	}
}

// TestCreateAIMemoryValidStopsBeforeRepo 校验合法输入行进到事务入口（不触库）。
func TestCreateAIMemoryValidStopsBeforeRepo(t *testing.T) {
	svc := testService(Deps{})
	_, err := svc.CreateAIMemory(context.Background(), 1, 7, CreateMemoryRequest{
		MemoryType: domain.MemoryTypeRecent,
		Content:    "用户喜欢 Go 语言",
		Category:   domain.MemoryCategoryPreference,
	})
	requireTransactionMissing(t, err)
}

// TestClearMemoriesValidation 校验清空记忆的 type 校验与合法输入的事务入口。
func TestClearMemoriesValidation(t *testing.T) {
	svc := testService(Deps{})
	_, err := svc.ClearMemories(context.Background(), 1, 7, "bad")
	validationDetails(t, err)

	_, err = svc.ClearMemories(context.Background(), 1, 7, domain.MemoryTypeLongTerm)
	requireTransactionMissing(t, err)
}

// TestMemoryWritePathsRequireTransaction 校验编辑/删除记忆在事务入口前不触库。
func TestMemoryWritePathsRequireTransaction(t *testing.T) {
	ctx := context.Background()
	svc := testService(Deps{})
	_, err := svc.UpdateMemory(ctx, 1, 7, 9, UpdateMemoryRequest{
		Content: strPtr("新正文"),
		Status:  strPtr(domain.MemoryStatusActive),
	})
	requireTransactionMissing(t, err)

	requireTransactionMissing(t, svc.DeleteMemory(ctx, 1, 7, 9))
}

// TestCreateMemoryRejectsInvalidInput 校验 agent 侧写入记忆的入参校验。
func TestCreateMemoryRejectsInvalidInput(t *testing.T) {
	ctx := context.Background()
	svc := testService(Deps{})
	_, err := svc.CreateMemory(ctx, contracts.CreateMemoryInput{
		AIID: 7, MemoryType: "bad", Content: "内容", Importance: 3,
	})
	e := requireKind(t, err, apperr.KindInvalid)
	if e.Message != "memory_type 必须是 recent 或 long_term" {
		t.Fatalf("错误文案错误: %q", e.Message)
	}

	_, err = svc.CreateMemory(ctx, contracts.CreateMemoryInput{
		AIID: 7, MemoryType: domain.MemoryTypeRecent, Content: "  ", Importance: 3,
	})
	details := validationDetails(t, err)
	if _, ok := details["content"]; !ok {
		t.Fatalf("空正文应返回 content 错误，实际 %v", details)
	}

	_, err = svc.CreateMemory(ctx, contracts.CreateMemoryInput{
		AIID: 7, MemoryType: domain.MemoryTypeRecent, Content: "内容", Importance: 0,
	})
	details = validationDetails(t, err)
	if _, ok := details["importance"]; !ok {
		t.Fatalf("重要性越界应返回 importance 错误，实际 %v", details)
	}
}

// TestCreateMemoryIDError 校验 agent 侧规范化通过后 ID 生成失败映射为内部错误。
func TestCreateMemoryIDError(t *testing.T) {
	cause := errors.New("雪花生成失败")
	svc := testService(Deps{IDs: &fakeIDs{err: cause}})
	_, err := svc.CreateMemory(context.Background(), contracts.CreateMemoryInput{
		AIID: 7, MemoryType: domain.MemoryTypeRecent, Content: "用户喜欢 Go 语言", Importance: 3,
	})
	requireKind(t, err, apperr.KindInternal)
	if !errors.Is(err, cause) {
		t.Fatal("ID 生成错误应保留底因")
	}
}

// TestAddMemorySourcesEmptyInput 校验空来源直接成功且不触库。
func TestAddMemorySourcesEmptyInput(t *testing.T) {
	svc := testService(Deps{})
	if err := svc.AddMemorySources(context.Background(), 1, nil); err != nil {
		t.Fatalf("空来源应返回 nil，实际 %v", err)
	}
	if err := svc.AddMemorySources(context.Background(), 1, []contracts.MemorySourceInput{}); err != nil {
		t.Fatalf("空切片来源应返回 nil，实际 %v", err)
	}
}

// TestInvalidateMemoriesBySourcesEmptyInput 校验空来源直接返回 0 且不触库。
func TestInvalidateMemoriesBySourcesEmptyInput(t *testing.T) {
	svc := testService(Deps{})
	affected, err := svc.InvalidateMemoriesBySources(context.Background(),
		contracts.InvalidateMemoriesBySourcesInput{AIID: 7})
	if err != nil || affected != 0 {
		t.Fatalf("空来源应返回 (0, nil)，实际 (%d, %v)", affected, err)
	}
}

// TestMemoriesToContracts 校验领域记忆到跨模块视图的字段映射。
func TestMemoriesToContracts(t *testing.T) {
	created := time.Date(2026, 10, 18, 12, 0, 0, 0, clock.AsiaShanghai)
	updated := created.Add(time.Minute)
	lastUsed := updated.Add(time.Minute)
	sourceRefID := int64(321)
	input := []domain.Memory{
		{
			ID:             11,
			AID:            7,
			MemoryType:     domain.MemoryTypeLongTerm,
			Status:         domain.MemoryStatusActive,
			Content:        "长期记忆",
			Category:       domain.MemoryCategoryFact,
			Keywords:       []string{"go"},
			Importance:     5,
			SourceType:     domain.MemorySourceConsolidation,
			SourceRefType:  domain.MemorySourceTypePost,
			SourceRefID:    &sourceRefID,
			InferenceLevel: domain.MemoryInferenceSummary,
			CreatedAt:      created,
			UpdatedAt:      updated,
			LastUsedAt:     &lastUsed,
		},
	}
	got := memoriesToContracts(input)
	if len(got) != 1 {
		t.Fatalf("映射结果长度错误: %d", len(got))
	}
	memory := got[0]
	if memory.ID != 11 || memory.AID != 7 || memory.MemoryType != domain.MemoryTypeLongTerm ||
		memory.Status != domain.MemoryStatusActive || memory.Content != "长期记忆" ||
		memory.Category != domain.MemoryCategoryFact || len(memory.Keywords) != 1 ||
		memory.Importance != 5 || memory.SourceType != domain.MemorySourceConsolidation ||
		memory.SourceRefType != domain.MemorySourceTypePost || memory.SourceRefID == nil ||
		*memory.SourceRefID != 321 || memory.InferenceLevel != domain.MemoryInferenceSummary ||
		!memory.CreatedAt.Equal(created) || !memory.UpdatedAt.Equal(updated) ||
		memory.LastUsedAt == nil || !memory.LastUsedAt.Equal(lastUsed) {
		t.Fatalf("记忆映射字段错误: %+v", memory)
	}

	// 空输入返回空切片而不是 nil，便于上层直接序列化为 []。
	empty := memoriesToContracts(nil)
	if empty == nil || len(empty) != 0 {
		t.Fatalf("空输入应返回空切片，实际 %#v", empty)
	}
}

// TestMemoryManagementTypesZeroValue 确认记忆管理视图类型零值可用（结构防漂移）。
func TestMemoryManagementTypesZeroValue(t *testing.T) {
	var list MemoryList
	if list.Items != nil || list.LastID != 0 || list.HasMore {
		t.Fatalf("MemoryList 零值异常: %+v", list)
	}
	var filter MemoryFilter
	if filter.MemoryType != "" || filter.Status != "" || filter.Query != "" ||
		filter.Cursor != 0 || filter.Limit != 0 {
		t.Fatalf("MemoryFilter 零值异常: %+v", filter)
	}
	var create CreateMemoryRequest
	if create.Importance != nil || create.Keywords != nil {
		t.Fatalf("CreateMemoryRequest 零值异常: %+v", create)
	}
}
