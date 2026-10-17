// Package domain 实现 content 模块的纯领域规则：帖子状态机、可见性/定时/版本约束、
// 正文渲染与参数规范化。不依赖数据库、HTTP 与其它模块；错误使用 platform/apperr。
package domain

import (
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/access"
	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/shared/richtext"
)

// 时间线条目类型。
const (
	KindPost   = "post"
	KindRepost = "repost"
)

// 创建与更新帖子的动作。
const (
	ActionDraft    = "draft"
	ActionPublish  = "publish"
	ActionSchedule = "schedule"
)

// 时间线内容筛选。
const (
	FilterAll   = "all"
	FilterHuman = "human"
	FilterAI    = "ai"
)

// 个人主页时间线页签。
const (
	TabPosts   = "posts"
	TabReplies = "replies"
	TabReposts = "reposts"
)

// 作业与附件的固定取值。
const (
	RefTypePost           = "post"
	AttachmentPurposePost = "post"
)

const (
	// MinScheduleLead 定时发布时间至少晚于当前时间 1 分钟。
	MinScheduleLead = time.Minute

	// 版本摘要与搜索关键词的长度上限（按字符计）。
	SummaryRunes    = 120
	MaxSearchRunes  = 100
	MaxTagRunes     = 50
	MaxSuggestCount = 10

	// MaxPageBatches 是「过滤后不足一页」时最多连续拉取的批次数，避免死循环。
	MaxPageBatches = 5

	// 发帖与编辑限频默认值：每用户 30 次 / 10 分钟（可被系统设置覆盖）。
	DefaultPostRateLimit  = 30
	DefaultPostRateWindow = 10 * time.Minute
)

// Content 是校验、渲染并校验引用/附件之后可直接写入的内容。
type Content struct {
	Format        string
	Source        string
	HTML          string
	Plain         string
	Visibility    string
	AttachmentIDs []int64
	QuotedPostID  *int64
}

// RenderContent 校验内容格式并把原始正文渲染为安全 HTML 与纯文本。
// 正文为空按需求返回字段级校验错误。
func RenderContent(format, source string) (safeHTML, plain string, err error) {
	normalizedFormat, err := NormalizeFormat(format)
	if err != nil {
		return "", "", err
	}
	safeHTML, plain = richtext.Render(normalizedFormat, source)
	if strings.TrimSpace(plain) == "" {
		return "", "", apperr.Validation("正文不能为空").
			WithDetails(map[string]any{"content_source": "正文不能为空"})
	}
	return safeHTML, plain, nil
}

// NormalizeFormat 校验内容格式。
func NormalizeFormat(raw string) (string, error) {
	format := strings.ToLower(strings.TrimSpace(raw))
	if !richtext.ValidFormat(format) {
		return "", apperr.Validation("内容格式无效").
			WithDetails(map[string]any{"content_format": "内容格式只能是 richtext 或 markdown"})
	}
	return format, nil
}

// NormalizeVisibility 校验可见性，空值按需求默认公开。
func NormalizeVisibility(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", model.VisibilityPublic:
		return model.VisibilityPublic, nil
	case model.VisibilityFollowers:
		return model.VisibilityFollowers, nil
	case model.VisibilityPrivate:
		return model.VisibilityPrivate, nil
	default:
		return "", apperr.Validation("可见性设置无效").
			WithDetails(map[string]any{"visibility": "可见性只能是 public、followers 或 private"})
	}
}

// CheckVisibilityPolicy 校验作者账号允许的可见性：AI 内容必须公开（需求 13.4）。
func CheckVisibilityPolicy(authorIsAI bool, visibility string) error {
	if authorIsAI && visibility != model.VisibilityPublic {
		return apperr.Validation("AI 内容必须公开").
			WithDetails(map[string]any{"visibility": "AI 账号发布的内容只能是 public"})
	}
	return nil
}

