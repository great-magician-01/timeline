package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/great-magician-01/callable"

	"github.com/great-magician-01/timeline/internal/ai"
	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/model"
)

// 所有测试都不依赖数据库与网络，只覆盖纯逻辑：
// 决策解析、动作白名单、频率与冷却、活跃时段、上下文预算、幂等键与记忆归一化。

// ── 决策解析与动作白名单 ──

func TestParseDecisionValid(t *testing.T) {
	raw := `{"action":"ORIGINAL_POST","target_type":"","target_id":"","content":"  今天聊聊 Go 的并发  ","reason":"主动分享"}`
	decision, err := parseDecision(raw)
	if err != nil {
		t.Fatalf("合法决策不应报错: %v", err)
	}
	if decision.Action != actionOriginalPost {
		t.Fatalf("动作应规范化为 %s，实际 %s", actionOriginalPost, decision.Action)
	}
	if decision.Content != "今天聊聊 Go 的并发" {
		t.Fatalf("正文应去除首尾空白，实际 %q", decision.Content)
	}
	if decision.Reason != "主动分享" {
		t.Fatalf("决策摘要不一致: %q", decision.Reason)
	}
}

func TestParseDecisionNoop(t *testing.T) {
	decision, err := parseDecision(`{"action":"noop","reason":"没有合适的内容"}`)
	if err != nil {
		t.Fatalf("noop 是合法决策: %v", err)
	}
	if decision.Action != actionNoop {
		t.Fatalf("动作应为 noop，实际 %s", decision.Action)
	}
	if !actionAllowed(ai.BehaviorSettings{}, decision.Action) {
		t.Fatal("noop 不需要动作开关，应始终允许")
	}
	if isRetryableTaskStatus(statusNoop) {
		t.Fatal("noop 不应重试")
	}
	if retryPending(1, maxTaskAttempts) == false || isRetryableTaskStatus(statusSucceeded) {
		t.Fatal("只有 failed 与 pending 可以继续执行")
	}
}

func TestParseDecisionInvalidAction(t *testing.T) {
	cases := []string{
		`{"action":"delete_post"}`,
		`{"action":"unrepost"}`,
		`{"action":""}`,
		`{"action":"like","target_type":"topic","target_id":"1"}`,
	}
	for _, raw := range cases {
		if _, err := parseDecision(raw); !errors.Is(err, errInvalidAction) {
			t.Fatalf("非法动作应返回 errInvalidAction: %s → %v", raw, err)
		}
	}
}

func TestParseDecisionInvalidJSON(t *testing.T) {
	cases := []string{
		"",
		"   ",
		"抱歉，我无法完成",
		`{"action":"like","unknown_field":1}`,
		`{"action":"like"`,
	}
	for _, raw := range cases {
		if _, err := parseDecision(raw); !errors.Is(err, errInvalidJSON) {
			t.Fatalf("非法 JSON 应返回 errInvalidJSON: %q → %v", raw, err)
		}
	}
}

func TestParseDecisionStripsCodeFence(t *testing.T) {
	raw := "```json\n{\"action\":\"like\",\"target_type\":\"post\",\"target_id\":\"123\"}\n```"
	decision, err := parseDecision(raw)
	if err != nil {
		t.Fatalf("代码块包裹的决策应可解析: %v", err)
	}
	if decision.Action != actionLike || decision.TargetID != "123" {
		t.Fatalf("解析结果不符合预期: %+v", decision)
	}
}

func TestDecisionActionWhitelist(t *testing.T) {
	// unlike / unfollow 属于需求 13.2 允许的 AI 动作，必须能出现在决策白名单里；
	// 它们的执行受“目标必须是自己当前持有的关系”约束（见 validateDecisionTarget）。
	for _, action := range []string{
		actionOriginalPost, actionComment, actionReply, actionLike, actionUnlike,
		actionFollow, actionUnfollow, actionRepost, actionQuote, actionNoop,
	} {
		if !validDecisionAction(action) {
			t.Fatalf("动作 %s 应在决策白名单内", action)
		}
	}
	for _, action := range []string{"delete_post", "unrepost", ""} {
		if validDecisionAction(action) {
			t.Fatalf("动作 %s 不应出现在决策白名单内", action)
		}
	}
	if !validActionType(actionUnlike) || !validActionType(actionUnfollow) || validActionType("drop_table") {
		t.Fatal("任务动作取值校验与数据库 CHECK 约束不一致")
	}
}

