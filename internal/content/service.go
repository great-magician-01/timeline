// Package content 实现帖子、草稿与自动保存、定时发布、编辑历史、标签、搜索与时间线。
// 所有多表写入都在同一事务内完成（数据库不使用外键，引用完整性由应用层校验），
// 删除一律逻辑删除，查询与时间比较全部在 SQL 内完成（docs/WORKFLOWS.md §3、
// docs/DATA_MODEL.md 6.3-6.8、docs/API.md §6、§7）。
package content

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/internal/access"
	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/auth"
	"github.com/great-magician-01/timeline/internal/jobs"
	"github.com/great-magician-01/timeline/internal/model"
	"github.com/great-magician-01/timeline/internal/ratelimit"
	"github.com/great-magician-01/timeline/internal/richtext"
	"github.com/great-magician-01/timeline/internal/settings"
)

// PoolProvider 与 auth、jobs 等包保持同形，便于主代理统一装配。
type PoolProvider interface {
	Pool() *pgxpool.Pool
}

// IDGenerator 与应用侧雪花 ID 生成器同形。
type IDGenerator interface {
	Next() (int64, error)
}

// 时间线条目类型。
const (
	kindPost   = "post"
	kindRepost = "repost"
)

// 创建与更新帖子的动作。
const (
	actionDraft    = "draft"
	actionPublish  = "publish"
	actionSchedule = "schedule"
)

// 时间线内容筛选。
const (
	filterAll   = "all"
	filterHuman = "human"
	filterAI    = "ai"
)

// 个人主页时间线页签。
const (
	tabPosts   = "posts"
	tabReplies = "replies"
	tabReposts = "reposts"
)

const (
	// 发帖与编辑限频：每用户 30 次 / 10 分钟（docs/API.md §13）。
	postRateLimit  = 30
	postRateWindow = 10 * time.Minute

	// 定时发布时间至少晚于当前时间 1 分钟。
	minScheduleLead = time.Minute

	// 版本摘要与搜索关键词的长度上限（按字符计）。
	summaryRunes    = 120
	maxSearchRunes  = 100
	maxTagRunes     = 50
	maxSuggestCount = 10

	// 一页最多分几批取候选，避免可见性过滤后为空时的死循环。
	maxPageBatches = 5
)

// Service 承载帖子、草稿、版本与时间线相关的全部读写。
type Service struct {
	pools PoolProvider
	ids   IDGenerator
	log   *slog.Logger
	jobs  *jobs.Runner

	postLimiter *ratelimit.Limiter
}

// NewService 构造内容服务；runner 可为 nil（不启用定时发布入队）。
func NewService(pools PoolProvider, ids IDGenerator, log *slog.Logger, runner *jobs.Runner) *Service {
	return &Service{
		pools:       pools,
		ids:         ids,
		log:         log,
		jobs:        runner,
		postLimiter: ratelimit.New(postRateLimit, postRateWindow),
	}
}

func (s *Service) pool(c *gin.Context) (*pgxpool.Pool, bool) {
	pool := s.pools.Pool()
	if pool == nil {
		api.Error(c, http.StatusServiceUnavailable, "service_unavailable", "服务尚未就绪，请稍后重试")
		return nil, false
	}
	return pool, true
}

func (s *Service) internalError(c *gin.Context, message string, err error) {
	s.log.Error(message, "err", err, "path", c.Request.URL.Path)
	api.Error(c, http.StatusInternalServerError, "internal_error", "服务暂时异常，请稍后重试")
}

// respond 统一把业务错误写回响应；不是业务错误时按内部错误处理。
func (s *Service) respond(c *gin.Context, message string, err error) {
	if !api.Respond(c, err) {
		s.internalError(c, message, err)
	}
}

// viewerOf 取当前访问者身份，游客返回 GuestViewer。
func viewerOf(c *gin.Context) model.Viewer {
	if u, ok := auth.CurrentUser(c); ok {
		return model.ViewerFrom(u)
	}
	return model.GuestViewer()
}

