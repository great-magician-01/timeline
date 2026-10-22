package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/clock"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// ===== 内存 fake：让 app 用例在没有数据库的情况下可测 =====

type fakeUserRepo struct {
	users map[int64]model.User
	seq   int64
	// countErr 非空时让 CountHumanUsers 返回该错误，用于测试初始化状态查询失败的兜底。
	countErr error
}

func newFakeUserRepo() *fakeUserRepo { return &fakeUserRepo{users: map[int64]model.User{}} }

func (r *fakeUserRepo) nextID() int64 {
	r.seq++
	return r.seq
}

func (r *fakeUserRepo) Insert(_ context.Context, u model.User) error {
	for _, existing := range r.users {
		if strings.EqualFold(existing.Username, u.Username) {
			return apperr.Conflict("duplicate", "用户名已存在")
		}
		if existing.Email != "" && strings.EqualFold(existing.Email, u.Email) {
			return apperr.Conflict("duplicate", "邮箱已存在")
		}
	}
	r.users[u.ID] = u
	return nil
}

func (r *fakeUserRepo) ByID(_ context.Context, id int64) (model.User, error) {
	u, ok := r.users[id]
	if !ok {
		return model.User{}, apperr.NotFound("用户不存在")
	}
	return u, nil
}

func (r *fakeUserRepo) ByIDForUpdate(ctx context.Context, id int64) (model.User, error) {
	return r.ByID(ctx, id)
}

func (r *fakeUserRepo) ByUsername(_ context.Context, username string) (model.User, error) {
	for _, u := range r.users {
		if strings.EqualFold(u.Username, username) {
			return u, nil
		}
	}
	return model.User{}, apperr.NotFound("用户不存在")
}

func (r *fakeUserRepo) ByLoginIdentifier(_ context.Context, identifier string) (model.User, error) {
	for _, u := range r.users {
		if strings.EqualFold(u.Username, identifier) ||
			(u.Email != "" && strings.EqualFold(u.Email, identifier)) {
			return u, nil
		}
	}
	return model.User{}, apperr.NotFound("用户不存在")
}

func (r *fakeUserRepo) UpdateProfile(_ context.Context, id int64, nickname, bio, avatarURL *string, at time.Time) (model.User, error) {
	u, ok := r.users[id]
	if !ok {
		return model.User{}, apperr.NotFound("用户不存在")
	}
	if nickname != nil {
		u.Nickname = *nickname
	}
	if bio != nil {
		u.Bio = *bio
	}
	if avatarURL != nil {
		u.AvatarURL = *avatarURL
	}
	u.UpdatedAt = at
	r.users[id] = u
	return u, nil
}

func (r *fakeUserRepo) UpdateProfileFields(_ context.Context, id int64, username, nickname, bio *string, at time.Time) (model.User, error) {
	u, ok := r.users[id]
	if !ok {
		return model.User{}, apperr.NotFound("用户不存在")
	}
	if username != nil {
		u.Username = *username
	}
	if nickname != nil {
		u.Nickname = *nickname
	}
	if bio != nil {
		u.Bio = *bio
	}
	u.UpdatedAt = at
	r.users[id] = u
	return u, nil
}

func (r *fakeUserRepo) UpdateFollowApproval(_ context.Context, id int64, enabled bool, at time.Time) (bool, error) {
	u, ok := r.users[id]
	if !ok || u.IsAI {
		return false, apperr.NotFound("用户不存在")
	}
	u.RequireFollowApproval = enabled
	u.UpdatedAt = at
	r.users[id] = u
	return enabled, nil
}

func (r *fakeUserRepo) UpdateDMPolicy(_ context.Context, id int64, policy string, at time.Time) (string, error) {
	u, ok := r.users[id]
	if !ok || u.IsAI {
		return "", apperr.NotFound("用户不存在")
	}
	u.DMPolicy = policy
	u.UpdatedAt = at
	r.users[id] = u
	return policy, nil
}

func (r *fakeUserRepo) UpdateRole(_ context.Context, id int64, role string, at time.Time) (model.User, error) {
	u, ok := r.users[id]
	if !ok {
		return model.User{}, apperr.NotFound("用户不存在")
	}
	u.Role = role
	u.UpdatedAt = at
	r.users[id] = u
	return u, nil
}

func (r *fakeUserRepo) UpdateStatus(_ context.Context, id int64, status string, suspendedAt *time.Time, tokenVersion int64, at time.Time) (model.User, error) {
	u, ok := r.users[id]
	if !ok {
		return model.User{}, apperr.NotFound("用户不存在")
	}
	u.Status = status
	u.SuspendedAt = suspendedAt
	u.TokenVersion = tokenVersion
	u.UpdatedAt = at
	r.users[id] = u
	return u, nil
}