// NormalizeAction 校验创建帖子的动作。
func NormalizeAction(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case ActionDraft:
		return ActionDraft, nil
	case ActionPublish:
		return ActionPublish, nil
	case ActionSchedule:
		return ActionSchedule, nil
	default:
		return "", apperr.Validation("缺少或无效的操作类型").
			WithDetails(map[string]any{"action": "action 只能是 draft、publish 或 schedule"})
	}
}

// NormalizeFilter 校验时间线内容筛选。
func NormalizeFilter(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", FilterAll:
		return FilterAll, nil
	case FilterHuman:
		return FilterHuman, nil
	case FilterAI:
		return FilterAI, nil
	default:
		return "", apperr.Invalid("filter 参数无效")
	}
}

// NormalizeTab 校验个人主页时间线页签，缺省为 posts。
func NormalizeTab(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", TabPosts:
		return TabPosts, nil
	case TabReplies:
		return TabReplies, nil
	case TabReposts:
		return TabReposts, nil
	default:
		return "", apperr.Invalid("tab 参数无效")
	}
}

// NormalizeSearchKeyword 校验搜索关键词长度。
func NormalizeSearchKeyword(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", apperr.Validation("请输入搜索关键词").
			WithDetails(map[string]any{"q": "搜索关键词不能为空"})
	}
	if utf8.RuneCountInString(value) > MaxSearchRunes {
		return "", apperr.Validation("搜索关键词过长").
			WithDetails(map[string]any{"q": "搜索关键词不能超过 100 个字符"})
	}
	return value, nil
}

// NormalizeTagName 规范化标签名（小写、去空白），空标签或超长返回空字符串表示不查询。
func NormalizeTagName(raw string) string {
	name := strings.ToLower(strings.TrimSpace(raw))
	if name == "" || utf8.RuneCountInString(name) > MaxTagRunes {
		return ""
	}
	return name
}

// Summary 截取纯文本摘要，按字符数截断且不破坏多字节字符。
func Summary(plain string) string {
	value := strings.TrimSpace(plain)
	if utf8.RuneCountInString(value) <= SummaryRunes {
		return value
	}
	return string([]rune(value)[:SummaryRunes])
}

// ParseAttachmentIDs 解析并去重附件 ID 参数。
func ParseAttachmentIDs(raw []string) ([]int64, error) {
	ids := make([]int64, 0, len(raw))
	seen := make(map[int64]bool, len(raw))
	for _, value := range raw {
		id, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil || id <= 0 {
			return nil, apperr.Validation("附件参数无效").
				WithDetails(map[string]any{"attachment_ids": "附件 ID 无效"})
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids, nil
}

// ParseQuotedPostID 解析引用转发的原帖 ID，未填写时为 nil。
func ParseQuotedPostID(raw *string) (*int64, error) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil, nil
	}
	id, err := strconv.ParseInt(strings.TrimSpace(*raw), 10, 64)
	if err != nil || id <= 0 {
		return nil, apperr.Validation("引用内容无效").
			WithDetails(map[string]any{"quoted_post_id": "引用的帖子 ID 无效"})
	}
	return &id, nil
}

// UniqueIDs 去重并保序，忽略非正数。
func UniqueIDs(values []int64) []int64 {
	out := make([]int64, 0, len(values))
	seen := make(map[int64]bool, len(values))
	for _, value := range values {
		if value <= 0 || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

// SortedIDs 把关注集合转为升序切片，保证 SQL 参数稳定且可比较。
func SortedIDs(set map[int64]bool) []int64 {
	out := make([]int64, 0, len(set))
	for id, active := range set {
		if active {
			out = append(out, id)
		}
	}
	// 小型集合直接插入排序，避免为纯策略引入额外依赖顺序的假设。
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// CanViewPublished 判断访问者能否看到一条已发布内容（标签聚合/搜索/回复时间线共用）。
func CanViewPublished(viewer model.Viewer, post model.Post, following map[int64]bool) bool {
	return post.Status == model.PostStatusPublished &&
		access.CanViewPostWithFollowing(viewer, post, following)
}
