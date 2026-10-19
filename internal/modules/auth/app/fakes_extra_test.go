package app

// 本文补充 auth/app 单元测试所需的额外 fake 与辅助函数：
//   - cheapHash 复用最低 bcrypt 成本，避免 cost=12 拖慢涉及密码比对的用例；
//   - stub*Repo 在已有内存 fake 之上按方法注入错误，覆盖仓储故障分支；
//   - failing* 覆盖 ID 生成、注册锁、审计等端口失败分支；
//   - fakeStats / failingRelations 覆盖个人主页统计与关注状态读取。
// 已有 fake_test.go 中的 fakeUserRepo/fakeSessionRepo/fakeTx/fakeIDs/fakeLocker/
// fakeSettings/fakeAudit/fakeRelations 直接复用，不重复声明。

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// errInjected 是注入端口错误的统一底层错误；调用方可经 errors.Is 断言错误链。
var errInjected = errors.New("注入的端口错误")

// cheapHash 生成最低成本 bcrypt 哈希，供登录/改密等用例快速比对密码。
// 生产哈希成本仍由 domain.PasswordBcryptCost 决定，此处不修改生产常量。
func cheapHash(t *testing.T, password string) string {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("生成低成本测试哈希失败: %v", err)
	}
	return string(hash)
}

// stubUserRepo 在 fakeUserRepo 之上按方法注入错误；未注入时行为与内存 fake 一致。
type stubUserRepo struct {
	*fakeUserRepo
	insertErr              error
	byIDErr                error
	byIDForUpdateErr       error
	byUsernameErr          error
	updateProfileErr       error
	updateProfileFieldsErr error
	updateFollowErr        error
	updateRoleErr          error
	updateStatusErr        error
	updatePasswordErr      error
	updateAvatarErr        error
	searchErr              error
	lastSearchKeyword      string
	getUsersErr            error
	getUserBriefsErr       error
	findActiveErr          error
	listUserIDsErr         error
	listAvatarIDsErr       error
	countSuperAdminsErr    error
}

func (r *stubUserRepo) Insert(ctx context.Context, u model.User) error {
	if r.insertErr != nil {
		return r.insertErr
	}
	return r.fakeUserRepo.Insert(ctx, u)
}

func (r *stubUserRepo) ByID(ctx context.Context, id int64) (model.User, error) {
	if r.byIDErr != nil {
		return model.User{}, r.byIDErr
	}
	return r.fakeUserRepo.ByID(ctx, id)
}

func (r *stubUserRepo) ByIDForUpdate(ctx context.Context, id int64) (model.User, error) {
	if r.byIDForUpdateErr != nil {
		return model.User{}, r.byIDForUpdateErr
	}
	return r.fakeUserRepo.ByIDForUpdate(ctx, id)
}

func (r *stubUserRepo) ByUsername(ctx context.Context, username string) (model.User, error) {
	if r.byUsernameErr != nil {
		return model.User{}, r.byUsernameErr
	}
	return r.fakeUserRepo.ByUsername(ctx, username)
}

func (r *stubUserRepo) UpdateProfile(ctx context.Context, id int64, nickname, bio, avatarURL *string, at time.Time) (model.User, error) {
	if r.updateProfileErr != nil {
		return model.User{}, r.updateProfileErr
	}
	return r.fakeUserRepo.UpdateProfile(ctx, id, nickname, bio, avatarURL, at)
}

func (r *stubUserRepo) UpdateProfileFields(ctx context.Context, id int64, username, nickname, bio *string, at time.Time) (model.User, error) {
	if r.updateProfileFieldsErr != nil {
		return model.User{}, r.updateProfileFieldsErr
	}
	return r.fakeUserRepo.UpdateProfileFields(ctx, id, username, nickname, bio, at)
}

func (r *stubUserRepo) UpdateFollowApproval(ctx context.Context, id int64, enabled bool, at time.Time) (bool, error) {
	if r.updateFollowErr != nil {
		return false, r.updateFollowErr
	}
	return r.fakeUserRepo.UpdateFollowApproval(ctx, id, enabled, at)
}

func (r *stubUserRepo) UpdateDMPolicy(ctx context.Context, id int64, policy string, at time.Time) (string, error) {
	return r.fakeUserRepo.UpdateDMPolicy(ctx, id, policy, at)
}

