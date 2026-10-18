package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/great-magician-01/timeline/internal/modules/ai/repo"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// 说明：service.go 的辅助函数负责错误映射、审计写入、ID 生成与 AI 身份校验；
// 这些函数不触库（ensureAIUser 通过 contracts.UserReader fake 验证），因此可在无数据库下测。

// TestFormatID 校验雪花 ID 的日志/审计字符串化。
func TestFormatID(t *testing.T) {
	cases := []struct {
		id   int64
		want string
	}{
		{id: 0, want: "0"},
		{id: 1, want: "1"},
		{id: 1234567890123456789, want: "1234567890123456789"},
		{id: -1, want: "-1"},
	}
	for _, tc := range cases {
		if got := formatID(tc.id); got != tc.want {
			t.Errorf("formatID(%d) = %q，期望 %q", tc.id, got, tc.want)
		}
	}
}

// TestFirstDetail 校验字段详情的第一条提示与空详情兜底文案。
func TestFirstDetail(t *testing.T) {
	if got := firstDetail(nil); got != "请求参数不符合要求" {
		t.Fatalf("空详情应返回兜底文案，实际 %q", got)
	}
	if got := firstDetail(map[string]string{}); got != "请求参数不符合要求" {
		t.Fatalf("空 map 应返回兜底文案，实际 %q", got)
	}
	if got := firstDetail(map[string]string{"username": "用户名无效"}); got != "用户名无效" {
		t.Fatalf("单条详情应返回该提示，实际 %q", got)
	}
	// 多条详情顺序不保证，只断言返回其中之一。
	multi := map[string]string{"a": "提示A", "b": "提示B"}
	got := firstDetail(multi)
	if got != "提示A" && got != "提示B" {
		t.Fatalf("多详情应返回其中之一，实际 %q", got)
	}
}

// TestValidationError 校验字段校验错误携带 validation_failed 与详情。
func TestValidationError(t *testing.T) {
	details := map[string]string{"username": "用户名无效", "bio": "简介过长"}
	err := validationError(details)
	e := requireKind(t, err, apperr.KindValidation)
	if e.Code != "validation_failed" {
		t.Fatalf("错误码应为 validation_failed，实际 %q", e.Code)
	}
	got, ok := e.Details.(map[string]string)
	if !ok || len(got) != 2 || got["username"] != "用户名无效" {
		t.Fatalf("详情映射错误: %#v", e.Details)
	}
	if e.Message != "用户名无效" && e.Message != "简介过长" {
		t.Fatalf("错误消息应取第一条详情，实际 %q", e.Message)
	}
}

// TestInternalError 校验内部错误包装保留底因且使用统一安全文案。
func TestInternalError(t *testing.T) {
	cause := errors.New("底层失败")
	err := internalError(cause, "生成资源 ID 失败")
	e := requireKind(t, err, apperr.KindInternal)
	if e.Message != "服务暂时异常，请稍后重试" {
		t.Fatalf("内部错误文案应统一，实际 %q", e.Message)
	}
	if !errors.Is(err, cause) {
		t.Fatal("内部错误应可通过 errors.Is 穿透到原始错误")
	}
	var target *apperr.Error
	if !errors.As(err, &target) || target.Kind != apperr.KindInternal {
		t.Fatalf("内部错误应可 errors.As 为 *apperr.Error，实际 %v", err)
	}
}

// TestNotFoundIfNoRows 校验 repo 行不存在错误映射为 not_found，其它错误映射为内部错误。
func TestNotFoundIfNoRows(t *testing.T) {
	if err := notFoundIfNoRows(nil, "模型接口预设不存在"); err != nil {
		t.Fatalf("nil 错误应返回 nil，实际 %v", err)
	}
	e := requireKind(t, notFoundIfNoRows(repo.ErrNotFound, "模型接口预设不存在"), apperr.KindNotFound)
	if e.Message != "模型接口预设不存在" {
		t.Fatalf("not_found 文案错误: %q", e.Message)
	}
	wrapped := fmt.Errorf("查询失败: %w", repo.ErrNotFound)
	requireKind(t, notFoundIfNoRows(wrapped, "记忆不存在"), apperr.KindNotFound)

	cause := errors.New("连接中断")
	err := notFoundIfNoRows(cause, "模型接口预设不存在")
	requireKind(t, err, apperr.KindInternal)
	if !errors.Is(err, cause) {
		t.Fatal("内部错误应保留底因")
	}
}

