package app

import (
	"context"
	"strconv"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/chat"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/shared/richtext"
)

// AI 群聊代发（contracts.GroupReader / contracts.GroupMessageWriter 实现）。
//
// 人类发送入口（SendMessage）要求发送者是登录人类访问者；AI 账号被 access 层拒绝，
// 因此代发走本文件的内部路径：同一渲染清洗与文本校验、同一事务与实时信号，
// 但发送资格由 agent 任务执行前的成员资格复核与这里的重校共同保证。
// AI 产出的群消息不触发 @提及（首期不引入 AI 间群聊循环，后续需要时单独设计）。

// IsMemberActive 实现 contracts.GroupReader：判断 userID 是否是群的有效成员且群未解散。
func (s *Service) IsMemberActive(ctx context.Context, groupID, userID int64) (bool, error) {
	group, err := s.deps.Groups.GetByID(ctx, groupID)
	if err != nil {
		if apperr.IsKind(err, apperr.KindNotFound) {
			return false, nil
		}
		return false, err
	}
	if group.Dissolved() {
		return false, nil
	}
	member, err := s.deps.Members.GetByGroupAndUser(ctx, groupID, userID)
	if err != nil {
		if apperr.IsKind(err, apperr.KindNotFound) {
			return false, nil
		}
		return false, err
	}
	return member.Active(), nil
}

// Snapshot 实现 contracts.GroupReader：返回群名、状态与最近消息（beforeID=0 取最新）。
// 消息按时间升序返回；已撤回消息不进入上下文。
func (s *Service) Snapshot(ctx context.Context, groupID, beforeID int64, limit int) (contracts.GroupSnapshot, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	group, err := s.deps.Groups.GetByID(ctx, groupID)
	if err != nil {
		return contracts.GroupSnapshot{}, err
	}
	rows, err := s.deps.Messages.ListBefore(ctx, groupID, beforeID, limit)
	if err != nil {
		return contracts.GroupSnapshot{}, err
	}
	messages := make([]contracts.GroupContextMessage, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		if row.Recalled() {
			continue
		}
		messages = append(messages, contracts.GroupContextMessage{
			MessageID: row.ID,
			SenderID:  row.SenderID,
			PlainText: row.PlainText,
			CreatedAt: row.CreatedAt,
		})
	}
	return contracts.GroupSnapshot{
		GroupID:        group.ID,
		Name:           group.Name,
		Active:         !group.Dissolved(),
		RecentMessages: messages,
	}, nil
}

// ListActiveGroupIDs 实现 contracts.GroupReader：返回 userID 当前有效成员且未解散的群。
func (s *Service) ListActiveGroupIDs(ctx context.Context, userID int64) ([]int64, error) {
	memberships, err := s.deps.Members.MembershipsByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(memberships))
	for _, member := range memberships {
		if !member.Active() {
			continue
		}
		group, err := s.deps.Groups.GetByID(ctx, member.GroupID)
		if err != nil {
			if apperr.IsKind(err, apperr.KindNotFound) {
				continue
			}
			return nil, err
		}
		if !group.Dissolved() {
			ids = append(ids, group.ID)
		}
	}
	return ids, nil
}

// WriteFromAI 实现 contracts.GroupMessageWriter：AI 代发一条群消息。
// 群已解散、发送者已不是成员或账号停用时返回领域错误，任务按失败处理。
// 插入后推进群最后消息指针并向全部成员推送实时信号；不触发 @提及。
func (s *Service) WriteFromAI(ctx context.Context, input contracts.GroupMessageInput) (int64, error) {
	if input.GroupID <= 0 || input.SenderID <= 0 {
		return 0, apperr.Validation("群消息输入不完整")
	}
	if !richtext.ValidFormat(input.ContentFormat) {
		return 0, apperr.Validation("群消息格式无效")
	}
	if err := chat.ValidateSource(input.ContentSource); err != nil {
		return 0, err
	}
	// AI 账号与人类共用同一发送限频（按发送者维度）。
	if allowed, retryAfter := s.deps.WriteLimiter.Consume("user:" + strconv.FormatInt(input.SenderID, 10)); !allowed {
		return 0, apperr.RateLimited(retryAfter)
	}
	now := s.now()
	messageID, err := s.newID()
	if err != nil {
		return 0, err
	}
	var row model.GroupMessage
	err = s.deps.Tx.WithTx(ctx, func(ctx context.Context) error {
		group, getErr := s.deps.Groups.GetForUpdate(ctx, input.GroupID)
		if getErr != nil {
			return getErr
		}
		if group.Dissolved() {
			return apperr.Forbidden("群已解散，不能再发送消息")
		}
		member, getErr := s.deps.Members.GetForUpdate(ctx, input.GroupID, input.SenderID)
		if getErr != nil {
			return getErr
		}
		if !member.Active() {
			return apperr.Forbidden("发送者不是该群成员")
		}
		sender, getErr := s.deps.Users.GetUser(ctx, input.SenderID)
		if getErr != nil {
			return getErr
		}
		if sender.IsSuspended() {
			return apperr.Forbidden("账号已停用，不能发送消息")
		}
		safeHTML, plain := richtext.Render(input.ContentFormat, input.ContentSource)
		if getErr := chat.ValidateBody(safeHTML, plain); getErr != nil {
			return getErr
		}
		// 表约束只接受 richtext（与人类消息一致）：markdown 输入在此完成渲染，
		// content_source 落库清洗后的安全 HTML，保持列约束与渲染管线一致。
		inserted, insertErr := s.deps.Messages.Insert(ctx, model.GroupMessage{
			ID:            messageID,
			GroupID:       group.ID,
			SenderID:      input.SenderID,
			ContentFormat: richtext.FormatRichText,
			ContentSource: safeHTML,
			ContentHTML:   safeHTML,
			PlainText:     plain,
			Status:        model.GroupMessageStatusPublished,
			CreatedAt:     now,
			UpdatedAt:     now,
		})
		if insertErr != nil {
			return insertErr
		}
		row = inserted
		if touchErr := s.deps.Groups.TouchOnSend(ctx, group.ID, inserted.ID, now); touchErr != nil {
			return touchErr
		}
		if s.deps.Realtime != nil {
			memberIDs, getErr := s.activeMemberIDs(ctx, group.ID)
			if getErr != nil {
				return getErr
			}
			s.publishAfterCommit(ctx, memberIDs, group.ID)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return row.ID, nil
}