func (r *stubUserRepo) UpdateRole(ctx context.Context, id int64, role string, at time.Time) (model.User, error) {
	if r.updateRoleErr != nil {
		return model.User{}, r.updateRoleErr
	}
	return r.fakeUserRepo.UpdateRole(ctx, id, role, at)
}

func (r *stubUserRepo) UpdateStatus(ctx context.Context, id int64, status string, suspendedAt *time.Time, tokenVersion int64, at time.Time) (model.User, error) {
	if r.updateStatusErr != nil {
		return model.User{}, r.updateStatusErr
	}
	return r.fakeUserRepo.UpdateStatus(ctx, id, status, suspendedAt, tokenVersion, at)
}

func (r *stubUserRepo) UpdatePassword(ctx context.Context, id int64, hash string, tokenVersion int64, at time.Time) error {
	if r.updatePasswordErr != nil {
		return r.updatePasswordErr
	}
	return r.fakeUserRepo.UpdatePassword(ctx, id, hash, tokenVersion, at)
}

func (r *stubUserRepo) UpdateAvatar(ctx context.Context, id int64, attachmentID *int64, avatarURL string, at time.Time) error {
	if r.updateAvatarErr != nil {
		return r.updateAvatarErr
	}
	return r.fakeUserRepo.UpdateAvatar(ctx, id, attachmentID, avatarURL, at)
}

func (r *stubUserRepo) Search(ctx context.Context, keyword string, cursor int64, limit int) ([]model.User, error) {
	r.lastSearchKeyword = keyword
	if r.searchErr != nil {
		return nil, r.searchErr
	}
	return r.fakeUserRepo.Search(ctx, keyword, cursor, limit)
}

func (r *stubUserRepo) CountSuperAdmins(ctx context.Context) (int64, error) {
	if r.countSuperAdminsErr != nil {
		return 0, r.countSuperAdminsErr
	}
	return r.fakeUserRepo.CountSuperAdmins(ctx)
}

func (r *stubUserRepo) GetUsers(ctx context.Context, ids []int64) (map[int64]model.User, error) {
	if r.getUsersErr != nil {
		return nil, r.getUsersErr
	}
	return r.fakeUserRepo.GetUsers(ctx, ids)
}

func (r *stubUserRepo) GetUserBriefs(ctx context.Context, ids []int64) (map[int64]model.UserBrief, error) {
	if r.getUserBriefsErr != nil {
		return nil, r.getUserBriefsErr
	}
	return r.fakeUserRepo.GetUserBriefs(ctx, ids)
}

func (r *stubUserRepo) FindActiveUsersByUsernames(ctx context.Context, usernames []string) ([]model.UserBrief, error) {
	if r.findActiveErr != nil {
		return nil, r.findActiveErr
	}
	return r.fakeUserRepo.FindActiveUsersByUsernames(ctx, usernames)
}

func (r *stubUserRepo) ListUserIDsByAIFlag(ctx context.Context, isAI bool) ([]int64, error) {
	if r.listUserIDsErr != nil {
		return nil, r.listUserIDsErr
	}
	return r.fakeUserRepo.ListUserIDsByAIFlag(ctx, isAI)
}

func (r *stubUserRepo) ListAvatarAttachmentIDs(ctx context.Context) ([]int64, error) {
	if r.listAvatarIDsErr != nil {
		return nil, r.listAvatarIDsErr
	}
	return r.fakeUserRepo.ListAvatarAttachmentIDs(ctx)
}

// sequenceHasHumanRepo 按次数返回预设的“是否已有人类账号”，用于覆盖注册事务内
// 二次复核分支（预检查看到空库、事务内看到已有用户）。
type sequenceHasHumanRepo struct {
	*fakeUserRepo
	has []bool
	idx int
}

func (r *sequenceHasHumanRepo) HasHumanUser(ctx context.Context) (bool, error) {
	if r.idx < len(r.has) {
		value := r.has[r.idx]
		r.idx++
		return value, nil
	}
	return r.fakeUserRepo.HasHumanUser(ctx)
}

// errorAfterHasHumanRepo 前 okCalls 次正常返回“空库”，之后返回错误，用于覆盖注册事务内
// 初始化状态读取失败的分支。
type errorAfterHasHumanRepo struct {
	*fakeUserRepo
	okCalls int
	calls   int
	err     error
}