// consumePostQuota 消耗一次发帖/编辑额度，超限时按 429 写回响应。
// 限频阈值与窗口来自系统设置，每次调用即时读取，管理后台修改后立即生效。
func (s *Service) consumePostQuota(c *gin.Context, userID int64) bool {
	pool := s.pools.Pool()
	if pool == nil {
		api.Error(c, http.StatusServiceUnavailable, "service_unavailable", "服务尚未就绪，请稍后重试")
		return false
	}
	values := settings.LoadOrDefaults(c.Request.Context(), pool)
	allowed, retryAfter := s.postLimiter.ConsumeLimit(
		"user:"+api.FormatID(userID), values.PostRateLimit, values.PostRateWindow)
	if allowed {
		return true
	}
	seconds := int(math.Ceil(retryAfter.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	c.Header("Retry-After", strconv.Itoa(seconds))
	if !api.Respond(c, api.ErrRateLimited()) {
		s.internalError(c, "发帖限频响应失败", fmt.Errorf("rate limited"))
	}
	return false
}

// pathID 解析路径参数中的雪花 ID。
func pathID(c *gin.Context) (int64, bool) {
	id, err := api.ParseID(c.Param("id"))
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", "资源 ID 无效")
		return 0, false
	}
	return id, true
}

// invalidInputError 与认证接口保持同一文案，避免泄露解析细节。
func invalidInputError(_ error) string {
	return "请求体必须是有效的 JSON 对象，且不能包含未知字段"
}

// pageCursor 是带时间戳的分页游标。
// 时间线按 (sort_at, item_id) 排序，只带 ID 会在定时发布后错序，因此游标必须包含时间。
type pageCursor struct {
	Time time.Time `json:"t"`
	ID   int64     `json:"i"`
}

// encodePageCursor 生成不可解析、不保证稳定的时间游标。
func encodePageCursor(t time.Time, id int64) string {
	payload, err := json.Marshal(pageCursor{Time: t.UTC(), ID: id})
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

// decodePageCursor 解析时间游标；空字符串表示首页。
func decodePageCursor(raw string) (time.Time, int64, error) {
	if strings.TrimSpace(raw) == "" {
		return time.Time{}, 0, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return time.Time{}, 0, errInvalidCursor
	}
	var cursor pageCursor
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cursor); err != nil || cursor.ID <= 0 || cursor.Time.IsZero() {
		return time.Time{}, 0, errInvalidCursor
	}
	return cursor.Time, cursor.ID, nil
}

// errInvalidCursor 由调用方映射为 400 invalid_request。
var errInvalidCursor = fmt.Errorf("无效的游标")

// pageQueryCursor 读取请求中的 cursor 参数。
func pageQueryCursor(c *gin.Context) (time.Time, int64, error) {
	return decodePageCursor(c.Query("cursor"))
}

// timeListResponse 组装时间游标列表响应；没有下一页时一律不返回游标。
func timeListResponse(items any, lastTime time.Time, lastID int64, hasMore bool) gin.H {
	var nextCursor any
	if hasMore && lastID > 0 && !lastTime.IsZero() {
		nextCursor = encodePageCursor(lastTime, lastID)
	} else {
		hasMore = false
	}
	return gin.H{"items": items, "next_cursor": nextCursor, "has_more": hasMore}
}

// pageFetchFunc 从 (afterTime, afterID) 之后按时间倒序拉取一批候选。
type pageFetchFunc[T any] func(ctx context.Context, afterTime time.Time, afterID int64, limit int) ([]T, error)

// collectPage 按“多取 limit+1 条 → 过滤 → 判断是否还有更多”的策略组装一页。
// 过滤后不足 limit 且仍有候选时继续分批获取，最多 maxPageBatches 轮，避免死循环。
func collectPage[T any](
	ctx context.Context,
	fetch pageFetchFunc[T],
	key func(T) (time.Time, int64),
	cursorTime time.Time,
	cursorID int64,
	limit int,
	keep func(T) bool,
) ([]T, bool, error) {
	collected := make([]T, 0, limit)
	exhausted := false
	afterTime, afterID := cursorTime, cursorID
	for round := 0; round < maxPageBatches; round++ {
		batch, err := fetch(ctx, afterTime, afterID, limit+1)
		if err != nil {
			return nil, false, err
		}
		if len(batch) == 0 {
			exhausted = true
			break
		}
		hasCandidate := len(batch) > limit
		if hasCandidate {
			batch = batch[:limit]
		}
		afterTime, afterID = key(batch[len(batch)-1])
		for _, item := range batch {
			if keep == nil || keep(item) {
				collected = append(collected, item)
			}
		}
		// 本轮候选不足 limit+1 说明后端已取尽，即使正好凑满一页也不再有下一页。
		if !hasCandidate {
			exhausted = true
			break
		}
		if len(collected) >= limit {
			break
		}
	}
	hasMore := !exhausted || len(collected) > limit
	if len(collected) > limit {
		collected = collected[:limit]
	}
	return collected, hasMore, nil
}

// normalizeVisibility 校验可见性，空值按需求默认公开。
func normalizeVisibility(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", model.VisibilityPublic:
		return model.VisibilityPublic, nil
	case model.VisibilityFollowers:
		return model.VisibilityFollowers, nil
	case model.VisibilityPrivate:
		return model.VisibilityPrivate, nil
	default:
		return "", api.ErrValidation("可见性设置无效").
			WithDetails(gin.H{"visibility": "可见性只能是 public、followers 或 private"})
	}
}

// normalizeAction 校验创建帖子的动作。
func normalizeAction(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case actionDraft:
		return actionDraft, nil
	case actionPublish:
		return actionPublish, nil
	case actionSchedule:
		return actionSchedule, nil
	default:
		return "", api.ErrValidation("缺少或无效的操作类型").
			WithDetails(gin.H{"action": "action 只能是 draft、publish 或 schedule"})
	}
}