func (r *fakeUserRepo) UpdatePassword(_ context.Context, id int64, hash string, tokenVersion int64, at time.Time) error {
	u, ok := r.users[id]
	if !ok {
		return apperr.NotFound("用户不存在")
	}
	u.PasswordHash = hash
	u.TokenVersion = tokenVersion
	u.UpdatedAt = at
	r.users[id] = u
	return nil
}

func (r *fakeUserRepo) UpdateAvatar(_ context.Context, id int64, _ *int64, avatarURL string, at time.Time) error {
	u, ok := r.users[id]
	if !ok {
		return apperr.NotFound("用户不存在")
	}
	u.AvatarURL = avatarURL
	u.UpdatedAt = at
	r.users[id] = u
	return nil
}

func (r *fakeUserRepo) UpdateBanner(_ context.Context, id int64, _ *int64, bannerURL string, at time.Time) error {
	u, ok := r.users[id]
	if !ok {
		return apperr.NotFound("用户不存在")
	}
	u.BannerURL = bannerURL
	u.UpdatedAt = at
	r.users[id] = u
	return nil
}

func (r *fakeUserRepo) Search(_ context.Context, keyword string, _ int64, limit int) ([]model.User, error) {
	result := make([]model.User, 0, limit)
	for _, u := range r.users {
		if strings.Contains(strings.ToLower(u.Username), keyword) ||
			strings.Contains(strings.ToLower(u.Nickname), keyword) {
			result = append(result, u)
		}
	}
	return result, nil
}

func (r *fakeUserRepo) CountHumanUsers(_ context.Context) (int64, error) {
	if r.countErr != nil {
		return 0, r.countErr
	}
	var count int64
	for _, u := range r.users {
		if !u.IsAI {
			count++
		}
	}
	return count, nil
}

func (r *fakeUserRepo) HasHumanUser(_ context.Context) (bool, error) {
	if r.countErr != nil {
		return false, r.countErr
	}
	for _, u := range r.users {
		if !u.IsAI {
			return true, nil
		}
	}
	return false, nil
}

func (r *fakeUserRepo) CountSuperAdmins(_ context.Context) (int64, error) {
	var count int64
	for _, u := range r.users {
		if u.IsSuperAdmin {
			count++
		}
	}
	return count, nil
}

func (r *fakeUserRepo) GetUsers(_ context.Context, ids []int64) (map[int64]model.User, error) {
	result := map[int64]model.User{}
	for _, id := range ids {
		if u, ok := r.users[id]; ok {
			result[id] = u
		}
	}
	return result, nil
}

func (r *fakeUserRepo) GetUserBriefs(_ context.Context, ids []int64) (map[int64]model.UserBrief, error) {
	result := map[int64]model.UserBrief{}
	for _, id := range ids {
		if u, ok := r.users[id]; ok {
			result[id] = model.UserBrief{
				ID: u.ID, Username: u.Username, Nickname: u.Nickname,
				AvatarURL: u.AvatarURL, IsAI: u.IsAI, IsSuspended: u.IsSuspended(),
			}
		}
	}
	return result, nil
}

func (r *fakeUserRepo) FindActiveUsersByUsernames(_ context.Context, usernames []string) ([]model.UserBrief, error) {
	result := make([]model.UserBrief, 0, len(usernames))
	for _, name := range usernames {
		for _, u := range r.users {
			if strings.EqualFold(u.Username, name) && u.IsActive() {
				result = append(result, model.UserBrief{ID: u.ID, Username: u.Username, Nickname: u.Nickname})
			}
		}
	}
	return result, nil
}

func (r *fakeUserRepo) ListUserIDsByAIFlag(_ context.Context, isAI bool) ([]int64, error) {
	ids := make([]int64, 0)
	for _, u := range r.users {
		if u.IsAI == isAI {
			ids = append(ids, u.ID)
		}
	}
	return ids, nil
}

func (r *fakeUserRepo) ListAvatarAttachmentIDs(_ context.Context) ([]int64, error) {
	return nil, nil
}

type fakeSessionRepo struct {
	users    *fakeUserRepo
	sessions map[string]fakeSession
	seq      int64
}

type fakeSession struct {
	user    model.User
	session model.Session
}

func newFakeSessionRepo(users *fakeUserRepo) *fakeSessionRepo {
	return &fakeSessionRepo{users: users, sessions: map[string]fakeSession{}}
}

func (r *fakeSessionRepo) Insert(_ context.Context, session model.Session, tokenHash string, _ time.Time) error {
	user, ok := r.users.users[session.UserID]
	if !ok {
		return apperr.NotFound("用户不存在")
	}
	r.sessions[tokenHash] = fakeSession{user: user, session: session}
	return nil
}

