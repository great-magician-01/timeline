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
	AuthorID     int64
	FollowingIDs []int64
	// AllowedIDs 非 nil 时只保留这些用户（human/ai 过滤）；空切片表示过滤后无结果。
	AllowedIDs []int64
	AfterAt    time.Time
	AfterID    int64
	Limit      int
}