func TestActionAllowedSwitches(t *testing.T) {
	behavior := ai.BehaviorSettings{
		AllowOriginalPost:  true,
		AllowActiveComment: false,
		AllowPassiveReply:  true,
		AllowLike:          true,
	}
	if !actionAllowed(behavior, actionOriginalPost) || !actionAllowed(behavior, actionReply) {
		t.Fatal("已开启的动作应被允许")
	}
	if actionAllowed(behavior, actionComment) || actionAllowed(behavior, actionFollow) {
		t.Fatal("未开启的动作不应被允许")
	}
	if !proactiveAllowed(behavior) {
		t.Fatal("开启点赞即视为开启主动互动")
	}
	disabled := ai.BehaviorSettings{}
	if proactiveAllowed(disabled) {
		t.Fatal("全部关闭时不应允许主动互动")
	}
}

// ── 错误分类 ──

func TestClassifyCallError(t *testing.T) {
	cases := []struct {
		name        string
		err         error
		wantStatus  string
		wantErrType string
	}{
		{"限流可重试", &callable.APIError{StatusCode: 429}, statusFailed, errorTypeAPI429},
		{"服务端错误可重试", &callable.APIError{StatusCode: 503}, statusFailed, errorTypeAPI5xx},
		{"传输层错误可重试", &callable.APIError{StatusCode: 0}, statusFailed, errorTypeTransport},
		{"请求错误不重试", &callable.APIError{StatusCode: 400}, statusRejected, errorTypeAPI4xx},
		{"鉴权错误不重试", &callable.APIError{StatusCode: 401}, statusRejected, errorTypeAPI4xx},
		{"超时可重试", context.DeadlineExceeded, statusFailed, errorTypeTimeout},
		{"上下文取消", context.Canceled, statusCanceled, errorTypeTimeout},
		{"其他错误按内部错误", errors.New("boom"), statusFailed, errorTypeInternal},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			status, errorType, message := classifyCallError(item.err)
			if status != item.wantStatus || errorType != item.wantErrType {
				t.Fatalf("分类结果应为 (%s,%s)，实际 (%s,%s)", item.wantStatus, item.wantErrType, status, errorType)
			}
			if message == "" {
				t.Fatal("错误信息不应为空")
			}
		})
	}
	if status, _, _ := classifyCallError(nil); status != statusSucceeded {
		t.Fatalf("无错误应为 succeeded，实际 %s", status)
	}
}

func TestClassifyActionError(t *testing.T) {
	status, errorType := classifyActionError(api.ErrForbidden("账号已停用"))
	if status != statusRejected || errorType != errorTypeForbidden {
		t.Fatalf("业务拒绝应映射为 rejected/forbidden，实际 (%s,%s)", status, errorType)
	}
	if status, errorType := classifyActionError(errors.New("connection reset")); status != statusFailed ||
		errorType != errorTypeDatabase {
		t.Fatalf("数据库错误应映射为 failed/database，实际 (%s,%s)", status, errorType)
	}
}

// ── 每日上限与冷却 ──

func TestUnderDailyLimit(t *testing.T) {
	cases := []struct {
		count, limit int
		want         bool
	}{
		{0, 0, true},   // 上限 0 = 不限制
		{100, 0, true}, // 上限 0 = 不限制
		{0, 1, true},
		{4, 5, true},
		{5, 5, false},
		{6, 5, false},
	}
	for _, item := range cases {
		if got := underDailyLimit(item.count, item.limit); got != item.want {
			t.Fatalf("count=%d limit=%d 期望 %v，实际 %v", item.count, item.limit, item.want, got)
		}
	}
}