func (r *errorAfterHasHumanRepo) HasHumanUser(ctx context.Context) (bool, error) {
	r.calls++
	if r.calls > r.okCalls {
		return false, r.err
	}
	return r.fakeUserRepo.HasHumanUser(ctx)
}

// stubSessionRepo 在 fakeSessionRepo 之上按方法注入错误。
type stubSessionRepo struct {
	*fakeSessionRepo
	insertErr          error
	lookupErr          error
	revokeErr          error
	revokeAllErr       error
	revokeAllExceptErr error
	updateVersionErr   error
}

func (r *stubSessionRepo) Insert(ctx context.Context, session model.Session, tokenHash string, createdAt time.Time) error {
	if r.insertErr != nil {
		return r.insertErr
	}
	return r.fakeSessionRepo.Insert(ctx, session, tokenHash, createdAt)
}

func (r *stubSessionRepo) ByTokenHash(ctx context.Context, tokenHash string) (model.User, model.Session, error) {
	if r.lookupErr != nil {
		return model.User{}, model.Session{}, r.lookupErr
	}
	return r.fakeSessionRepo.ByTokenHash(ctx, tokenHash)
}

func (r *stubSessionRepo) Revoke(ctx context.Context, sessionID int64, at time.Time) error {
	if r.revokeErr != nil {
		return r.revokeErr
	}
	return r.fakeSessionRepo.Revoke(ctx, sessionID, at)
}

func (r *stubSessionRepo) RevokeAllForUser(ctx context.Context, userID int64, at time.Time) error {
	if r.revokeAllErr != nil {
		return r.revokeAllErr
	}
	return r.fakeSessionRepo.RevokeAllForUser(ctx, userID, at)
}

func (r *stubSessionRepo) RevokeAllForUserExcept(ctx context.Context, userID, exceptSessionID int64, at time.Time) error {
	if r.revokeAllExceptErr != nil {
		return r.revokeAllExceptErr
	}
	return r.fakeSessionRepo.RevokeAllForUserExcept(ctx, userID, exceptSessionID, at)
}

func (r *stubSessionRepo) UpdateTokenVersion(ctx context.Context, sessionID, tokenVersion int64) error {
	if r.updateVersionErr != nil {
		return r.updateVersionErr
	}
	return r.fakeSessionRepo.UpdateTokenVersion(ctx, sessionID, tokenVersion)
}

// recordingSessionRepo 记录最近一次 Insert 的 createdAt 参数，用于断言登录写入的
// 会话 created_at 来自应用时钟；其余行为委托内存 fake。
type recordingSessionRepo struct {
	*fakeSessionRepo
	lastInsertAt *time.Time
}

func (r *recordingSessionRepo) Insert(ctx context.Context, session model.Session, tokenHash string, createdAt time.Time) error {
	at := createdAt
	r.lastInsertAt = &at
	return r.fakeSessionRepo.Insert(ctx, session, tokenHash, createdAt)
}

// failingIDs 让 ID 生成失败，覆盖登录/注册/创建 AI 账号的错误分支。
type failingIDs struct{}

func (failingIDs) Next() (int64, error) { return 0, errInjected }

// failingLocker 让注册 advisory lock 获取失败。
type failingLocker struct{}

func (failingLocker) AcquireBootstrapLock(context.Context) error { return errInjected }

// failingAudit 让审计写入失败，覆盖事务内审计回滚分支。
type failingAudit struct{}

func (failingAudit) Write(context.Context, contracts.AuditEntry) error { return errInjected }

// fakeStats 是 ProfileStatsReader 的 fake；err 非空时模拟统计读取失败。
type fakeStats struct {
	value ProfileStats
	err   error
}

func (f fakeStats) ProfileStats(context.Context, int64) (ProfileStats, error) {
	return f.value, f.err
}

// failingRelations 让关注状态读取失败，覆盖个人主页的资料组装错误分支。
type failingRelations struct{ err error }

func (f failingRelations) IsFollowing(context.Context, int64, int64) (bool, error) {
	return false, f.err
}

func (f failingRelations) FollowingSet(context.Context, int64) (map[int64]bool, error) {
	return nil, f.err
}

func (f failingRelations) FollowState(context.Context, int64, int64) (string, error) {
	return "", f.err
}
