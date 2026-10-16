package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/great-magician-01/timeline/internal/access"
	"github.com/great-magician-01/timeline/internal/ai"
	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/content"
	"github.com/great-magician-01/timeline/internal/model"
	"github.com/great-magician-01/timeline/internal/richtext"
	"github.com/great-magician-01/timeline/internal/social"
)

// manualRefDraft 是管理端“生成草稿”任务的 trigger_ref_type 取值；
// 其余手动任务（直接发布）使用 manualRefDirect。
const (
	manualRefDraft  = "draft"
	manualRefDirect = "direct"
)

// actionOutcome 是一次动作执行的结果，用于写回 ai_tasks 并形成记忆候选。
type actionOutcome struct {
	ActionType    string
	TargetType    string
	TargetID      *int64
	ResultRefType string
	ResultRefID   *int64
	Summary       string
	// MemoryNote 是给记忆形成模型的动作描述；为空时不生成记忆。
	MemoryNote string
	// SourceRefType / SourceRefID 是记忆的来源引用（帖子或评论）。
	SourceRefType string
	SourceRefID   *int64
	// Skipped 表示执行前已核对到实际结果存在（幂等跳过），不计入每日动作计数。
	Skipped bool
}

// executeDecision 以 AI 身份执行已经通过校验的动作。
// 调用方必须在同一事务内调用（动作、计数与任务结果一起提交）。
func (s *Service) executeDecision(
	ctx context.Context,
	q access.Querier,
	rt ai.Runtime,
	task aiTask,
	decision Decision,
	target decisionTarget,
	now time.Time,
) (actionOutcome, error) {
	var outcome actionOutcome
	switch decision.Action {
	case actionOriginalPost, actionQuote:
		asDraft := isDraftTask(task)
		quoted := target.QuotedID
		post, err := s.content.CreatePostForActor(ctx, q, content.ActorPostInput{
			AuthorID:      rt.AIID,
			ContentFormat: richtext.FormatMarkdown,
			ContentSource: decision.Content,
			// AI 已发布的原创与引用帖恒为公开（需求 13.4）。
			Visibility:   model.VisibilityPublic,
			QuotedPostID: quoted,
			AsDraft:      asDraft,
		})
		if err != nil {
			return actionOutcome{}, err
		}
		postID := post.ID
		outcome.ActionType = decision.Action
		outcome.ResultRefType = resultRefPost
		outcome.ResultRefID = &postID
		outcome.SourceRefType = sourcePost
		outcome.SourceRefID = &postID
		if asDraft {
			outcome.Summary = "生成 AI 草稿，等待管理员发布"
			outcome.MemoryNote = ""
		} else if decision.Action == actionQuote {
			outcome.Summary = fmt.Sprintf("引用转发了帖子 %s", api.FormatID(postID))
			outcome.MemoryNote = fmt.Sprintf("引用转发了一条公开帖子，并写道：%s", truncateRunes(decision.Content, 300))
		} else {
			outcome.Summary = fmt.Sprintf("发布了原创帖 %s", api.FormatID(postID))
			outcome.MemoryNote = fmt.Sprintf("发布了一条原创帖：%s", truncateRunes(decision.Content, 300))
		}
		return outcome, nil

	case actionComment, actionReply:
		in := social.CommentInput{
			ActorID: rt.AIID,
			IsAI:    true,
			PostID:  target.PostID,
			Format:  richtext.FormatMarkdown,
			Source:  decision.Content,
		}
		if decision.Action == actionReply {
			parent := target.ID
			in.ParentID = &parent
		}
		comment, err := s.social.CreateCommentFor(ctx, q, in)
		if err != nil {
			return actionOutcome{}, err
		}
		commentID := comment.ID
		if decision.Action == actionReply {
			targetID := target.ID
			outcome.TargetType = targetComment
			outcome.TargetID = &targetID
			outcome.Summary = fmt.Sprintf("回复了评论 %s", api.FormatID(targetID))
			outcome.MemoryNote = fmt.Sprintf("回复了一条公开评论，写道：%s", truncateRunes(decision.Content, 300))
		} else {
			postID := target.PostID
			outcome.TargetType = targetPost
			outcome.TargetID = &postID
			outcome.Summary = fmt.Sprintf("评论了帖子 %s", api.FormatID(postID))
			outcome.MemoryNote = fmt.Sprintf("评论了一条公开帖子，写道：%s", truncateRunes(decision.Content, 300))
		}
		outcome.ActionType = decision.Action
		outcome.ResultRefType = resultRefComment
		outcome.ResultRefID = &commentID
		outcome.SourceRefType = sourceComment
		outcome.SourceRefID = &commentID
		return outcome, nil

	case actionLike:
		targetType := target.Type
		if targetType != targetComment {
			targetType = targetPost
		}
		active, err := s.likeActive(ctx, q, rt.AIID, targetType, target.ID)
		if err != nil {
			return actionOutcome{}, err
		}
		if !active {
			if _, _, err := s.social.LikeForActor(ctx, q, rt.AIID, targetType, target.ID); err != nil {
				return actionOutcome{}, err
			}
		}
		targetID := target.ID
		outcome.ActionType = actionLike
		outcome.TargetType = targetType
		outcome.TargetID = &targetID
		outcome.ResultRefType = resultRefLike
		outcome.ResultRefID = &targetID
		if active {
			outcome.Summary = fmt.Sprintf("点赞已存在，跳过重复动作（%s %s）", targetType, api.FormatID(targetID))
			outcome.MemoryNote = ""
			outcome.Skipped = true
		} else {
			outcome.Summary = fmt.Sprintf("点赞了 %s %s", targetType, api.FormatID(targetID))
			outcome.MemoryNote = fmt.Sprintf("点赞了一条公开%s", targetTypeLabel(targetType))
		}
		return outcome, nil

	case actionFollow:
		following, err := access.IsFollowing(ctx, q, rt.AIID, target.ID)
		if err != nil {
			return actionOutcome{}, err
		}
		state := model.FollowStateFollowing
		if !following {
			state, err = s.social.FollowForActor(ctx, q, rt.AIID, target.ID)
			if err != nil {
				return actionOutcome{}, err
			}
		}
		targetID := target.ID
		outcome.ActionType = actionFollow
		outcome.TargetType = targetUser
		outcome.TargetID = &targetID
		outcome.ResultRefType = resultRefFollow
		outcome.ResultRefID = &targetID
		switch {
		case following:
			outcome.Summary = fmt.Sprintf("关注关系已存在，跳过重复动作（用户 %s）", api.FormatID(targetID))
			outcome.MemoryNote = ""
			outcome.Skipped = true
		case state == model.FollowStatePending:
			outcome.Summary = fmt.Sprintf("已向用户 %s 发出关注申请", api.FormatID(targetID))
			outcome.MemoryNote = fmt.Sprintf("关注了用户 %s（对方需要审批，已发出申请）", api.FormatID(targetID))
		default:
			outcome.Summary = fmt.Sprintf("关注了用户 %s", api.FormatID(targetID))
			outcome.MemoryNote = fmt.Sprintf("关注了用户 %s 并开始关注其公开内容", api.FormatID(targetID))
		}
		return outcome, nil

	case actionRepost:
		active, err := s.repostActive(ctx, q, rt.AIID, target.ID)
		if err != nil {
			return actionOutcome{}, err
		}
		if !active {
			if _, _, err := s.social.RepostForActor(ctx, q, rt.AIID, target.ID); err != nil {
				return actionOutcome{}, err
			}
		}
		targetID := target.ID
		outcome.ActionType = actionRepost
		outcome.TargetType = targetPost
		outcome.TargetID = &targetID
		outcome.ResultRefType = resultRefRepost
		outcome.ResultRefID = &targetID
		outcome.SourceRefType = sourcePost
		outcome.SourceRefID = &targetID
		if active {
			outcome.Summary = fmt.Sprintf("转发已存在，跳过重复动作（帖子 %s）", api.FormatID(targetID))
			outcome.MemoryNote = ""
			outcome.Skipped = true
		} else {
			outcome.Summary = fmt.Sprintf("转发了帖子 %s", api.FormatID(targetID))
			outcome.MemoryNote = fmt.Sprintf("转发了一条公开帖子 %s", api.FormatID(targetID))
		}
		return outcome, nil

	case actionUnlike:
		// 取消点赞：只对“自己当前点过赞的帖子”生效；已取消时视为幂等跳过。
		active, err := s.likeActive(ctx, q, rt.AIID, targetPost, target.ID)
		if err != nil {
			return actionOutcome{}, err
		}
		if active {
			if _, _, err := s.social.UnlikeForActor(ctx, q, rt.AIID, targetPost, target.ID); err != nil {
				return actionOutcome{}, err
			}
		}
		targetID := target.ID
		outcome.ActionType = actionUnlike
		outcome.TargetType = targetPost
		outcome.TargetID = &targetID
		outcome.ResultRefType = resultRefLike
		outcome.ResultRefID = &targetID
		if active {
			outcome.Summary = fmt.Sprintf("取消了对帖子 %s 的点赞", api.FormatID(targetID))
			outcome.MemoryNote = fmt.Sprintf("取消了对帖子 %s 的点赞", api.FormatID(targetID))
		} else {
			outcome.Summary = fmt.Sprintf("该点赞已不存在，跳过（帖子 %s）", api.FormatID(targetID))
			outcome.MemoryNote = ""
			outcome.Skipped = true
		}
		return outcome, nil

	case actionUnfollow:
		// 取关：只对“自己当前关注的用户”生效；对方开启审批时不会留下有效关系，
		// 因此这里以生效关注关系为准。
		following, err := access.IsFollowing(ctx, q, rt.AIID, target.ID)
		if err != nil {
			return actionOutcome{}, err
		}
		if following {
			if _, err := s.social.UnfollowForActor(ctx, q, rt.AIID, target.ID); err != nil {
				return actionOutcome{}, err
			}
		}
		targetID := target.ID
		outcome.ActionType = actionUnfollow
		outcome.TargetType = targetUser
		outcome.TargetID = &targetID
		outcome.ResultRefType = resultRefFollow
		outcome.ResultRefID = &targetID
		if following {
			outcome.Summary = fmt.Sprintf("取消了对用户 %s 的关注", api.FormatID(targetID))
			outcome.MemoryNote = fmt.Sprintf("取消了对用户 %s 的关注", api.FormatID(targetID))
		} else {
			outcome.Summary = fmt.Sprintf("关注关系已不存在，跳过（用户 %s）", api.FormatID(targetID))
			outcome.MemoryNote = ""
			outcome.Skipped = true
		}
		return outcome, nil

	case actionNoop:
		// 模型选择无操作：直接进入 noop 终态，不产生任何写入。
		return actionOutcome{ActionType: actionNoop, Summary: "选择不执行任何操作", Skipped: true}, nil

	default:
		return actionOutcome{}, api.ErrValidation("动作不在白名单内")
	}
}