// normalizeFilter 校验时间线内容筛选。
func normalizeFilter(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", filterAll:
		return filterAll, nil
	case filterHuman:
		return filterHuman, nil
	case filterAI:
		return filterAI, nil
	default:
		return "", api.ErrInvalid("filter 参数无效")
	}
}

// normalizeTab 校验个人主页时间线页签，缺省为 posts。
func normalizeTab(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", tabPosts:
		return tabPosts, nil
	case tabReplies:
		return tabReplies, nil
	case tabReposts:
		return tabReposts, nil
	default:
		return "", api.ErrInvalid("tab 参数无效")
	}
}

// normalizeFormat 校验内容格式。
func normalizeFormat(raw string) (string, error) {
	format := strings.ToLower(strings.TrimSpace(raw))
	if !richtext.ValidFormat(format) {
		return "", api.ErrValidation("内容格式无效").
			WithDetails(gin.H{"content_format": "内容格式只能是 richtext 或 markdown"})
	}
	return format, nil
}

// validateFutureScheduledAt 校验 Actor 创建路径的定时发布时间必须晚于当前时间。
// AI 生成任务从接收请求到真正执行之间可能已经过了一段时间，因此这里只要求“未来”，
// 不套用用户接口“至少 1 分钟”的输入提示；已到期的时间会由作业运行器立即执行。
func validateFutureScheduledAt(at, now time.Time) error {
	if !at.After(now) {
		return api.ErrValidation("定时发布时间必须晚于当前时间").
			WithDetails(gin.H{"scheduled_at": "发布时间需要晚于当前时间"})
	}
	return nil
}

// validateScheduledAt 校验定时发布时间至少晚于当前时间 1 分钟（纯函数，便于单元测试）。
func validateScheduledAt(at, now time.Time) error {
	if at.Before(now.Add(minScheduleLead)) {
		return api.ErrValidation("定时发布时间过早").
			WithDetails(gin.H{"scheduled_at": "发布时间需要晚于当前时间至少 1 分钟"})
	}
	return nil
}

// parseScheduledAt 解析定时发布时间：必须是 RFC3339，且晚于当前时间至少 1 分钟。
func parseScheduledAt(raw string, now time.Time) (time.Time, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return time.Time{}, api.ErrValidation("请填写定时发布时间").
			WithDetails(gin.H{"scheduled_at": "定时发布需要填写发布时间"})
	}
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, api.ErrValidation("定时发布时间格式无效").
			WithDetails(gin.H{"scheduled_at": "时间需为 RFC3339 格式，例如 2026-10-17T20:30:00+08:00"})
	}
	if err := validateScheduledAt(at, now); err != nil {
		return time.Time{}, err
	}
	return at, nil
}

// parseAttachmentIDs 解析并去重附件 ID 参数。
func parseAttachmentIDs(raw []string) ([]int64, error) {
	ids := make([]int64, 0, len(raw))
	seen := make(map[int64]bool, len(raw))
	for _, value := range raw {
		id, err := api.ParseID(strings.TrimSpace(value))
		if err != nil {
			return nil, api.ErrValidation("附件参数无效").
				WithDetails(gin.H{"attachment_ids": "附件 ID 无效"})
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids, nil
}

// parseQuotedPostID 解析引用转发的原帖 ID，未填写时为 nil。
func parseQuotedPostID(raw *string) (*int64, error) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil, nil
	}
	id, err := api.ParseID(strings.TrimSpace(*raw))
	if err != nil {
		return nil, api.ErrValidation("引用内容无效").
			WithDetails(gin.H{"quoted_post_id": "引用的帖子 ID 无效"})
	}
	return &id, nil
}

// summary 截取纯文本摘要，按字符数截断且不破坏多字节字符。
func summary(plain string) string {
	value := strings.TrimSpace(plain)
	if utf8.RuneCountInString(value) <= summaryRunes {
		return value
	}
	runes := []rune(value)
	return string(runes[:summaryRunes])
}

// normalizeSearchKeyword 校验搜索关键词长度。
func normalizeSearchKeyword(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", api.ErrValidation("请输入搜索关键词").
			WithDetails(gin.H{"q": "搜索关键词不能为空"})
	}
	if utf8.RuneCountInString(value) > maxSearchRunes {
		return "", api.ErrValidation("搜索关键词过长").
			WithDetails(gin.H{"q": fmt.Sprintf("搜索关键词不能超过 %d 个字符", maxSearchRunes)})
	}
	return value, nil
}

// uniqueIDs 去重并保序，忽略非正数。
func uniqueIDs(values []int64) []int64 {
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

// activeDraftAttachmentIDs 读取草稿工作区中仍生效的附件（按排序）。
func activeDraftAttachmentIDs(ctx context.Context, q access.Querier, postID int64) ([]int64, error) {
	rows, err := q.Query(ctx, `
		SELECT attachment_id FROM post_draft_attachments
		WHERE post_id = $1 AND is_active
		ORDER BY sort_order, attachment_id`, postID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