// TestIsUniqueViolation 校验 PostgreSQL 唯一冲突判断（含错误包装穿透）。
func TestIsUniqueViolation(t *testing.T) {
	if !isUniqueViolation(fakePgError{code: "23505"}) {
		t.Fatal("SQLState 23505 应判定为唯一冲突")
	}
	if isUniqueViolation(fakePgError{code: "23503"}) {
		t.Fatal("SQLState 23503 不是唯一冲突")
	}
	if isUniqueViolation(errors.New("普通错误")) {
		t.Fatal("普通错误不是唯一冲突")
	}
	if isUniqueViolation(nil) {
		t.Fatal("nil 不是唯一冲突")
	}
	wrapped := fmt.Errorf("写库失败: %w", fakePgError{code: "23505"})
	if !isUniqueViolation(wrapped) {
		t.Fatal("包装后的唯一冲突仍应被识别")
	}
}

// TestNewID 校验 ID 生成错误被包装为内部错误。
func TestNewID(t *testing.T) {
	ids := &fakeIDs{id: 42}
	svc := testService(Deps{IDs: ids})
	got, err := svc.newID()
	if err != nil || got != 42 {
		t.Fatalf("生成 ID 失败: got=%d err=%v", got, err)
	}
	if ids.calls != 1 {
		t.Fatalf("应调用一次 ID 生成器，实际 %d", ids.calls)
	}

	cause := errors.New("雪花时钟回拨")
	svc = testService(Deps{IDs: &fakeIDs{err: cause}})
	_, err = svc.newID()
	requireKind(t, err, apperr.KindInternal)
	if !errors.Is(err, cause) {
		t.Fatal("ID 生成错误应保留底因")
	}
}

// TestWriteAudit 校验审计写入参数与失败映射。
func TestWriteAudit(t *testing.T) {
	audit := &fakeAudit{}
	svc := testService(Deps{Audit: audit})
	targetID := int64(99)
	err := svc.writeAudit(context.Background(), 7, contracts.AuditActionAICreate, targetAI, &targetID,
		map[string]any{"username": "ai_demo"}, testNow)
	if err != nil {
		t.Fatalf("写入审计失败: %v", err)
	}
	if len(audit.entries) != 1 {
		t.Fatalf("应写入一条审计，实际 %d", len(audit.entries))
	}
	entry := audit.entries[0]
	if entry.AdminID != 7 || entry.Action != contracts.AuditActionAICreate ||
		entry.TargetType != targetAI || entry.TargetID == nil || *entry.TargetID != 99 ||
		!entry.At.Equal(testNow) {
		t.Fatalf("审计参数错误: %+v", entry)
	}

	cause := errors.New("审计表不可用")
	svc = testService(Deps{Audit: &fakeAudit{err: cause}})
	err = svc.writeAudit(context.Background(), 7, "x", targetAI, nil, nil, testNow)
	requireKind(t, err, apperr.KindInternal)
	if !errors.Is(err, cause) {
		t.Fatal("审计错误应保留底因")
	}
}

// TestEnsureAIUser 校验目标账号必须是 AI 用户；非 AI 与不存在都映射为 not_found。
func TestEnsureAIUser(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name    string
		users   fakeUsers
		wantErr apperr.Kind
	}{
		{
			name:  "AI 用户通过",
			users: fakeUsers{user: aiUser(10)},
		},
		{
			name:    "非 AI 用户按不存在处理",
			users:   fakeUsers{user: humanUser(10)},
			wantErr: apperr.KindNotFound,
		},
		{
			name:    "账号不存在",
			users:   fakeUsers{getErr: apperr.NotFound("账号不存在")},
			wantErr: apperr.KindNotFound,
		},
		{
			name:    "读取账号内部错误",
			users:   fakeUsers{getErr: errors.New("数据库不可用")},
			wantErr: apperr.KindInternal,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := testService(Deps{Users: tc.users})
			err := svc.ensureAIUser(ctx, 10)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("应通过校验，实际 %v", err)
				}
				return
			}
			e := requireKind(t, err, tc.wantErr)
			if e.Message != "AI 用户不存在" && tc.wantErr == apperr.KindNotFound {
				t.Fatalf("非 AI/不存在应统一返回「AI 用户不存在」，实际 %q", e.Message)
			}
		})
	}
}

