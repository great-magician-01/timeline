package domain

import (
	"context"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/shared/page"
)

func TestNormalizeVisibility(t *testing.T) {
	for raw, want := range map[string]string{
		"":            model.VisibilityPublic,
		"public":      model.VisibilityPublic,
		" Followers ": model.VisibilityFollowers,
		"PRIVATE":     model.VisibilityPrivate,
	} {
		got, err := NormalizeVisibility(raw)
		if err != nil || got != want {
			t.Errorf("NormalizeVisibility(%q) = %q, %v；期望 %q", raw, got, err, want)
		}
	}
	if _, err := NormalizeVisibility("friends"); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("非法可见性应返回 validation，实际 %v", err)
	}
}

func TestNormalizeActionFilterTabFormat(t *testing.T) {
	if got, err := NormalizeAction("publish"); err != nil || got != ActionPublish {
		t.Fatalf("NormalizeAction(publish) = %q, %v", got, err)
	}
	if _, err := NormalizeAction("unknown"); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("非法 action 应返回 validation，实际 %v", err)
	}
	if got, err := NormalizeFilter(""); err != nil || got != FilterAll {
		t.Fatalf("NormalizeFilter 默认值错误: %q, %v", got, err)
	}
	if _, err := NormalizeFilter("bots"); !apperr.IsKind(err, apperr.KindInvalid) {
		t.Fatalf("非法 filter 应返回 invalid，实际 %v", err)
	}
	if got, err := NormalizeTab("replies"); err != nil || got != TabReplies {
		t.Fatalf("NormalizeTab 错误: %q, %v", got, err)
	}
	if _, err := NormalizeTab("likes"); !apperr.IsKind(err, apperr.KindInvalid) {
		t.Fatalf("非法 tab 应返回 invalid，实际 %v", err)
	}
	if _, err := NormalizeFormat("pdf"); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("非法格式应返回 validation，实际 %v", err)
	}
}

func TestRenderContent(t *testing.T) {
	html, plain, err := RenderContent("markdown", "**粗体** 正文")
	if err != nil || plain == "" {
		t.Fatalf("RenderContent 失败: %v", err)
	}
	if html == "" || plain != "粗体 正文" {
		t.Fatalf("渲染结果异常: html=%q plain=%q", html, plain)
	}
	if _, _, err := RenderContent("richtext", "  <p> </p> "); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("空正文应返回 validation，实际 %v", err)
	}
}

func TestCheckVisibilityPolicy(t *testing.T) {
	if err := CheckVisibilityPolicy(true, model.VisibilityPublic); err != nil {
		t.Fatalf("AI 公开内容应允许: %v", err)
	}
	if err := CheckVisibilityPolicy(true, model.VisibilityPrivate); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("AI 非公开内容应拒绝，实际 %v", err)
	}
	if err := CheckVisibilityPolicy(false, model.VisibilityPrivate); err != nil {
		t.Fatalf("人类私有内容应允许: %v", err)
	}
}

func TestSummary(t *testing.T) {
	if got := Summary("  短文本  "); got != "短文本" {
		t.Fatalf("Summary 去空白错误: %q", got)
	}
	long := ""
	for i := 0; i < SummaryRunes+10; i++ {
		long += "字"
	}
	got := Summary(long)
	if len([]rune(got)) != SummaryRunes {
		t.Fatalf("Summary 截断长度错误: %d", len([]rune(got)))
	}
}

