package domain

import (
	"strconv"
	"strings"
	"time"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/format"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// CanPublish 判断当前状态是否允许推进为已发布；已发布返回 false（调用方按幂等处理）。
func CanPublish(status string) bool {
	return status == model.PostStatusDraft || status == model.PostStatusScheduled
}

// CanSetSchedule 判断当前状态是否允许设置/修改定时发布。
func CanSetSchedule(status string) bool {
	return status == model.PostStatusDraft || status == model.PostStatusScheduled
}

// CanCancelSchedule 判断当前状态是否允许取消定时发布（已发布、已删除不允许）。
func CanCancelSchedule(status string) bool {
	return status == model.PostStatusDraft || status == model.PostStatusScheduled
}

// MaxPinnedPerAuthor 是每位作者个人主页置顶帖上限（0022，与数据库 CHECK 约束一致）。
const MaxPinnedPerAuthor = 3

// CanPin 判断当前状态是否允许置顶/取消置顶（仅已发布帖子；删除帖按不存在处理）。
func CanPin(status string) bool {
	return status == model.PostStatusPublished
}

// Editable 判断内容是否还有可编辑的草稿工作区（未发布且未删除）。
func Editable(status string) bool {
	return status == model.PostStatusDraft || status == model.PostStatusScheduled
}

// IsDeleted 判断内容是否已逻辑删除。
func IsDeleted(status string) bool { return status == model.PostStatusDeleted }

// NextVersionNumber 计算追加版本后的版本号。
func NextVersionNumber(maxVersion int) int {
	if maxVersion < 0 {
		maxVersion = 0
	}
	return maxVersion + 1
}

// CheckPublishState 把「不可发布状态」映射为领域错误；已发布由调用方按幂等处理。
func CheckPublishState(status string) error {
	if CanPublish(status) {
		return nil
	}
	return apperr.Conflict("invalid_state", "当前状态不能发布")
}

// ValidateScheduledAt 校验定时发布时间至少晚于当前时间 1 分钟（纯函数，便于单元测试）。
func ValidateScheduledAt(at, now time.Time) error {
	if at.Before(now.Add(MinScheduleLead)) {
		return apperr.Validation("定时发布时间过早").
			WithDetails(map[string]any{"scheduled_at": "发布时间需要晚于当前时间至少 1 分钟"})
	}
	return nil
}

// ValidateFutureScheduledAt 校验 Actor 创建路径的定时发布时间必须晚于当前时间。
// AI 生成任务从接收请求到真正执行之间可能已经过了一段时间，因此这里只要求“未来”，
// 不套用用户接口“至少 1 分钟”的输入提示；已到期的时间会由作业运行器立即执行。
func ValidateFutureScheduledAt(at, now time.Time) error {
	if !at.After(now) {
		return apperr.Validation("定时发布时间必须晚于当前时间").
			WithDetails(map[string]any{"scheduled_at": "发布时间需要晚于当前时间"})
	}
	return nil
}

// ParseScheduledAt 解析定时发布时间：必须是 RFC3339，且晚于当前时间至少 1 分钟。
func ParseScheduledAt(raw string, now time.Time) (time.Time, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return time.Time{}, apperr.Validation("请填写定时发布时间").
			WithDetails(map[string]any{"scheduled_at": "定时发布需要填写发布时间"})
	}
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, apperr.Validation("定时发布时间格式无效").
			WithDetails(map[string]any{"scheduled_at": "时间需为 RFC3339 格式，例如 2026-10-17T20:30:00+08:00"})
	}
	if err := ValidateScheduledAt(at, now); err != nil {
		return time.Time{}, err
	}
	return at, nil
}

// SameScheduleTime 比较作业计划时间与帖子当前定时时间。
// 精度只取到秒：作业幂等键同样按秒生成，避免亚秒精度差异误判为“已变更”。
func SameScheduleTime(jobRunAt, scheduledAt time.Time) bool {
	return jobRunAt.Unix() == scheduledAt.Unix()
}

// PublishJobKey 是定时发布作业的幂等键。
func PublishJobKey(postID int64, at time.Time) string {
	return "post_publish:" + format.FormatID(postID) + ":" + strconv.FormatInt(at.Unix(), 10)
}

// PublishStateFor 把帖子状态与最新一条定时发布作业状态合并为列表展示状态。
// 最新作业处于 failed / paused 时以作业状态为准，其余情况沿用 posts.status；
// publishFailed 只表示发布失败（暂停由 status=paused 单独展示）。
func PublishStateFor(postStatus, jobStatus, jobError string) (status string, publishFailed bool, publishError string) {
	switch jobStatus {
	case "failed":
		return "failed", true, jobError
	case "paused":
		return "paused", false, ""
	default:
		return postStatus, false, ""
	}
}

// CounterColumn 把帖子聚合计数器名映射为 posts 表列名。
func CounterColumn(counter string) (string, error) {
	switch counter {
	case "like":
		return "like_count", nil
	case "comment":
		return "comment_count", nil
	case "repost":
		return "repost_count", nil
	default:
		return "", apperr.Validation("计数器类型无效")
	}
}

// ValidateCounterDelta 校验计数器增减幅度。
func ValidateCounterDelta(delta int) error {
	if delta == 0 {
		return apperr.Validation("计数器增减幅度不能为 0")
	}
	return nil
}

// ValidatePinnedReorder 校验置顶排序请求：列表非空、不超过上限、ID 有效且无重复
// （纯函数，便于单元测试；与当前置顶集合的一致性比对在 app 层完成）。
func ValidatePinnedReorder(ids []int64) error {
	if len(ids) == 0 {
		return apperr.Validation("请提供置顶帖列表").
			WithDetails(map[string]any{"post_ids": "置顶帖列表不能为空"})
	}
	if len(ids) > MaxPinnedPerAuthor {
		return apperr.Validation("置顶帖数量超出上限").
			WithDetails(map[string]any{"post_ids": "最多置顶 3 条帖子"})
	}
	seen := make(map[int64]bool, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return apperr.Validation("帖子 ID 无效").
				WithDetails(map[string]any{"post_ids": "帖子 ID 必须为正整数"})
		}
		if seen[id] {
			return apperr.Validation("置顶帖列表存在重复").
				WithDetails(map[string]any{"post_ids": "帖子 ID 不能重复"})
		}
		seen[id] = true
	}
	return nil
}

// SameIDSet 判断两个 ID 列表作为多重集合是否完全一致（元素相同、次数相同），
// 不比较顺序。用于校验置顶排序请求覆盖了当前全部置顶帖且未混入其他帖子。
func SameIDSet(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[int64]int, len(a)+len(b))
	for _, id := range a {
		counts[id]++
	}
	for _, id := range b {
		counts[id]--
		if counts[id] < 0 {
			return false
		}
	}
	for _, n := range counts {
		if n != 0 {
			return false
		}
	}
	return true
}