func TestCooldownElapsed(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.Local)
	cases := []struct {
		name    string
		last    time.Time
		seconds int
		want    bool
	}{
		{"从未成功", time.Time{}, 900, true},
		{"冷却为 0 表示不限制", now.Add(-time.Second), 0, true},
		{"仍在冷却中", now.Add(-10 * time.Second), 900, false},
		{"刚好达到冷却边界", now.Add(-900 * time.Second), 900, true},
		{"已过冷却", now.Add(-901 * time.Second), 900, true},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if got := cooldownElapsed(item.last, now, item.seconds); got != item.want {
				t.Fatalf("期望 %v，实际 %v", item.want, got)
			}
		})
	}
}

func TestReplyDepthAllowed(t *testing.T) {
	if !replyDepthAllowed(1, 2) {
		t.Fatal("深度 1 的评论在上限 2 时应可回复")
	}
	if replyDepthAllowed(2, 2) {
		t.Fatal("达到深度上限后不应继续回复")
	}
	if replyDepthAllowed(1, 0) {
		t.Fatal("上限 0 表示 AI 不参与回复")
	}
}

// ── 活跃时段（含跨零点） ──

func TestWithinActiveHours(t *testing.T) {
	cases := []struct {
		name       string
		start, end int
		hour       int
		want       bool
	}{
		{"常规时段内", 8, 23, 8, true},
		{"常规时段内靠后", 8, 23, 22, true},
		{"常规时段结束点不含", 8, 23, 23, false},
		{"常规时段开始前", 8, 23, 7, false},
		{"跨零点夜间", 22, 6, 23, true},
		{"跨零点凌晨", 22, 6, 2, true},
		{"跨零点结束点不含", 22, 6, 6, false},
		{"跨零点白天不在内", 22, 6, 12, false},
		{"起止相同视为全天", 8, 8, 3, true},
		{"非法取值视为全天", -1, 30, 3, true},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if got := withinActiveHours(item.start, item.end, item.hour); got != item.want {
				t.Fatalf("start=%d end=%d hour=%d 期望 %v，实际 %v",
					item.start, item.end, item.hour, item.want, got)
			}
		})
	}
}

// ── 上下文预算截断 ──

func TestBuildUserPromptBudget(t *testing.T) {
	event := strings.Repeat("事件", 60)     // 120 字
	memory := strings.Repeat("记忆", 60)    // 120 字
	candidate := strings.Repeat("候选", 60) // 120 字
	parts := []promptPart{
		{Name: "event", Text: event},
		{Name: "memory", Text: memory},
		{Name: "candidates", Text: candidate},
	}

	// 预算充足时全部保留。
	full, truncated := buildUserPrompt(parts, 10000)
	if truncated || !strings.Contains(full, "候选") {
		t.Fatalf("预算充足时不应截断: truncated=%v", truncated)
	}

	// 预算不足时按优先级保留：事件 > 记忆 > 候选；低优先级片段先被截断。
	limited, truncated := buildUserPrompt(parts, 250)
	if !truncated {
		t.Fatal("预算不足时应标记截断")
	}
	if runes := len([]rune(limited)); runes > 250 {
		t.Fatalf("用户提示不应超过预算: %d", runes)
	}
	if !strings.HasPrefix(limited, event) {
		t.Fatal("高优先级的事件片段应完整保留")
	}
	if !strings.Contains(limited, memory) {
		t.Fatal("次级优先级的记忆片段应完整保留")
	}
	if strings.Contains(limited, candidate) {
		t.Fatal("最低优先级的候选片段应先被截断")
	}
	if !strings.Contains(limited, "候选") {
		t.Fatalf("候选片段应在剩余预算内尽量保留: %q", []rune(limited)[len([]rune(limited))-8:])
	}

	// 预算只够事件时，记忆与候选都必须让位。
	tight, truncated := buildUserPrompt(parts, 130)
	if !truncated || !strings.HasPrefix(tight, event) {
		t.Fatalf("预算紧张时应完整保留事件: truncated=%v", truncated)
	}
	if strings.Contains(tight, "候选") {
		t.Fatal("预算紧张时最低优先级的候选片段必须被丢弃")
	}
	if strings.Contains(tight, memory) {
		t.Fatal("预算紧张时记忆片段应被截断")
	}
}

