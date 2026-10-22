package app

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/great-magician-01/timeline/internal/modules/auth/domain"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/access"
	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/shared/search"
)

// UpdateProfileInput 是资料更新用例输入；nil 指针表示不修改对应字段。
type UpdateProfileInput struct {
	UserID    int64
	Nickname  *string
	Bio       *string
	AvatarURL *string
}

// UpdateProfile 更新昵称与简介。头像只能通过附件上传（media 写 users.avatar_url），
// 直接修改 avatar_url 且与当前值不同时按旧行为返回校验错误。
func (s *Service) UpdateProfile(ctx context.Context, in UpdateProfileInput) (model.User, error) {
	if s.users == nil {
		return model.User{}, apperr.Internal("auth 账号仓储未初始化")
	}
	if in.Nickname == nil && in.Bio == nil && in.AvatarURL == nil {
		return model.User{}, apperr.Invalid("至少需要修改一项资料")
	}
	if details := domain.ValidateProfile(in.Nickname, in.Bio); details != nil {
		return model.User{}, apperr.Validation(domain.ValidationMessage(details)).WithDetails(details)
	}

	current, err := s.users.ByID(ctx, in.UserID)
	if err != nil {
		return model.User{}, err
	}
	var avatarURL *string
	if in.AvatarURL != nil && *in.AvatarURL != current.AvatarURL {
		return model.User{}, apperr.Validation("头像需通过附件上传后设置").
			WithDetails(map[string]string{"avatar_url": "头像上传将随附件模块开放"})
	}
	if in.AvatarURL != nil {
		avatarURL = in.AvatarURL
	}
	updated, err := s.users.UpdateProfile(ctx, in.UserID, in.Nickname, in.Bio, avatarURL, s.now())
	if err != nil {
		return model.User{}, internal(err, "更新用户资料失败")
	}
	return updated, nil
}

// ClearBanner 移除个人主页横幅：清空 banner_url 与 banner_attachment_id，
// 主页回退默认占位。与头像不同，横幅支持恢复默认，因此提供独立用例。
func (s *Service) ClearBanner(ctx context.Context, userID int64) (model.User, error) {
	if s.users == nil {
		return model.User{}, apperr.Internal("auth 账号仓储未初始化")
	}
	if err := s.users.UpdateBanner(ctx, userID, nil, "", s.now()); err != nil {
		return model.User{}, internal(err, "移除主页横幅失败")
	}
	return s.users.ByID(ctx, userID)
}

// FollowSettings 读取关注审批开关。
func (s *Service) FollowSettings(ctx context.Context, userID int64) (bool, error) {
	if s.users == nil {
		return false, apperr.Internal("auth 账号仓储未初始化")
	}
	user, err := s.users.ByID(ctx, userID)
	if err != nil {
		return false, err
	}
	return user.RequireFollowApproval, nil
}

// UpdateFollowSettings 更新关注审批开关并返回最新值。
func (s *Service) UpdateFollowSettings(ctx context.Context, userID int64, enabled bool) (bool, error) {
	if s.users == nil {
		return false, apperr.Internal("auth 账号仓储未初始化")
	}
	updated, err := s.users.UpdateFollowApproval(ctx, userID, enabled, s.now())
	if err != nil {
		return false, internal(err, "更新关注审批设置失败")
	}
	return updated, nil
}

// DMSettings 读取私信接收策略（all/following/mutual/none）。
func (s *Service) DMSettings(ctx context.Context, userID int64) (string, error) {
	if s.users == nil {
		return "", apperr.Internal("auth 账号仓储未初始化")
	}
	user, err := s.users.ByID(ctx, userID)
	if err != nil {
		return "", err
	}
	if user.DMPolicy == "" {
		return model.DMPolicyAll, nil
	}
	return user.DMPolicy, nil
}

