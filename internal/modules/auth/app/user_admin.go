package app

import (
	"context"
	"strings"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/auth/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// ===== contracts.UserReader =====

// GetUser 按 ID 读取完整账号行；不存在返回领域 NotFound。
func (s *Service) GetUser(ctx context.Context, userID int64) (model.User, error) {
	if s.users == nil {
		return model.User{}, apperr.Internal("auth 账号仓储未初始化")
	}
	return s.users.ByID(ctx, userID)
}

// GetUserByUsername 按用户名读取账号行；不存在返回领域 NotFound。
func (s *Service) GetUserByUsername(ctx context.Context, username string) (model.User, error) {
	if s.users == nil {
		return model.User{}, apperr.Internal("auth 账号仓储未初始化")
	}
	return s.users.ByUsername(ctx, username)
}

// GetUsers 批量读取账号行，返回 id → User；缺失 ID 不在 map 中。
func (s *Service) GetUsers(ctx context.Context, userIDs []int64) (map[int64]model.User, error) {
	if s.users == nil {
		return nil, apperr.Internal("auth 账号仓储未初始化")
	}
	users, err := s.users.GetUsers(ctx, userIDs)
	if err != nil {
		return nil, internal(err, "批量读取账号失败")
	}
	return users, nil
}

// GetUserBrief 读取单个作者摘要；不存在返回领域 NotFound。
func (s *Service) GetUserBrief(ctx context.Context, userID int64) (model.UserBrief, error) {
	briefs, err := s.GetUserBriefs(ctx, []int64{userID})
	if err != nil {
		return model.UserBrief{}, err
	}
	brief, ok := briefs[userID]
	if !ok {
		return model.UserBrief{}, apperr.NotFound("用户不存在")
	}
	return brief, nil
}

// GetUserBriefs 批量读取作者摘要，返回 id → UserBrief；缺失 ID 不在 map 中。
func (s *Service) GetUserBriefs(ctx context.Context, userIDs []int64) (map[int64]model.UserBrief, error) {
	if s.users == nil {
		return nil, apperr.Internal("auth 账号仓储未初始化")
	}
	briefs, err := s.users.GetUserBriefs(ctx, userIDs)
	if err != nil {
		return nil, internal(err, "批量读取作者摘要失败")
	}
	return briefs, nil
}

// BriefsByIDs 与 GetUserBriefs 同义（social 模块使用的命名，contracts 兼容入口）。
func (s *Service) BriefsByIDs(ctx context.Context, userIDs []int64) (map[int64]model.UserBrief, error) {
	return s.GetUserBriefs(ctx, userIDs)
}

// FindActiveUsersByUsernames 按用户名（大小写不敏感）解析生效账号，用于 @提及。
func (s *Service) FindActiveUsersByUsernames(ctx context.Context, usernames []string) ([]model.UserBrief, error) {
	if s.users == nil {
		return nil, apperr.Internal("auth 账号仓储未初始化")
	}
	briefs, err := s.users.FindActiveUsersByUsernames(ctx, usernames)
	if err != nil {
		return nil, internal(err, "解析提及账号失败")
	}
	return briefs, nil
}

// IsSuspended 判断账号是否停用；账号不存在返回领域 NotFound。
func (s *Service) IsSuspended(ctx context.Context, userID int64) (bool, error) {
	user, err := s.GetUser(ctx, userID)
	if err != nil {
		return false, err
	}
	return user.IsSuspended(), nil
}

// ListUserIDsByAIFlag 列出全部 AI 或人类账号 ID（时间线按作者类型过滤）。
func (s *Service) ListUserIDsByAIFlag(ctx context.Context, isAI bool) ([]int64, error) {
	if s.users == nil {
		return nil, apperr.Internal("auth 账号仓储未初始化")
	}
	ids, err := s.users.ListUserIDsByAIFlag(ctx, isAI)
	if err != nil {
		return nil, internal(err, "读取账号 ID 列表失败")
	}
	return ids, nil
}

// ListAvatarAttachmentIDs 列出全部用户当前头像引用的附件 ID（媒体清理防误删）。
func (s *Service) ListAvatarAttachmentIDs(ctx context.Context) ([]int64, error) {
	if s.users == nil {
		return nil, apperr.Internal("auth 账号仓储未初始化")
	}
	ids, err := s.users.ListAvatarAttachmentIDs(ctx)
	if err != nil {
		return nil, internal(err, "读取头像附件引用失败")
	}
	return ids, nil
}

// ===== contracts.UserWriter =====

// CreateAIUser 创建一个 AI 身份账号（is_ai=true，role=user，无邮箱与密码）。
func (s *Service) CreateAIUser(ctx context.Context, in contracts.CreateAIUserInput) (model.User, error) {
	if s.users == nil || s.ids == nil {
		return model.User{}, apperr.Internal("auth 账号依赖未初始化")
	}
	username := strings.TrimSpace(in.Username)
	if err := domain.ValidateUsername(username); err != nil {
		return model.User{}, apperr.Validation(err.Error()).
			WithDetails(map[string]string{"username": err.Error()})
	}
	nickname := strings.TrimSpace(in.Nickname)
	if nickname == "" {
		nickname = username
	}
	bio := strings.TrimSpace(in.Bio)
	bioPtr, nicknamePtr := &bio, &nickname
	if details := domain.ValidateProfile(nicknamePtr, bioPtr); details != nil {
		return model.User{}, apperr.Validation(domain.ValidationMessage(details)).WithDetails(details)
	}
	id, err := s.ids.Next()
	if err != nil {
		return model.User{}, internal(err, "生成用户 ID 失败")
	}
	now := in.At
	if now.IsZero() {
		now = s.now()
	}
	user := model.User{
		ID:           id,
		Username:     username,
		Nickname:     nickname,
		Bio:          bio,
		Role:         domain.RoleUser,
		IsAI:         true,
		Status:       domain.StatusActive,
		TokenVersion: 1,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.users.Insert(ctx, user); err != nil {
		return model.User{}, err
	}
	return user, nil
}

// UpdateUserProfile 实现 contracts.UserWriter：按字段更新用户名/昵称/简介。
// 用户名重复由 repo 映射为领域 Conflict；指针为 nil 表示不修改。
func (s *Service) UpdateUserProfile(ctx context.Context, in contracts.UpdateUserProfileInput) error {
	if s.users == nil {
		return apperr.Internal("auth 账号仓储未初始化")
	}
	if in.Username == nil && in.Nickname == nil && in.Bio == nil {
		return apperr.Invalid("至少需要修改一项资料")
	}
	var username *string
	if in.Username != nil {
		trimmed := strings.TrimSpace(*in.Username)
		if err := domain.ValidateUsername(trimmed); err != nil {
			return apperr.Validation(err.Error()).
				WithDetails(map[string]string{"username": err.Error()})
		}
		username = &trimmed
	}
	if details := domain.ValidateProfile(in.Nickname, in.Bio); details != nil {
		return apperr.Validation(domain.ValidationMessage(details)).WithDetails(details)
	}
	at := in.At
	if at.IsZero() {
		at = s.now()
	}
	if _, err := s.users.UpdateProfileFields(ctx, in.UserID, username, in.Nickname, in.Bio, at); err != nil {
		return internal(err, "更新账号资料失败")
	}
	return nil
}

// SetAvatar 更新头像附件引用与展示地址（media 在自身事务内调用）。
func (s *Service) SetAvatar(ctx context.Context, in contracts.AvatarUpdateInput) error {
	if s.users == nil {
		return apperr.Internal("auth 账号仓储未初始化")
	}
	attachmentID := in.AttachmentID
	if err := s.users.UpdateAvatar(ctx, in.UserID, &attachmentID, in.AvatarURL, in.At); err != nil {
		return internal(err, "更新头像引用失败")
	}
	return nil
}

// SetUserStatus 停用/启用账号：更新状态、token_version 与会话失效；
// 与审计在同一事务内提交（审计动作由 admin 领域约定）。
func (s *Service) SetUserStatus(ctx context.Context, in contracts.UserStatusInput) (model.User, error) {
	if err := s.requireRepos(); err != nil {
		return model.User{}, err
	}
	if s.tx == nil {
		return model.User{}, apperr.Internal("auth 事务管理器未初始化")
	}
	var updated model.User
	err := s.tx.WithTx(ctx, func(txCtx context.Context) error {
		target, err := s.users.ByIDForUpdate(txCtx, in.TargetID)
		if err != nil {
			return err
		}
		actor, err := s.users.ByID(txCtx, in.ActorID)
		if err != nil {
			if isNotFound(err) {
				return apperr.Forbidden("需要管理员权限")
			}
			return internal(err, "复核操作者权限失败")
		}
		if err := domain.CanModerateUser(model.ViewerFrom(actor), target); err != nil {
			return err
		}
		if in.Suspend && target.ID == actor.ID {
			return apperr.Validation("不能停用自己的管理员账号")
		}

		desiredStatus := domain.StatusActive
		action := contracts.AuditActionUserUnsuspend
		var suspendedAt *time.Time
		if in.Suspend {
			desiredStatus = domain.StatusSuspended
			action = contracts.AuditActionUserSuspend
			suspendedAt = &in.At
		}
		changed := target.Status != desiredStatus
		tokenVersion := target.TokenVersion
		if in.Suspend && changed {
			tokenVersion++
		}
		updated = target
		if changed {
			updated, err = s.users.UpdateStatus(txCtx, target.ID, desiredStatus, suspendedAt, tokenVersion, in.At)
			if err != nil {
				return internal(err, "更新账号状态失败")
			}
		}
		if in.Suspend {
			if err := s.sessions.RevokeAllForUser(txCtx, target.ID, in.At); err != nil {
				return internal(err, "吊销停用账号会话失败")
			}
		}
		if s.audit != nil {
			targetID := target.ID
			detail := map[string]any{
				"previous_status": target.Status,
				"changed":         changed,
			}
			if err := s.audit.Write(txCtx, contracts.AuditEntry{
				AdminID: actor.ID, Action: action, TargetType: "user", TargetID: &targetID,
				Detail: detail, At: in.At,
			}); err != nil {
				return internal(err, "写入账号状态审计失败")
			}
		}
		return nil
	})
	if err != nil {
		return model.User{}, err
	}
	return updated, nil
}

// ResetPassword 管理员重置账号密码：校验、哈希、吊销会话并写审计，全部在同一事务内。
func (s *Service) ResetPassword(ctx context.Context, in contracts.ResetPasswordInput) error {
	if err := s.requireRepos(); err != nil {
		return err
	}
	if s.tx == nil {
		return apperr.Internal("auth 事务管理器未初始化")
	}
	newHash, err := domain.HashPassword(in.NewPassword)
	if err != nil {
		return apperr.Validation("新密码不符合要求").
			WithDetails(map[string]string{"new_password": err.Error()})
	}
	return s.tx.WithTx(ctx, func(txCtx context.Context) error {
		target, err := s.users.ByIDForUpdate(txCtx, in.TargetID)
		if err != nil {
			return err
		}
		actor, err := s.users.ByID(txCtx, in.ActorID)
		if err != nil {
			if isNotFound(err) {
				return apperr.Forbidden("需要管理员权限")
			}
			return internal(err, "复核操作者权限失败")
		}
		if err := domain.CanModerateUser(model.ViewerFrom(actor), target); err != nil {
			return err
		}
		if target.IsAI {
			return apperr.Validation("AI 账号没有密码，不能重置")
		}
		if err := s.users.UpdatePassword(txCtx, target.ID, newHash, target.TokenVersion+1, in.At); err != nil {
			return internal(err, "管理员重置密码失败")
		}
		if err := s.sessions.RevokeAllForUser(txCtx, target.ID, in.At); err != nil {
			return internal(err, "吊销重置账号会话失败")
		}
		if s.audit != nil {
			targetID := target.ID
			if err := s.audit.Write(txCtx, contracts.AuditEntry{
				AdminID: actor.ID, Action: contracts.AuditActionUserResetPwd,
				TargetType: "user", TargetID: &targetID, At: in.At,
			}); err != nil {
				return internal(err, "写入重置密码审计失败")
			}
		}
		return nil
	})
}