func TestBuildUserPromptZeroBudget(t *testing.T) {
	text, truncated := buildUserPrompt([]promptPart{{Name: "event", Text: "内容"}}, 0)
	if text != "" || !truncated {
		t.Fatalf("预算为 0 时应全部截断: %q truncated=%v", text, truncated)
	}
	if text, truncated := buildUserPrompt(nil, 100); text != "" || truncated {
		t.Fatalf("空片段不应触发截断: %q truncated=%v", text, truncated)
	}
}

func TestAssemblePromptsPrioritizesEvent(t *testing.T) {
	rt := ai.Runtime{
		AIID:     7,
		Nickname: "写作助手",
		Profile:  ai.Profile{Personality: "冷静", Topics: "Go、分布式系统"},
		Behavior: ai.BehaviorSettings{AllowOriginalPost: true, CooldownSeconds: 900, MaxReplyDepth: 2},
	}
	prompt := promptInput{
		TriggerType: triggerMention,
		Event:       "【当前事件】你被提及了",
		Candidates: []candidatePost{
			{ID: 1, AuthorName: "张三", Username: "zhangsan", Summary: "一条公开内容"},
		},
		Recent: []ai.Memory{{ID: 1, MemoryType: "recent", Category: "event", Importance: 3, Content: "上次聊过 Go"}},
	}
	system, user := assemblePrompts(rt, prompt)
	if !strings.Contains(system, "写作助手") || !strings.Contains(system, "noop") {
		t.Fatalf("系统提示应包含身份与 noop 要求: %q", system)
	}
	eventIndex := strings.Index(user, "被提及")
	memoryIndex := strings.Index(user, "上次聊过")
	candidateIndex := strings.Index(user, "一条公开内容")
	if eventIndex < 0 || memoryIndex < 0 || candidateIndex < 0 {
		t.Fatalf("用户提示应包含事件、记忆与候选: %q", user)
	}
	if !(eventIndex < memoryIndex && memoryIndex < candidateIndex) {
		t.Fatal("用户提示应按 事件 > 记忆 > 候选 的顺序排列")
	}
}

func TestKeywordTerms(t *testing.T) {
	terms := keywordTerms("今天和 @zhangsan 讨论了 PostgreSQL 的索引优化问题。", 8)
	if len(terms) == 0 {
		t.Fatal("应提取到关键词")
	}
	found := false
	for _, term := range terms {
		if term == "postgresql" {
			found = true
		}
	}
	if !found {
		t.Fatalf("ASCII 词应被规范化为小写: %v", terms)
	}
	limited := keywordTerms("内容内容内容内容内容内容", 2)
	if len(limited) > 2 {
		t.Fatalf("关键词数量应受限制: %v", limited)
	}
}

// ── 幂等键与时间桶 ──

func TestScheduleKeyBucketStability(t *testing.T) {
	base := time.Date(2026, 10, 17, 10, 0, 0, 0, time.Local)
	sameBucket := base.Add(90 * time.Minute)
	nextBucket := base.Add(370 * time.Minute)

	first := scheduleKey(42, actionOriginalPost, base, 360)
	if again := scheduleKey(42, actionOriginalPost, sameBucket, 360); again != first {
		t.Fatalf("同一时间桶的幂等键应稳定: %s != %s", first, again)
	}
	if other := scheduleKey(42, actionOriginalPost, nextBucket, 360); other == first {
		t.Fatal("跨时间桶的幂等键应不同")
	}
	if other := scheduleKey(43, actionOriginalPost, base, 360); other == first {
		t.Fatal("不同 AI 的幂等键应不同")
	}
	if !strings.HasPrefix(first, "schedule:42:original_post:") {
		t.Fatalf("幂等键前缀不符合约定: %s", first)
	}
}

func TestTriggerKeys(t *testing.T) {
	if got := mentionKey(1001, 77); got != "mention:1001:77" {
		t.Fatalf("提及幂等键不符合约定: %s", got)
	}
	if got := commentTriggerKey(2002, 77); got != "comment-trigger:2002:77" {
		t.Fatalf("评论触发幂等键不符合约定: %s", got)
	}
	at := time.Date(2026, 10, 17, 23, 59, 0, 0, time.Local)
	sameHour := at.Add(30 * time.Second)
	nextHour := at.Add(time.Hour)
	if consolidationKey(5, at) != consolidationKey(5, sameHour) {
		t.Fatal("同一小时的沉淀作业键应稳定")
	}
	if consolidationKey(5, at) == consolidationKey(5, nextHour) {
		t.Fatal("跨小时的沉淀作业键应不同")
	}
}

