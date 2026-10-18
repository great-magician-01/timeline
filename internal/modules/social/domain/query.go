package domain

import "time"

// FollowRow 是关注关系列表行：RowID 用于游标，UserID 是被展示账号。
// 定义在 domain 使 repo 无需 import app（保持 repo → domain 的依赖方向）。
type FollowRow struct {
	RowID  int64
	UserID int64
}

// RequestRow 是关注申请行。
type RequestRow struct {
	ID          int64
	RequesterID int64
	TargetID    int64
	Status      string
	CreatedAt   time.Time
	ResolvedAt  *time.Time
}

// RepostEvent 是纯转发事件行（供 content 合并时间线）。
type RepostEvent struct {
	ID        int64
	UserID    int64
	PostID    int64
	CreatedAt time.Time
}

// RepostEventQuery 是纯转发事件查询条件（由 app 从 contracts 输入映射）。
type RepostEventQuery struct {
	AuthorID int64
	// FollowingIDs 是关注时间线允许的转发者集合；仅在 FollowingOnly=true 时生效。
	FollowingIDs []int64
	// FollowingOnly 为 true 时必须显式按 FollowingIDs 过滤：空集合表示无匹配用户，
	// 不能退化为不过滤（关注时间线的空关注集合必须返回空结果）。
	FollowingOnly bool
	// AuthorIsAI 非 nil 时按转发者账号类型过滤（false=仅人类，true=仅 AI）；
	// 直接命中 reposts.author_is_ai 快照列与 reposts_author_type_timeline 部分索引，
	// 不再先查询全量账号 ID 再以无界数组下传。
	AuthorIsAI *bool
	AfterAt    time.Time
	AfterID    int64
	Limit      int
}