// TestServiceNowUsesClock 校验取时只来自应用时钟。
func TestServiceNowUsesClock(t *testing.T) {
	svc := testService(Deps{})
	if got := svc.now(); !got.Equal(testNow) {
		t.Fatalf("now 应返回注入时钟的时间，实际 %v", got)
	}
}

// TestWithTxNotInitialized 校验事务管理器缺失时在进入用例前返回明确错误。
func TestWithTxNotInitialized(t *testing.T) {
	svc := testService(Deps{})
	called := false
	err := svc.withTx(context.Background(), func(context.Context) error {
		called = true
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "事务管理器未初始化") {
		t.Fatalf("缺少事务管理器时应返回初始化错误，实际 %v", err)
	}
	if called {
		t.Fatal("事务未开启时不应执行用例函数")
	}
}

// TestGetAIRejectsMissingOrHumanUser 校验 GetAI/详情组装的账号校验分支。
func TestGetAIRejectsMissingOrHumanUser(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name    string
		users   fakeUsers
		wantErr apperr.Kind
	}{
		{name: "账号不存在", users: fakeUsers{getErr: apperr.NotFound("账号不存在")}, wantErr: apperr.KindNotFound},
		{name: "人类账号", users: fakeUsers{user: humanUser(3)}, wantErr: apperr.KindNotFound},
		{name: "内部错误", users: fakeUsers{getErr: errors.New("连接失败")}, wantErr: apperr.KindInternal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := testService(Deps{Users: tc.users})
			_, err := svc.GetAI(ctx, 3)
			requireKind(t, err, tc.wantErr)
		})
	}
}

// TestListAIsAndEnabledAIIDsUserErrors 校验账号端口失败时的内部错误映射。
func TestListAIsAndEnabledAIIDsUserErrors(t *testing.T) {
	cause := errors.New("账号服务不可用")
	svc := testService(Deps{Users: fakeUsers{idsErr: cause}})
	if _, err := svc.ListAIs(context.Background(), AIFilter{Limit: 0}); !errors.Is(err, cause) {
		t.Fatalf("ListAIs 应保留账号端口错误: %v", err)
	} else {
		requireKind(t, err, apperr.KindInternal)
	}
	_, err := svc.EnabledAIIDs(context.Background())
	requireKind(t, err, apperr.KindInternal)
	if !errors.Is(err, cause) {
		t.Fatal("EnabledAIIDs 应保留账号端口错误")
	}
}

// TestRuntimeByAIUserErrors 校验运行时快照的账号校验分支（不触库）。
func TestRuntimeByAIUserErrors(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name    string
		users   fakeUsers
		wantErr apperr.Kind
	}{
		{name: "账号不存在", users: fakeUsers{getErr: apperr.NotFound("账号不存在")}, wantErr: apperr.KindNotFound},
		{name: "非 AI 账号", users: fakeUsers{user: humanUser(5)}, wantErr: apperr.KindNotFound},
		{name: "内部错误", users: fakeUsers{getErr: errors.New("查询失败")}, wantErr: apperr.KindInternal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := testService(Deps{Users: tc.users})
			_, err := svc.RuntimeByAI(ctx, 5)
			requireKind(t, err, tc.wantErr)
		})
	}
}

// TestAIDetailTypesZeroValue 确认数据视图类型零值可用（编译期/结构防漂移）。
func TestAIDetailTypesZeroValue(t *testing.T) {
	var detail AIDetail
	if detail.User.ID != 0 || detail.RecentTasks != nil {
		t.Fatalf("AIDetail 零值异常: %+v", detail)
	}
	var list AIList
	if list.Items != nil || list.HasMore || list.LastID != 0 {
		t.Fatalf("AIList 零值异常: %+v", list)
	}
	var memoryCounts MemoryCounts
	if memoryCounts.Recent != 0 || memoryCounts.LongTerm != 0 {
		t.Fatalf("MemoryCounts 零值异常: %+v", memoryCounts)
	}
	if got := (AIListItem{User: model.UserBrief{ID: 1}}).User.ID; got != 1 {
		t.Fatalf("AIListItem 字段异常: %d", got)
	}
}