func TestScheduleMinutes(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		key      string
		fallback int
		want     int
	}{
		{"缺失取默认值", `{}`, "post_interval_minutes", 360, 360},
		{"空值取默认值", ``, "post_interval_minutes", 360, 360},
		{"数字配置", `{"post_interval_minutes":120}`, "post_interval_minutes", 360, 120},
		{"字符串配置", `{"post_interval_minutes":"90"}`, "post_interval_minutes", 360, 90},
		{"0 表示关闭", `{"post_interval_minutes":0}`, "post_interval_minutes", 360, 0},
		{"非法取值回退默认", `{"post_interval_minutes":"abc"}`, "post_interval_minutes", 360, 360},
		{"负数回退默认", `{"post_interval_minutes":-5}`, "post_interval_minutes", 360, 360},
		{"非法 JSON 回退默认", `not-json`, "post_interval_minutes", 360, 360},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if got := scheduleMinutes(json.RawMessage(item.raw), item.key, item.fallback); got != item.want {
				t.Fatalf("期望 %d，实际 %d", item.want, got)
			}
		})
	}
}

func TestScheduleBucket(t *testing.T) {
	base := time.Date(2026, 10, 17, 10, 0, 0, 0, time.Local)
	if scheduleBucket(base, 360) != scheduleBucket(base.Add(59*time.Minute), 360) {
		t.Fatal("同一 6 小时桶内应得到相同桶号")
	}
	if scheduleBucket(base, 360) == scheduleBucket(base.Add(6*time.Hour), 360) {
		t.Fatal("跨桶应得到不同桶号")
	}
}

// ── 记忆候选规范化 ──

func TestNormalizeMemoryDraft(t *testing.T) {
	candidate, err := normalizeMemoryDraft(MemoryDraft{
		Content:        "  AI 关注 Go 与分布式系统  ",
		Category:       "unknown",
		Keywords:       []string{" Go ", "go", "", strings.Repeat("超长", 20)},
		Importance:     9,
		InferenceLevel: "guess",
	})
	if err != nil {
		t.Fatalf("归一化不应报错: %v", err)
	}
	if candidate.Content != "AI 关注 Go 与分布式系统" {
		t.Fatalf("正文应去除首尾空白: %q", candidate.Content)
	}
	if candidate.Category != "event" {
		t.Fatalf("未知分类应回退为 event: %s", candidate.Category)
	}
	if candidate.Importance != 3 {
		t.Fatalf("越界重要性应回退为 3: %d", candidate.Importance)
	}
	if candidate.InferenceLevel != "fact" {
		t.Fatalf("未知推断级别应回退为 fact: %s", candidate.InferenceLevel)
	}
	if len(candidate.Keywords) != 1 || candidate.Keywords[0] != "Go" {
		t.Fatalf("关键词应去重并丢弃超长项: %v", candidate.Keywords)
	}
}

func TestNormalizeMemoryDraftKeywordLimit(t *testing.T) {
	keywords := make([]string, 0, 30)
	for index := 0; index < 30; index++ {
		keywords = append(keywords, fmt.Sprintf("kw%02d", index))
	}
	candidate, err := normalizeMemoryDraft(MemoryDraft{Content: "正文", Keywords: keywords, Importance: 4})
	if err != nil {
		t.Fatalf("归一化不应报错: %v", err)
	}
	if len(candidate.Keywords) != maxMemoryKeywords {
		t.Fatalf("关键词最多 %d 个，实际 %d", maxMemoryKeywords, len(candidate.Keywords))
	}
}

func TestNormalizeMemoryDraftEmptyContent(t *testing.T) {
	if _, err := normalizeMemoryDraft(MemoryDraft{Content: "   "}); !errors.Is(err, errEmptyMemory) {
		t.Fatalf("空正文应返回 errEmptyMemory: %v", err)
	}
}

