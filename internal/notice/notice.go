// Package notice 负责提及与通知：通知写入、幂等去重、列表与已读。
// 通知只记录事件引用，不复制正文；读取时按当前权限解析预览，
// 内容删除或不可见时 preview 返回 null（docs/API.md §9、docs/WORKFLOWS.md §9）。
package notice

import (
	"context"

	"github.com/great-magician-01/timeline/internal/access"
	"github.com/great-magician-01/timeline/internal/api"
)

// 通知类型（与 notifications.type 约束一致）。
const (
	TypePostLike      = "post_like"
	TypeCommentLike   = "comment_like"
	TypeComment       = "comment"
	TypeReply         = "reply"
	TypeRepost        = "repost"
	TypeQuote         = "quote"
	TypeFollow        = "follow"
	TypeFollowRequest = "follow_request"
	TypeFollowApprove = "follow_approved"
	TypeMention       = "mention"
)

// 提及来源类型。
const (
	SourcePost    = "post"
	SourceComment = "comment"
)

// IDGenerator 与应用雪花 ID 生成器同形。
type IDGenerator interface {
	Next() (int64, error)
}

// Params 描述一次通知写入。IdempotencyKey 必须唯一标识“一次事件”，
// 重试或重复投递时靠它去重（docs/WORKFLOWS.md 9.1）。
type Params struct {
	IdempotencyKey string
	RecipientID    int64
	ActorID        int64
	Type           string
	TargetType     string
	TargetID       int64
	PostID         *int64
	CommentID      *int64
}

// Create 写入一条通知。以下情况静默跳过：
//   - 接收者就是动作发起者（自通知无意义）；
//   - 接收者不存在、已停用或是 AI（AI 通过任务系统消费事件，不使用通知中心）；
//   - 相同幂等键已存在（重复事件）。
func Create(ctx context.Context, q access.Querier, ids IDGenerator, p Params) error {
	if p.RecipientID <= 0 || p.ActorID <= 0 || p.RecipientID == p.ActorID || p.IdempotencyKey == "" {
		return nil
	}
	var eligible bool
	if err := q.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM users
			WHERE id = $1 AND status = 'active' AND NOT is_ai
		)`, p.RecipientID).Scan(&eligible); err != nil {
		return err
	}
	if !eligible {
		return nil
	}
	id, err := ids.Next()
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, `
		INSERT INTO notifications (
			id, recipient_id, actor_id, type, target_type, target_id,
			post_id, comment_id, idempotency_key
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (idempotency_key) DO NOTHING`,
		id, p.RecipientID, p.ActorID, p.Type, p.TargetType, p.TargetID,
		p.PostID, p.CommentID, p.IdempotencyKey,
	)
	return err
}

// MentionTarget 是解析后的被提及用户。
type MentionTarget struct {
	UserID   int64
	Username string
}

// ResolveUsernames 把正文中的 @用户名 解析为真实账号，忽略不存在的账号。
// 停用账号不再产生新提及通知。
func ResolveUsernames(ctx context.Context, q access.Querier, names []string) ([]MentionTarget, error) {
	if len(names) == 0 {
		return nil, nil
	}
	rows, err := q.Query(ctx, `
		SELECT id, username FROM users
		WHERE LOWER(username) = ANY($1) AND status = 'active'`, lowerAll(names))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var targets []MentionTarget
	for rows.Next() {
		var t MentionTarget
		if err := rows.Scan(&t.UserID, &t.Username); err != nil {
			return nil, err
		}
		targets = append(targets, t)
	}
	return targets, rows.Err()
}

// RebuildMentions 按当前正文重建提及记录，并为人类接收者生成提及通知。
// 只应在内容已发布且可见性为公开时调用（非公开内容不触发 AI 读取与提及通知）。
func RebuildMentions(
	ctx context.Context,
	q access.Querier,
	ids IDGenerator,
	sourceType string,
	sourceID int64,
	postID int64,
	names []string,
) error {
	targets, err := ResolveUsernames(ctx, q, names)
	if err != nil {
		return err
	}
	keep := make([]int64, 0, len(targets))
	for _, t := range targets {
		keep = append(keep, t.UserID)
	}
	if _, err := q.Exec(ctx, `
		UPDATE mentions SET is_active = FALSE
		WHERE source_type = $1 AND source_id = $2 AND is_active
		  AND NOT (mentioned_user_id = ANY($3::bigint[]))`,
		sourceType, sourceID, keep,
	); err != nil {
		return err
	}
	for _, target := range targets {
		if _, err := q.Exec(ctx, `
			INSERT INTO mentions (id, source_type, source_id, mentioned_user_id, is_active)
			VALUES ($1, $2, $3, $4, TRUE)
			ON CONFLICT (source_type, source_id, mentioned_user_id)
			DO UPDATE SET is_active = TRUE`,
			mustID(ids), sourceType, sourceID, target.UserID,
		); err != nil {
			return err
		}
	}
	return nil
}

// MentionNotifications 为已发布的公开内容生成提及通知。
func MentionNotifications(
	ctx context.Context,
	q access.Querier,
	ids IDGenerator,
	sourceType string,
	sourceID int64,
	postID int64,
	authorID int64,
	names []string,
) error {
	targets, err := ResolveUsernames(ctx, q, names)
	if err != nil {
		return err
	}
	for _, target := range targets {
		targetID := postID
		commentID := (*int64)(nil)
		if sourceType == SourceComment {
			commentID = &sourceID
		}
		if err := Create(ctx, q, ids, Params{
			IdempotencyKey: "mention:" + sourceType + ":" + itoa(sourceID) + ":" + itoa(target.UserID),
			RecipientID:    target.UserID,
			ActorID:        authorID,
			Type:           TypeMention,
			TargetType:     targetTypeFor(sourceType),
			TargetID:       targetID,
			PostID:         &postID,
			CommentID:      commentID,
		}); err != nil {
			return err
		}
	}
	return nil
}

// PublishPostNotifications 在帖子发布时解析提及并写入通知（仅公开可见性）。
func PublishPostNotifications(
	ctx context.Context,
	q access.Querier,
	ids IDGenerator,
	postID int64,
	authorID int64,
	plainText string,
	mentionNames []string,
) error {
	if err := RebuildMentions(ctx, q, ids, SourcePost, postID, postID, mentionNames); err != nil {
		return err
	}
	if len(mentionNames) == 0 {
		return nil
	}
	return MentionNotifications(ctx, q, ids, SourcePost, postID, postID, authorID, mentionNames)
}

// DeactivateMentions 在内容重新变为非公开或删除时停用提及，避免继续触发 AI 与通知。
func DeactivateMentions(ctx context.Context, q access.Querier, sourceType string, sourceID int64) error {
	_, err := q.Exec(ctx, `
		UPDATE mentions SET is_active = FALSE
		WHERE source_type = $1 AND source_id = $2 AND is_active`, sourceType, sourceID)
	return err
}

func targetTypeFor(sourceType string) string {
	if sourceType == SourceComment {
		return "comment"
	}
	return "post"
}

func lowerAll(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, lowerASCII(v))
	}
	return out
}

func lowerASCII(value string) string {
	buf := []byte(value)
	for i := range buf {
		if buf[i] >= 'A' && buf[i] <= 'Z' {
			buf[i] += 'a' - 'A'
		}
	}
	return string(buf)
}

func itoa(value int64) string { return api.FormatID(value) }

// mustID 用于已在外层保证生成器可用的场景；失败时返回 0 由唯一约束兜底报错。
func mustID(ids IDGenerator) int64 {
	id, err := ids.Next()
	if err != nil {
		return 0
	}
	return id
}