// finalizeAction 在同一事务内累加动作计数（仅对真正产生动作的成功结果）。
// 幂等跳过（实际结果已存在）与 noop 不计入每日上限。
func (s *Service) finalizeAction(
	ctx context.Context,
	q access.Querier,
	rt ai.Runtime,
	outcome actionOutcome,
	now time.Time,
) error {
	if outcome.ActionType == "" || outcome.ActionType == actionNoop || outcome.Skipped {
		return nil
	}
	return s.bumpActionCounter(ctx, q, rt.AIID, outcome.ActionType, now)
}

// likeActive 判断 AI 是否已对该目标点赞（动作执行前的幂等核对）。
func (s *Service) likeActive(
	ctx context.Context,
	q access.Querier,
	userID int64,
	targetType string,
	targetID int64,
) (bool, error) {
	var active bool
	err := q.QueryRow(ctx, `
		SELECT is_active FROM likes
		WHERE user_id = $1 AND target_type = $2 AND target_id = $3`,
		userID, targetType, targetID).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return active, err
}

// repostActive 判断 AI 是否已转发该帖子（动作执行前的幂等核对）。
func (s *Service) repostActive(
	ctx context.Context,
	q access.Querier,
	userID int64,
	postID int64,
) (bool, error) {
	var active bool
	err := q.QueryRow(ctx, `
		SELECT is_active FROM reposts WHERE user_id = $1 AND post_id = $2`,
		userID, postID).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return active, err
}

// targetTypeLabel 返回动作目标类型的中文描述。
func targetTypeLabel(targetType string) string {
	switch targetType {
	case targetComment:
		return "评论"
	case targetUser:
		return "用户"
	default:
		return "帖子"
	}
}

// isDraftTask 判断任务是否只生成草稿（AI 草稿仅管理员可见）。
func isDraftTask(task aiTask) bool {
	return task.TriggerType == triggerManual && strings.TrimSpace(task.TriggerRefType) == manualRefDraft
}