func TestNormalizeMemoryDraftTruncation(t *testing.T) {
	long := strings.Repeat("很长", 800)
	candidate, err := normalizeMemoryDraft(MemoryDraft{Content: long})
	if err != nil {
		t.Fatalf("归一化不应报错: %v", err)
	}
	if len([]rune(candidate.Content)) != maxMemoryContentRunes {
		t.Fatalf("近期记忆正文应截断到 %d 字，实际 %d", maxMemoryContentRunes, len([]rune(candidate.Content)))
	}
	longTerm, err := normalizeLongTermDraft(LongTermMemoryDraft{Content: long})
	if err != nil {
		t.Fatalf("归一化不应报错: %v", err)
	}
	if len([]rune(longTerm.Content)) != longTermMemoryRunes {
		t.Fatalf("长期记忆正文应截断到 %d 字，实际 %d", longTermMemoryRunes, len([]rune(longTerm.Content)))
	}
}

func TestNormalizeLongTermSupersedes(t *testing.T) {
	candidate, err := normalizeLongTermDraft(LongTermMemoryDraft{
		Content:    "用户长期关注 Go 生态",
		Supersedes: []string{"11", "11", "abc", "", "12", "13", "14", "15", "16", "17"},
	})
	if err != nil {
		t.Fatalf("归一化不应报错: %v", err)
	}
	if len(candidate.Supersedes) != 5 {
		t.Fatalf("被替代记忆最多 5 条，实际 %v", candidate.Supersedes)
	}
	if candidate.Supersedes[0] != 11 || candidate.Supersedes[1] != 12 {
		t.Fatalf("非法与重复 ID 应被过滤并保序: %v", candidate.Supersedes)
	}
}

// ── 其他纯逻辑 ──

func TestTruncateBytesKeepsUTF8(t *testing.T) {
	value := strings.Repeat("中", 10) // 每个汉字 3 字节
	cut, truncated := truncateBytes(value, 10)
	if !truncated {
		t.Fatal("超长内容应标记截断")
	}
	if !isValidUTF8(cut) {
		t.Fatalf("截断结果必须是合法 UTF-8: %q", cut)
	}
	if len(cut) > 10 {
		t.Fatalf("截断结果不应超过字节上限: %d", len(cut))
	}
	if short, truncated := truncateBytes("短内容", 64); truncated || short != "短内容" {
		t.Fatal("未超长时不应改写内容")
	}
}

func TestParseScopeParams(t *testing.T) {
	params := parseScopeParams(json.RawMessage(`{"tags":["go","时间线"],"usernames":["alice"]}`))
	if len(params.Tags) != 2 || len(params.Usernames) != 1 {
		t.Fatalf("候选范围参数解析失败: %+v", params)
	}
	if empty := parseScopeParams(json.RawMessage(`not-json`)); len(empty.Tags) != 0 {
		t.Fatal("非法参数应退化为空")
	}
	if validCandidateScope(scopeNone) || !validCandidateScope(scopeTags) {
		t.Fatal("候选范围可用性判断不符合预期")
	}
}

func TestNormalizeTermsAndTargetRef(t *testing.T) {
	terms := normalizeTerms([]string{" go ", "GO", "", "rust"}, 3)
	if len(terms) != 2 || terms[0] != "go" || terms[1] != "rust" {
		t.Fatalf("术语去重结果不符合预期: %v", terms)
	}
	post := decisionTarget{Type: targetPost, ID: 9}
	if ref := targetRefID(post); ref == nil || *ref != 9 {
		t.Fatal("帖子目标应参与冷却判断")
	}
	user := decisionTarget{Type: targetUser, ID: 9}
	if ref := targetRefID(user); ref != nil {
		t.Fatal("用户目标不参与目标级冷却")
	}
}

func TestFormatErrorSummary(t *testing.T) {
	summary := formatErrorSummary(errorTypeInvalidJSON, "解析失败")
	if !strings.HasPrefix(summary, errorTypeInvalidJSON+": ") {
		t.Fatalf("错误摘要应带分类前缀: %s", summary)
	}
	if summary := formatErrorSummary("", "只有信息"); summary != "只有信息" {
		t.Fatalf("无分类时应只保留信息: %s", summary)
	}
	long := formatErrorSummary("x", strings.Repeat("长", 800))
	if len([]rune(long)) > 510 {
		t.Fatalf("错误摘要应截断: %d", len([]rune(long)))
	}
}