func TestParseAttachmentIDsAndQuotedPostID(t *testing.T) {
	ids, err := ParseAttachmentIDs([]string{"2", "1", "2", " 3 "})
	if err != nil || len(ids) != 3 || ids[0] != 2 || ids[2] != 3 {
		t.Fatalf("附件 ID 去重/保序错误: %+v, %v", ids, err)
	}
	if _, err := ParseAttachmentIDs([]string{"0"}); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("非法附件 ID 应返回 validation，实际 %v", err)
	}
	if got, err := ParseQuotedPostID(nil); err != nil || got != nil {
		t.Fatalf("空引用应为 nil: %v, %v", got, err)
	}
	empty := "   "
	if got, err := ParseQuotedPostID(&empty); err != nil || got != nil {
		t.Fatalf("空白引用应为 nil: %v, %v", got, err)
	}
	if got, err := ParseQuotedPostID(strPtr("9")); err != nil || got == nil || *got != 9 {
		t.Fatalf("引用 ID 解析错误: %v, %v", got, err)
	}
	if _, err := ParseQuotedPostID(strPtr("x")); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("非法引用应返回 validation，实际 %v", err)
	}
}

func TestScheduledAtValidation(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	if err := ValidateScheduledAt(now.Add(2*time.Minute), now); err != nil {
		t.Fatalf("合法定时时间应通过: %v", err)
	}
	if err := ValidateScheduledAt(now.Add(30*time.Second), now); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("过早时间应返回 validation，实际 %v", err)
	}
	if err := ValidateFutureScheduledAt(now.Add(time.Second), now); err != nil {
		t.Fatalf("未来时间应通过: %v", err)
	}
	if err := ValidateFutureScheduledAt(now, now); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("当前时间应返回 validation，实际 %v", err)
	}
	at, err := ParseScheduledAt("2026-10-18T20:30:00+08:00", now)
	if err != nil || at.IsZero() {
		t.Fatalf("RFC3339 解析失败: %v", err)
	}
	if _, err := ParseScheduledAt("", now); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("空定时时间应返回 validation，实际 %v", err)
	}
	if _, err := ParseScheduledAt("2026/10/18 20:30", now); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("非法格式应返回 validation，实际 %v", err)
	}
}

func TestPostStateMachine(t *testing.T) {
	if !CanPublish(model.PostStatusDraft) || !CanPublish(model.PostStatusScheduled) {
		t.Fatalf("草稿与定时帖应可发布")
	}
	if CanPublish(model.PostStatusPublished) || CanPublish(model.PostStatusDeleted) {
		t.Fatalf("已发布/已删除不应再次发布")
	}
	if !CanSetSchedule(model.PostStatusDraft) || CanSetSchedule(model.PostStatusPublished) {
		t.Fatalf("定时状态判断错误")
	}
	if !Editable(model.PostStatusScheduled) || Editable(model.PostStatusPublished) {
		t.Fatalf("可编辑状态判断错误")
	}
	if err := CheckPublishState(model.PostStatusDeleted); !apperr.IsKind(err, apperr.KindConflict) {
		t.Fatalf("非法发布状态应返回 conflict，实际 %v", err)
	}
	if got := NextVersionNumber(3); got != 4 {
		t.Fatalf("NextVersionNumber = %d", got)
	}
	if got := NextVersionNumber(0); got != 1 {
		t.Fatalf("初始版本号应为 1，实际 %d", got)
	}
}

func TestPublishStateFor(t *testing.T) {
	status, failed, message := PublishStateFor(model.PostStatusScheduled, "failed", "接口超时")
	if status != "failed" || !failed || message != "接口超时" {
		t.Fatalf("失败状态映射错误: %q %v %q", status, failed, message)
	}
	status, failed, message = PublishStateFor(model.PostStatusScheduled, "paused", "")
	if status != "paused" || failed || message != "" {
		t.Fatalf("暂停状态映射错误: %q %v %q", status, failed, message)
	}
	status, failed, message = PublishStateFor(model.PostStatusScheduled, "pending", "")
	if status != model.PostStatusScheduled || failed || message != "" {
		t.Fatalf("待执行状态应沿用帖子状态: %q %v %q", status, failed, message)
	}
}