func (r *fakeSessionRepo) ByTokenHash(_ context.Context, tokenHash string) (model.User, model.Session, error) {
	entry, ok := r.sessions[tokenHash]
	if !ok {
		return model.User{}, model.Session{}, apperr.NotFound("会话不存在")
	}
	// 重新从 users 读取，模拟 JOIN 拿到最新账号状态（停用/角色变化即时生效）。
	if user, ok := r.users.users[entry.user.ID]; ok {
		entry.user = user
	}
	return entry.user, entry.session, nil
}

func (r *fakeSessionRepo) Revoke(_ context.Context, sessionID int64, at time.Time) error {
	for hash, entry := range r.sessions {
		if entry.session.ID == sessionID {
			entry.session.RevokedAt = &at
			r.sessions[hash] = entry
		}
	}
	return nil
}

func (r *fakeSessionRepo) RevokeAllForUserExcept(_ context.Context, userID, exceptSessionID int64, at time.Time) error {
	for hash, entry := range r.sessions {
		if entry.session.UserID == userID && entry.session.ID != exceptSessionID {
			entry.session.RevokedAt = &at
			r.sessions[hash] = entry
		}
	}
	return nil
}

func (r *fakeSessionRepo) RevokeAllForUser(_ context.Context, userID int64, at time.Time) error {
	for hash, entry := range r.sessions {
		if entry.session.UserID == userID {
			entry.session.RevokedAt = &at
			r.sessions[hash] = entry
		}
	}
	return nil
}

func (r *fakeSessionRepo) UpdateTokenVersion(_ context.Context, sessionID, tokenVersion int64) error {
	for hash, entry := range r.sessions {
		if entry.session.ID == sessionID {
			entry.session.TokenVersion = tokenVersion
			r.sessions[hash] = entry
		}
	}
	return nil
}

type fakeTx struct{}

func (fakeTx) WithTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type fakeIDs struct{ next int64 }

func (f *fakeIDs) Next() (int64, error) {
	f.next++
	return f.next, nil
}

type fakeLocker struct{}

func (fakeLocker) AcquireBootstrapLock(context.Context) error { return nil }

// fakeSettings 是系统设置读取端口的 fake；err 非空时模拟数据库读取失败。
type fakeSettings struct {
	values contracts.SystemSettings
	err    error
}

func (f fakeSettings) Settings(context.Context) (contracts.SystemSettings, error) {
	if f.err != nil {
		return contracts.SystemSettings{}, f.err
	}
	return f.values, nil
}

func (f fakeSettings) WebSearch(context.Context) (contracts.WebSearchSettings, error) {
	return contracts.WebSearchSettings{}, nil
}

type fakeAudit struct{ entries []contracts.AuditEntry }

func (f *fakeAudit) Write(_ context.Context, entry contracts.AuditEntry) error {
	f.entries = append(f.entries, entry)
	return nil
}

type fakeRelations struct{ state string }

func (f fakeRelations) IsFollowing(context.Context, int64, int64) (bool, error) {
	return f.state == model.FollowStateFollowing, nil
}
func (f fakeRelations) FollowingSet(context.Context, int64) (map[int64]bool, error) {
	return map[int64]bool{}, nil
}
func (f fakeRelations) FollowState(context.Context, int64, int64) (string, error) {
	if f.state == "" {
		return model.FollowStateNone, nil
	}
	return f.state, nil
}

// newTestService 组装一个全部端口为内存实现的 auth 应用服务。
func newTestService(t *testing.T, settingsEnabled bool) (*Service, *fakeUserRepo, *fakeSessionRepo, *fakeAudit, *clock.Fake) {
	t.Helper()
	users := newFakeUserRepo()
	sessions := newFakeSessionRepo(users)
	audit := &fakeAudit{}
	clk := clock.NewFake()
	svc := New(Deps{
		Users:     users,
		Sessions:  sessions,
		Locks:     fakeLocker{},
		Tx:        fakeTx{},
		IDs:       &fakeIDs{},
		Clock:     clk,
		Settings:  fakeSettings{values: contracts.SystemSettings{RegistrationEnabled: settingsEnabled}},
		Audit:     audit,
		Relations: fakeRelations{},
	})
	return svc, users, sessions, audit, clk
}

func mustCreateUser(t *testing.T, users *fakeUserRepo, u model.User) model.User {
	t.Helper()
	if u.ID == 0 {
		u.ID = users.nextID()
	}
	if u.Nickname == "" {
		u.Nickname = u.Username
	}
	if u.Role == "" {
		u.Role = model.RoleUser
	}
	if u.Status == "" {
		u.Status = model.StatusActive
	}
	if u.TokenVersion == 0 {
		u.TokenVersion = 1
	}
	users.users[u.ID] = u
	return u
}