func TestTaskStatusFlow(t *testing.T) {
	for _, status := range []string{statusSucceeded, statusNoop, statusRejected, statusCanceled, statusRunning} {
		if isRetryableTaskStatus(status) {
			t.Fatalf("状态 %s 不应再次执行", status)
		}
	}
	for _, status := range []string{statusPending, statusFailed} {
		if !isRetryableTaskStatus(status) {
			t.Fatalf("状态 %s 应允许执行", status)
		}
	}
	if retryPending(maxTaskAttempts, maxTaskAttempts) {
		t.Fatal("达到最大尝试次数后不应继续重试")
	}
}

func TestModelConstantsAlignment(t *testing.T) {
	// 本包只在公开且已发布的帖子上产生动作，常量必须与 model 包一致。
	if model.VisibilityPublic != "public" || model.PostStatusPublished != "published" {
		t.Fatal("可见性与状态常量必须与 model 包保持一致")
	}
}

func TestTaskActionAllowed(t *testing.T) {
	manual := aiTask{TriggerType: triggerManual}
	if !taskActionAllowed(manual, actionOriginalPost) || !taskActionAllowed(manual, actionQuote) {
		t.Fatal("生成类任务允许产出原创帖与引用帖")
	}
	if !taskActionAllowed(manual, actionNoop) {
		t.Fatal("生成类任务必须允许 noop")
	}
	if taskActionAllowed(manual, actionLike) {
		t.Fatal("管理端生成任务不应产生点赞等互动动作")
	}
	schedule := aiTask{TriggerType: triggerSchedule}
	if taskActionAllowed(schedule, actionComment) {
		t.Fatal("定时原创任务不应产生评论")
	}
	proactive := aiTask{TriggerType: triggerProactive}
	for _, action := range []string{actionLike, actionFollow, actionRepost, actionComment, actionNoop} {
		if !taskActionAllowed(proactive, action) {
			t.Fatalf("主动互动任务不应限制动作 %s", action)
		}
	}
}

func TestParseTargetID(t *testing.T) {
	id, err := parseTargetID(" 123456789012345678 ")
	if err != nil || id != 123456789012345678 {
		t.Fatalf("合法 ID 解析失败: id=%d err=%v", id, err)
	}
	for _, raw := range []string{"", "   ", "abc", "-1"} {
		if _, err := parseTargetID(raw); err == nil {
			t.Fatalf("非法目标 ID 应报错: %q", raw)
		}
	}
}

// isValidUTF8 判断字符串是否是合法 UTF-8。
func isValidUTF8(value string) bool {
	return utf8.ValidString(value)
}

// 被动回复与主动评论是独立开关：被提及/收到评论时发表的评论属于被动回复，
// 不能要求 AI 同时开启主动评论。
func TestActionAllowedForTrigger(t *testing.T) {
	passiveOnly := ai.BehaviorSettings{AllowPassiveReply: true, AllowActiveComment: false}
	activeOnly := ai.BehaviorSettings{AllowPassiveReply: false, AllowActiveComment: true}

	cases := []struct {
		name     string
		behavior ai.BehaviorSettings
		trigger  string
		action   string
		want     bool
	}{
		{"被提及且只开了被动回复", passiveOnly, triggerMention, actionComment, true},
		{"收到评论且只开了被动回复", passiveOnly, triggerComment, actionComment, true},
		{"收到评论回复且只开了被动回复", passiveOnly, triggerComment, actionReply, true},
		{"主动浏览且只开了被动回复", passiveOnly, triggerProactive, actionComment, false},
		{"主动浏览且只开了主动评论", activeOnly, triggerProactive, actionComment, true},
		{"被提及但只开了主动评论", activeOnly, triggerMention, actionComment, false},
		{"定时原创不受评论开关影响", passiveOnly, triggerSchedule, actionOriginalPost, false},
		{"noop 永远允许", passiveOnly, triggerMention, actionNoop, true},
	}
	for _, tc := range cases {
		if got := actionAllowedForTrigger(tc.behavior, tc.trigger, tc.action); got != tc.want {
			t.Errorf("%s: got=%v want=%v", tc.name, got, tc.want)
		}
	}
}