// UpdateDMSettings 校验并更新私信接收策略，返回最新值。
func (s *Service) UpdateDMSettings(ctx context.Context, userID int64, raw string) (string, error) {
	if s.users == nil {
		return "", apperr.Internal("auth 账号仓储未初始化")
	}
	policy, err := access.ValidateDMPolicy(raw)
	if err != nil {
		return "", err
	}
	updated, err := s.users.UpdateDMPolicy(ctx, userID, policy, s.now())
	if err != nil {
		return "", internal(err, "更新私信接收设置失败")
	}
	return updated, nil
}

// SearchUsersResult 是用户搜索结果（游标分页）。
type SearchUsersResult struct {
	Users   []model.User
	LastID  int64
	HasMore bool
}

// SearchUsers 按用户名或昵称搜索账号；只返回公开资料由 http 层负责裁剪。
// 关键词先小写，再用 shared/search.EscapeLikePattern 转义 LIKE 通配符，
// 使 repo 的 `LOWER(...) LIKE '%' || $1 || '%' ESCAPE '\'` 按字面量子串匹配，
// 同时能命中 0017 的 trigram 表达式索引（≥3 字符短词除外，见 repo.Search 注释）。
func (s *Service) SearchUsers(ctx context.Context, keyword string, cursor int64, limit int) (SearchUsersResult, error) {
	if s.users == nil {
		return SearchUsersResult{}, apperr.Internal("auth 账号仓储未初始化")
	}
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	if keyword == "" {
		return SearchUsersResult{}, apperr.Invalid("请输入搜索关键词")
	}
	if utf8.RuneCountInString(keyword) > 100 {
		return SearchUsersResult{}, apperr.Invalid("搜索关键词过长")
	}
	users, err := s.users.Search(ctx, search.EscapeLikePattern(keyword), cursor, limit)
	if err != nil {
		return SearchUsersResult{}, internal(err, "搜索用户失败")
	}
	hasMore := len(users) > limit
	if hasMore {
		users = users[:limit]
	}
	var lastID int64
	if len(users) > 0 {
		lastID = users[len(users)-1].ID
	}
	return SearchUsersResult{Users: users, LastID: lastID, HasMore: hasMore}, nil
}

// ProfileResult 是个人主页资料响应数据。
type ProfileResult struct {
	User        model.User
	Stats       ProfileStats
	FollowState string
}

// GetProfile 读取个人主页资料、统计与访问者视角的关注状态。
func (s *Service) GetProfile(ctx context.Context, viewer model.Viewer, username string) (ProfileResult, error) {
	if s.users == nil {
		return ProfileResult{}, apperr.Internal("auth 账号仓储未初始化")
	}
	user, err := s.users.ByUsername(ctx, username)
	if err != nil {
		return ProfileResult{}, err
	}

	stats := ProfileStats{}
	if s.stats != nil {
		stats, err = s.stats.ProfileStats(ctx, user.ID)
		if err != nil {
			return ProfileResult{}, internal(err, "统计用户资料失败")
		}
	}

	followState := model.FollowStateNone
	if viewer.Authenticated && viewer.ID != user.ID && !viewer.IsAI && s.relations != nil {
		followState, err = s.relations.FollowState(ctx, viewer.ID, user.ID)
		if err != nil {
			return ProfileResult{}, internal(err, "读取关注状态失败")
		}
	}
	return ProfileResult{User: user, Stats: stats, FollowState: followState}, nil
}

// ConfigResult 是公开认证配置响应数据。
type ConfigResult struct {
	RegistrationEnabled bool
	BootstrapRequired   bool
}

// Config 读取注册开关与初始化状态：读设置失败时回退默认值（开放注册），初始化状态
// 查询失败时回退 false 并记录告警；两者都不阻塞公开配置接口（与旧 LoadOrDefaults 一致）。
func (s *Service) Config(ctx context.Context) (ConfigResult, error) {
	settings := s.settingsSnapshotOrDefaults(ctx)
	bootstrap, err := s.BootstrapRequired(ctx)
	if err != nil {
		s.log.Warn("读取初始化状态失败", "err", err)
		bootstrap = false
	}
	return ConfigResult{RegistrationEnabled: settings.RegistrationEnabled, BootstrapRequired: bootstrap}, nil
}
