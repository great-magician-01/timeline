package app

import (
	"context"
	"fmt"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/group/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/db"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// CreateGroup 建群：创建者即群主，可同时通过 usernames 拉入初始成员（≤20 人）。
// 拉人资格逐个校验（停用/关闭私信拒绝，AI 允许）；名单含创建者或重复项按去重处理。
func (s *Service) CreateGroup(ctx context.Context, viewer model.Viewer, name string, usernames []string) (Group, error) {
	if err := s.requireWritable(viewer); err != nil {
		return Group{}, err
	}
	cleanName, err := domain.ValidateName(name)
	if err != nil {
		return Group{}, err
	}
	if len(usernames) > domain.MaxInvitesPerCall {
		return Group{}, apperr.Validation("一次最多拉入 20 名成员")
	}
	if err := s.allowAction(viewer); err != nil {
		return Group{}, err
	}
	// 去重；创建者自己自动作为群主入群，名单中的同名校验在解析后按 ID 排除。
	seen := map[string]struct{}{}
	requested := make([]string, 0, len(usernames))
	for _, raw := range usernames {
		username := trimUsername(raw)
		if username == "" {
			continue
		}
		if _, ok := seen[username]; ok {
			continue
		}
		seen[username] = struct{}{}
		requested = append(requested, username)
	}
	if len(requested) > domain.MaxMembers-1 {
		return Group{}, apperr.Validation("群成员已达上限")
	}

	now := s.now()
	groupID, err := s.newID()
	if err != nil {
		return Group{}, err
	}
	// 逐个解析并校验拉人资格（任何一人不合格整个建群失败，避免静默部分成功）。
	invited, err := s.resolveInvitees(ctx, requested, viewer.ID)
	if err != nil {
		return Group{}, err
	}

	var group model.GroupConversation
	err = s.deps.Tx.WithTx(ctx, func(ctx context.Context) error {
		if count, err := s.deps.Members.OwnedGroupCount(ctx, viewer.ID); err != nil {
			return err
		} else if count >= domain.MaxOwnedGroups {
			return apperr.Conflict("group_owner_limit", "你创建的群已达到上限")
		}
		created, err := s.deps.Groups.Insert(ctx, model.GroupConversation{
			ID:        groupID,
			Name:      cleanName,
			OwnerID:   viewer.ID,
			Status:    model.GroupStatusActive,
			CreatedAt: now,
			UpdatedAt: now,
		})
		if err != nil {
			return err
		}
		group = created
		if err := s.insertOwnerMember(ctx, groupID, viewer.ID, now); err != nil {
			return err
		}
		for _, user := range invited {
			if _, err := s.insertMemberRow(ctx, groupID, user.ID, model.GroupRoleMember, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Group{}, err
	}
	return s.groupView(ctx, viewer, group)
}

// GroupList 返回访问者可见的群（有效成员、未隐藏、有消息），按最后消息倒序分页，
// 批量补齐角色、成员数、最后消息预览与未读数。
func (s *Service) GroupList(ctx context.Context, viewer model.Viewer, cursor int64, limit int) (GroupPage, error) {
	rows, err := s.deps.Groups.ListByUser(ctx, viewer.ID, cursor, limit+1)
	if err != nil {
		return GroupPage{}, err
	}
	rows, hasMore := trimPage(rows, limit)

	memberships, err := s.deps.Members.MembershipsByUser(ctx, viewer.ID)
	if err != nil {
		return GroupPage{}, err
	}
	byGroup := make(map[int64]model.GroupMember, len(memberships))
	for _, m := range memberships {
		byGroup[m.GroupID] = m
	}

	groupIDs := make([]int64, 0, len(rows))
	lastMessageIDs := make([]int64, 0, len(rows))
	for _, row := range rows {
		groupIDs = append(groupIDs, row.ID)
		if row.LastMessageID != nil {
			lastMessageIDs = append(lastMessageIDs, *row.LastMessageID)
		}
	}
	unread, err := s.deps.Messages.UnreadByGroups(ctx, viewer.ID)
	if err != nil {
		return GroupPage{}, err
	}
	lastMessages, err := s.deps.Messages.ByIDs(ctx, lastMessageIDs)
	if err != nil {
		return GroupPage{}, err
	}

	page := GroupPage{Items: make([]Group, 0, len(rows)), HasMore: hasMore}
	for _, row := range rows {
		item := Group{
			Row:         row,
			Role:        model.GroupRoleMember,
			UnreadCount: unread[row.ID],
		}
		if m, ok := byGroup[row.ID]; ok {
			item.Role = m.Role
		}
		if row.LastMessageID != nil {
			if msg, ok := lastMessages[*row.LastMessageID]; ok {
				item.LastMessage = &msg
			}
			page.NextCursor = *row.LastMessageID
		}
		page.Items = append(page.Items, item)
	}
	return page, nil
}

// GroupDetail 返回单个群视图（群信息 + 访问者角色 + 未读数）；非成员按不存在处理。
func (s *Service) GroupDetail(ctx context.Context, viewer model.Viewer, groupID int64) (Group, error) {
	member, err := s.deps.Members.GetByGroupAndUser(ctx, groupID, viewer.ID)
	if err != nil {
		return Group{}, err
	}
	if err := domain.RequireMember(member, viewer.ID); err != nil {
		return Group{}, err
	}
	group, err := s.deps.Groups.GetByID(ctx, groupID)
	if err != nil {
		return Group{}, err
	}
	return s.groupView(ctx, viewer, group)
}

// GroupMembers 返回群的有效成员分页（按加入时间正序）；非成员按不存在处理。
func (s *Service) GroupMembers(ctx context.Context, viewer model.Viewer, groupID int64, cursorJoinedAt time.Time, cursorID int64, limit int) (MemberPage, error) {
	member, err := s.deps.Members.GetByGroupAndUser(ctx, groupID, viewer.ID)
	if err != nil {
		return MemberPage{}, err
	}
	if err := domain.RequireMember(member, viewer.ID); err != nil {
		return MemberPage{}, err
	}
	rows, err := s.deps.Members.ListByGroup(ctx, groupID, cursorJoinedAt, cursorID, limit+1)
	if err != nil {
		return MemberPage{}, err
	}
	rows, hasMore := trimPage(rows, limit)

	userIDs := make([]int64, 0, len(rows))
	for _, row := range rows {
		userIDs = append(userIDs, row.UserID)
	}
	briefs, err := s.deps.Users.GetUserBriefs(ctx, userIDs)
	if err != nil {
		return MemberPage{}, err
	}
	page := MemberPage{Items: make([]MemberItem, 0, len(rows)), HasMore: hasMore}
	for _, row := range rows {
		page.Items = append(page.Items, MemberItem{Row: row, Brief: briefs[row.UserID]})
		if len(page.Items) == len(rows) {
			page.NextCursor = formatMemberCursor(row.JoinedAt, row.ID)
		}
	}
	return page, nil
}

// Rename 群主改名。
func (s *Service) Rename(ctx context.Context, viewer model.Viewer, groupID int64, name string) (Group, error) {
	if err := s.requireWritable(viewer); err != nil {
		return Group{}, err
	}
	cleanName, err := domain.ValidateName(name)
	if err != nil {
		return Group{}, err
	}
	err = s.deps.Tx.WithTx(ctx, func(ctx context.Context) error {
		group, member, getErr := s.lockViewer(ctx, groupID, viewer.ID)
		if getErr != nil {
			return getErr
		}
		if err := domain.RequireOwner(group, member, viewer.ID); err != nil {
			return err
		}
		return s.deps.Groups.Rename(ctx, groupID, cleanName, s.now())
	})
	if err != nil {
		return Group{}, err
	}
	group, err := s.deps.Groups.GetByID(ctx, groupID)
	if err != nil {
		return Group{}, err
	}
	return s.groupView(ctx, viewer, group)
}

// Invite 群主拉人：支持把已退出的成员重新拉回（复用同一行重置游标）。
func (s *Service) Invite(ctx context.Context, viewer model.Viewer, groupID int64, usernames []string) ([]MemberItem, error) {
	if err := s.requireWritable(viewer); err != nil {
		return nil, err
	}
	if len(usernames) == 0 || len(usernames) > domain.MaxInvitesPerCall {
		return nil, apperr.Validation("一次拉入 1–20 名成员")
	}
	if err := s.allowAction(viewer); err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	requested := make([]string, 0, len(usernames))
	for _, raw := range usernames {
		username := trimUsername(raw)
		if username == "" {
			continue
		}
		if _, ok := seen[username]; ok {
			continue
		}
		seen[username] = struct{}{}
		requested = append(requested, username)
	}
	invited, err := s.resolveInvitees(ctx, requested, viewer.ID)
	if err != nil {
		return nil, err
	}

	now := s.now()
	added := make([]int64, 0, len(invited))
	err = s.deps.Tx.WithTx(ctx, func(ctx context.Context) error {
		group, member, getErr := s.lockViewer(ctx, groupID, viewer.ID)
		if getErr != nil {
			return getErr
		}
		if err := domain.RequireOwner(group, member, viewer.ID); err != nil {
			return err
		}
		if group.Dissolved() {
			return apperr.Forbidden("群已解散，不能变更成员")
		}
		count, getErr := s.deps.Members.CountActive(ctx, groupID)
		if getErr != nil {
			return getErr
		}
		for _, user := range invited {
			if count >= domain.MaxMembers {
				return apperr.Conflict("group_member_limit", "群成员已达上限")
			}
			wasAdded, getErr := s.insertMemberRow(ctx, groupID, user.ID, model.GroupRoleMember, now)
			if getErr != nil {
				return getErr
			}
			if wasAdded {
				count++
				added = append(added, user.ID)
			}
		}
		// 成员变更信号：让全部成员刷新成员列表（拉人提交后发送）。
		if len(added) > 0 && s.deps.Realtime != nil {
			memberIDs, getErr := s.activeMemberIDs(ctx, groupID)
			if getErr != nil {
				return getErr
			}
			s.publishAfterCommit(ctx, memberIDs, groupID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// 回读成员行组装完整 DTO（id/role/joined_at）；重复拉在群成员不在 added 中。
	items := make([]MemberItem, 0, len(added))
	for _, uid := range added {
		member, err := s.deps.Members.GetByGroupAndUser(ctx, groupID, uid)
		if err != nil {
			return nil, err
		}
		brief, err := s.deps.Users.GetUserBrief(ctx, uid)
		if err != nil {
			return nil, err
		}
		items = append(items, MemberItem{Row: member, Brief: brief})
	}
	return items, nil
}

// RemoveMember 群主踢人：仅普通成员可被移除。
func (s *Service) RemoveMember(ctx context.Context, viewer model.Viewer, groupID, targetUserID int64) error {
	if err := s.requireWritable(viewer); err != nil {
		return err
	}
	now := s.now()
	return s.deps.Tx.WithTx(ctx, func(ctx context.Context) error {
		group, member, getErr := s.lockViewer(ctx, groupID, viewer.ID)
		if getErr != nil {
			return getErr
		}
		if err := domain.RequireOwner(group, member, viewer.ID); err != nil {
			return err
		}
		target, getErr := s.deps.Members.GetForUpdate(ctx, groupID, targetUserID)
		if getErr != nil {
			return getErr
		}
		if getErr := domain.CanRemove(target); getErr != nil {
			return getErr
		}
		_, getErr = s.deps.Members.Leave(ctx, groupID, targetUserID, now)
		if getErr != nil {
			return getErr
		}
		if s.deps.Realtime != nil {
			memberIDs, getErr := s.activeMemberIDs(ctx, groupID)
			if getErr != nil {
				return getErr
			}
			memberIDs = append(memberIDs, targetUserID)
			s.publishAfterCommit(ctx, memberIDs, groupID)
		}
		return nil
	})
}

// TransferOwner 群主转让：新群主须是有效成员；原群主降为普通成员。
func (s *Service) TransferOwner(ctx context.Context, viewer model.Viewer, groupID, targetUserID int64) error {
	if err := s.requireWritable(viewer); err != nil {
		return err
	}
	if targetUserID == viewer.ID {
		return apperr.Validation("不能转让给自己")
	}
	now := s.now()
	return s.deps.Tx.WithTx(ctx, func(ctx context.Context) error {
		group, member, getErr := s.lockViewer(ctx, groupID, viewer.ID)
		if getErr != nil {
			return getErr
		}
		if err := domain.RequireOwner(group, member, viewer.ID); err != nil {
			return err
		}
		if group.Dissolved() {
			return apperr.Forbidden("群已解散，不能转让")
		}
		if count, getErr := s.deps.Members.OwnedGroupCount(ctx, targetUserID); getErr != nil {
			return getErr
		} else if count >= domain.MaxOwnedGroups {
			return apperr.Conflict("group_owner_limit", "对方创建的群已达到上限")
		}
		target, getErr := s.deps.Members.GetForUpdate(ctx, groupID, targetUserID)
		if getErr != nil {
			return getErr
		}
		if !target.Active() {
			return apperr.NotFound("成员不存在")
		}
		if changed, getErr := s.deps.Groups.Transfer(ctx, groupID, targetUserID, viewer.ID, now); getErr != nil {
			return getErr
		} else if !changed {
			return apperr.Forbidden("只有群主可以执行该操作")
		}
		if err := s.deps.Members.SetRole(ctx, groupID, targetUserID, model.GroupRoleOwner); err != nil {
			return err
		}
		return s.deps.Members.SetRole(ctx, groupID, viewer.ID, model.GroupRoleMember)
	})
}

// Leave 成员退出；群主退出前必须先转让或解散。
func (s *Service) Leave(ctx context.Context, viewer model.Viewer, groupID int64) error {
	if err := s.requireWritable(viewer); err != nil {
		return err
	}
	now := s.now()
	return s.deps.Tx.WithTx(ctx, func(ctx context.Context) error {
		_, member, getErr := s.lockViewer(ctx, groupID, viewer.ID)
		if getErr != nil {
			return getErr
		}
		if err := domain.RequireTransferBeforeLeave(member); err != nil {
			return err
		}
		_, getErr = s.deps.Members.Leave(ctx, groupID, viewer.ID, now)
		if getErr != nil {
			return getErr
		}
		if s.deps.Realtime != nil {
			memberIDs, getErr := s.activeMemberIDs(ctx, groupID)
			if getErr != nil {
				return getErr
			}
			s.publishAfterCommit(ctx, memberIDs, groupID)
		}
		return nil
	})
}

// Dissolve 群主解散：群置 dissolved（只读保留），全部成员收到信号。
func (s *Service) Dissolve(ctx context.Context, viewer model.Viewer, groupID int64) error {
	if err := s.requireWritable(viewer); err != nil {
		return err
	}
	now := s.now()
	return s.deps.Tx.WithTx(ctx, func(ctx context.Context) error {
		group, member, getErr := s.lockViewer(ctx, groupID, viewer.ID)
		if getErr != nil {
			return getErr
		}
		if err := domain.RequireOwner(group, member, viewer.ID); err != nil {
			return err
		}
		if _, getErr := s.deps.Groups.Dissolve(ctx, groupID, now); getErr != nil {
			return getErr
		}
		if s.deps.Realtime != nil {
			memberIDs, getErr := s.activeMemberIDs(ctx, groupID)
			if getErr != nil {
				return getErr
			}
			s.publishAfterCommit(ctx, memberIDs, groupID)
		}
		return nil
	})
}

// HideGroup 把群会话从访问者列表隐藏（拨隐藏指针到群当前最后消息）。
// 活跃群在新消息到达后自动重新出现；解散群不会再有新消息（发送入口全部
// 拒绝解散群），隐藏后即永久从列表消失，同时已读游标顶到同一点清掉残留未读。
// 隐藏只影响自己的列表视图；群详情与历史消息仍可经直接链接只读访问。
func (s *Service) HideGroup(ctx context.Context, viewer model.Viewer, groupID int64) error {
	if err := s.requireWritable(viewer); err != nil {
		return err
	}
	return s.deps.Tx.WithTx(ctx, func(ctx context.Context) error {
		group, member, getErr := s.lockViewer(ctx, groupID, viewer.ID)
		if getErr != nil {
			return getErr
		}
		if err := domain.RequireMember(member, viewer.ID); err != nil {
			return err
		}
		hideTo := int64(0)
		if group.LastMessageID != nil {
			hideTo = *group.LastMessageID
		}
		// 与 dm HideConversation 一致：隐藏时把已读游标顶到同一点，
		// 隐藏期间旧消息不残留未读；新消息到达后未读从零累计
		//（解散群不会再有新消息，即未读清零后保持）。
		if hideTo > 0 {
			if _, err := s.deps.Members.MarkRead(ctx, groupID, viewer.ID, hideTo); err != nil {
				return err
			}
		}
		return s.deps.Members.Hide(ctx, groupID, viewer.ID, hideTo)
	})
}

// ===== 内部辅助 =====

// lockViewer 事务内加锁读取群与访问者成员行（群不存在返回 NotFound）。
func (s *Service) lockViewer(ctx context.Context, groupID, viewerID int64) (model.GroupConversation, model.GroupMember, error) {
	group, err := s.deps.Groups.GetForUpdate(ctx, groupID)
	if err != nil {
		return model.GroupConversation{}, model.GroupMember{}, err
	}
	member, err := s.deps.Members.GetForUpdate(ctx, groupID, viewerID)
	if err != nil {
		return model.GroupConversation{}, model.GroupMember{}, err
	}
	return group, member, nil
}

// insertOwnerMember 建群时写入群主成员行（新行，无冲突路径）。
func (s *Service) insertOwnerMember(ctx context.Context, groupID, ownerID int64, now time.Time) error {
	id, err := s.newID()
	if err != nil {
		return err
	}
	_, _, err = s.deps.Members.Insert(ctx, model.GroupMember{
		ID:       id,
		GroupID:  groupID,
		UserID:   ownerID,
		Role:     model.GroupRoleOwner,
		JoinedAt: now,
	})
	return err
}

// insertMemberRow 写入（或重置）成员行：新行插入；唯一冲突时判断旧行——
// 有效成员则幂等跳过（返回 false），已退出则复用该行重新入群。
// 返回是否实际发生入群变化。
func (s *Service) insertMemberRow(ctx context.Context, groupID, userID int64, role string, now time.Time) (bool, error) {
	id, err := s.newID()
	if err != nil {
		return false, err
	}
	_, ok, err := s.deps.Members.Insert(ctx, model.GroupMember{
		ID:       id,
		GroupID:  groupID,
		UserID:   userID,
		Role:     role,
		JoinedAt: now,
	})
	if err != nil {
		return false, err
	}
	if ok {
		return true, nil
	}
	// 唯一冲突：读旧行区分「已在群」与「曾退出」。
	existing, err := s.deps.Members.GetForUpdate(ctx, groupID, userID)
	if err != nil {
		return false, err
	}
	if existing.Active() {
		return false, nil
	}
	if _, ok, err := s.deps.Members.Rejoin(ctx, groupID, userID, model.GroupRoleMember, now); err != nil {
		return false, err
	} else {
		return ok, nil
	}
}

// resolveInvitees 按用户名解析候选成员并逐个校验拉人资格（停用/关闭私信拒绝）。
// excludeID 用于建群场景排除创建者自己（自动作为群主入群）；
// 每次拉人 ≤20 人，逐个 GetUserByUsername 与 dm 创建会话同一模式。
func (s *Service) resolveInvitees(ctx context.Context, usernames []string, excludeID int64) ([]model.User, error) {
	if len(usernames) == 0 {
		return nil, nil
	}
	invited := make([]model.User, 0, len(usernames))
	for _, username := range usernames {
		user, err := s.deps.Users.GetUserByUsername(ctx, username)
		if err != nil {
			return nil, apperr.NotFound(fmt.Sprintf("用户 %s 不存在", username))
		}
		if user.ID == excludeID {
			continue
		}
		if err := domain.CanBeInvited(user); err != nil {
			return nil, err
		}
		invited = append(invited, user)
	}
	return invited, nil
}

// activeMemberIDs 返回群全部有效成员 ID（实时信号目标；调用方在事务内使用）。
func (s *Service) activeMemberIDs(ctx context.Context, groupID int64) ([]int64, error) {
	members, err := s.deps.Members.ListByGroup(ctx, groupID, time.Time{}, 0, domain.MaxMembers)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.UserID)
	}
	return ids, nil
}

// publishAfterCommit 在事务提交后向成员批量发送群更新信号（AfterCommit 契约：
// 提交成功才执行；无事务时立即执行；回调不得阻塞、不得 panic）。
func (s *Service) publishAfterCommit(ctx context.Context, memberIDs []int64, groupID int64) {
	if s.deps.Realtime == nil || len(memberIDs) == 0 {
		return
	}
	targets := make([]int64, len(memberIDs))
	copy(targets, memberIDs)
	db.AfterCommit(ctx, func(context.Context) {
		for _, uid := range targets {
			s.deps.Realtime.PublishGroup(uid, groupID)
		}
	})
}

// groupView 按行组装群视图：访问者角色、成员数、最后消息预览与未读数。
func (s *Service) groupView(ctx context.Context, viewer model.Viewer, group model.GroupConversation) (Group, error) {
	view := Group{Row: group, Role: model.GroupRoleMember}
	member, err := s.deps.Members.GetByGroupAndUser(ctx, group.ID, viewer.ID)
	if err != nil {
		return Group{}, err
	}
	view.Role = member.Role
	if count, err := s.deps.Members.CountActive(ctx, group.ID); err != nil {
		return Group{}, err
	} else {
		view.MemberCount = count
	}
	if group.LastMessageID != nil {
		messages, err := s.deps.Messages.ByIDs(ctx, []int64{*group.LastMessageID})
		if err != nil {
			return Group{}, err
		}
		if msg, ok := messages[*group.LastMessageID]; ok {
			view.LastMessage = &msg
		}
	}
	unread, err := s.deps.Messages.UnreadCount(ctx, group.ID, viewer.ID, member.LastReadMessageID)
	if err != nil {
		return Group{}, err
	}
	view.UnreadCount = unread
	return view, nil
}

// trimUsername 归一化用户名输入（去首尾空白）。
func trimUsername(raw string) string {
	start, end := 0, len(raw)
	for start < end && (raw[start] == ' ' || raw[start] == '\t') {
		start++
	}
	for end > start && (raw[end-1] == ' ' || raw[end-1] == '\t') {
		end--
	}
	return raw[start:end]
}

// formatMemberCursor 序列化成员分页游标（joinedAt 的 RFC3339 纳秒 + id）。
func formatMemberCursor(joinedAt time.Time, id int64) string {
	return joinedAt.UTC().Format(time.RFC3339Nano) + ":" + fmt.Sprintf("%d", id)
}