func TestPublishJobKeyAndScheduleTime(t *testing.T) {
	at := time.Unix(1790000000, 0)
	key := PublishJobKey(123, at)
	if key != "post_publish:123:1790000000" {
		t.Fatalf("作业幂等键不兼容: %q", key)
	}
	if !SameScheduleTime(at, at.Add(300*time.Millisecond)) {
		t.Fatalf("秒级精度比较应相等")
	}
	if SameScheduleTime(at, at.Add(2*time.Second)) {
		t.Fatalf("不同秒应判定为已变更")
	}
}

func TestCounterHelpers(t *testing.T) {
	column, err := CounterColumn("like")
	if err != nil || column != "like_count" {
		t.Fatalf("计数器列映射错误: %q, %v", column, err)
	}
	if _, err := CounterColumn("view"); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("未知计数器应返回 validation，实际 %v", err)
	}
	if err := ValidateCounterDelta(0); !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("零增减应返回 validation，实际 %v", err)
	}
	if err := ValidateCounterDelta(-1); err != nil {
		t.Fatalf("负增减应允许（取消点赞等）: %v", err)
	}
}

func TestUniqueAndSortedIDs(t *testing.T) {
	ids := UniqueIDs([]int64{3, 0, 1, 3, -2, 2})
	if len(ids) != 3 || ids[0] != 3 || ids[1] != 1 || ids[2] != 2 {
		t.Fatalf("UniqueIDs 错误: %+v", ids)
	}
	sorted := SortedIDs(map[int64]bool{3: true, 1: true, 2: false})
	if len(sorted) != 2 || sorted[0] != 1 || sorted[1] != 3 {
		t.Fatalf("SortedIDs 错误: %+v", sorted)
	}
}

func TestCanViewPublished(t *testing.T) {
	post := model.Post{ID: 1, AuthorID: 2, Status: model.PostStatusPublished, Visibility: model.VisibilityPublic}
	if !CanViewPublished(model.GuestViewer(), post, nil) {
		t.Fatalf("公开已发布内容游客可见")
	}
	draft := post
	draft.Status = model.PostStatusDraft
	if CanViewPublished(model.GuestViewer(), draft, nil) {
		t.Fatalf("草稿不应判定为可见已发布")
	}
}

func TestCollectPage(t *testing.T) {
	type item struct {
		id   int64
		at   time.Time
		keep bool
	}
	start := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	fetch := func(_ context.Context, after page.TimeCursor, limit int) ([]item, error) {
		items := make([]item, 0, limit)
		for i := 0; i < limit; i++ {
			at := start.Add(-time.Duration(len(items)+1) * time.Minute)
			if !after.Time.IsZero() {
				at = after.Time.Add(-time.Duration(i+1) * time.Minute)
			}
			items = append(items, item{id: int64(100 - i), at: at, keep: true})
		}
		return items, nil
	}
	key := func(i item) (time.Time, int64) { return i.at, i.id }
	collected, _, hasMore, err := CollectPage(context.Background(), page.TimeCursor{}, 3, fetch, key, nil)
	if err != nil || len(collected) != 3 || !hasMore {
		t.Fatalf("CollectPage 基本分页错误: len=%d hasMore=%v err=%v", len(collected), hasMore, err)
	}

	// 过滤掉一部分候选时继续取下一批。
	calls := 0
	fetchFiltered := func(_ context.Context, after page.TimeCursor, limit int) ([]item, error) {
		calls++
		items := make([]item, 0, limit)
		for i := 0; i < limit; i++ {
			at := start.Add(-time.Duration(calls*10+i) * time.Minute)
			items = append(items, item{id: int64(calls*100 - i), at: at, keep: i%2 == 0})
		}
		return items, nil
	}
	keep := func(i item) bool { return i.keep }
	collected, _, _, err = CollectPage(context.Background(), page.TimeCursor{}, 4, fetchFiltered, key, keep)
	if err != nil || len(collected) != 4 || calls < 2 {
		t.Fatalf("CollectPage 过滤后补批错误: len=%d calls=%d err=%v", len(collected), calls, err)
	}
}

func strPtr(value string) *string { return &value }
