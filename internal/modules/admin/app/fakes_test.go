package app

// 本文件集中放置 admin app 无数据库单测使用的扩展假端口：
// fakeTx/fakeAudit/fakeAuth/fakeSettings/fakeGlobalAI 已在 service_test.go 中定义，
// 这里只补充任务处置、用户停用编排、内容处置与错误注入所需的替身，避免重复声明。

import (
	"context"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// failingAudit 固定返回指定错误，用于断言审计失败时用例向上返回错误（事务回滚语义）。
type failingAudit struct{ err error }

func (f failingAudit) Write(context.Context, contracts.AuditEntry) error { return f.err }

// fakeJobs 是后台作业端口的可记录假实现；嵌入接口使未实现方法一旦被调用即 panic，
// 从而暴露用例越界调用。
type fakeJobs struct {
	contracts.JobsQueue

	job    contracts.JobSnapshot
	getErr error

	retryErr  error
	cancelErr error
	pauseErr  error

	getCalls int
	retried  []contracts.JobActionInput
	canceled []contracts.JobActionInput
	paused   []contracts.PausePendingJobsInput
}

func (f *fakeJobs) GetJob(_ context.Context, jobID int64) (contracts.JobSnapshot, error) {
	f.getCalls++
	if f.getErr != nil {
		return contracts.JobSnapshot{}, f.getErr
	}
	current := f.job
	current.ID = jobID
	return current, nil
}

func (f *fakeJobs) RetryJob(_ context.Context, in contracts.JobActionInput) (contracts.JobSnapshot, error) {
	f.retried = append(f.retried, in)
	if f.retryErr != nil {
		return contracts.JobSnapshot{}, f.retryErr
	}
	updated := f.job
	updated.ID = in.JobID
	updated.Status = contracts.JobStatusPending
	updated.Attempts = 0
	updated.UpdatedAt = in.At
	return updated, nil
}

func (f *fakeJobs) CancelJob(_ context.Context, in contracts.JobActionInput) (contracts.JobSnapshot, error) {
	f.canceled = append(f.canceled, in)
	if f.cancelErr != nil {
		return contracts.JobSnapshot{}, f.cancelErr
	}
	updated := f.job
	updated.ID = in.JobID
	updated.Status = contracts.JobStatusCanceled
	updated.UpdatedAt = in.At
	return updated, nil
}

func (f *fakeJobs) PausePending(_ context.Context, in contracts.PausePendingJobsInput) (int, error) {
	f.paused = append(f.paused, in)
	if f.pauseErr != nil {
		return 0, f.pauseErr
	}
	return len(in.RefIDs), nil
}

// fakeContent 是 content 端口的最小假实现：只实现 admin 用例实际调用的方法。
type fakeContent struct {
	contracts.ContentService

	postIDs      []int64
	postIDsErr   error
	listCalls    int
	listAuthorID int64

	deleteErr error
	deleted   []contracts.DeletePostInput
}

func (f *fakeContent) ListPostIDsByAuthor(_ context.Context, authorID int64) ([]int64, error) {
	f.listCalls++
	f.listAuthorID = authorID
	if f.postIDsErr != nil {
		return nil, f.postIDsErr
	}
	return f.postIDs, nil
}

func (f *fakeContent) DeletePostAsAdmin(_ context.Context, in contracts.DeletePostInput) error {
	f.deleted = append(f.deleted, in)
	return f.deleteErr
}

// fakeSocial 是 social 端口的最小假实现。
type fakeSocial struct {
	contracts.SocialService

	deleteErr error
	deleted   []contracts.DeleteCommentInput
}

func (f *fakeSocial) DeleteCommentAsAdmin(_ context.Context, in contracts.DeleteCommentInput) error {
	f.deleted = append(f.deleted, in)
	return f.deleteErr
}

// fakeMedia 是 media 端口的最小假实现。
type fakeMedia struct {
	contracts.MediaService

	deleteErr error
	deleted   []contracts.DeleteAttachmentInput
}

func (f *fakeMedia) DeleteAttachmentAsAdmin(_ context.Context, in contracts.DeleteAttachmentInput) error {
	f.deleted = append(f.deleted, in)
	return f.deleteErr
}

// fakeAgent 是 agent 端口的最小假实现（停用 AI 账号时取消待执行任务）。
type fakeAgent struct {
	contracts.AgentService

	err      error
	canceled []contracts.CancelTasksInput
}

func (f *fakeAgent) CancelPendingTasksForActor(_ context.Context, in contracts.CancelTasksInput) (int, error) {
	f.canceled = append(f.canceled, in)
	if f.err != nil {
		return 0, f.err
	}
	return 1, nil
}

// errorAuth 在 fakeAuth 之上按需注入错误，用于覆盖端口失败分支。
type errorAuth struct {
	*fakeAuth

	getUserErr error
	statusErr  error
	resetErr   error
}

func (f *errorAuth) GetUser(ctx context.Context, userID int64) (model.User, error) {
	if f.getUserErr != nil {
		return model.User{}, f.getUserErr
	}
	return f.fakeAuth.GetUser(ctx, userID)
}

func (f *errorAuth) SetUserStatus(ctx context.Context, in contracts.UserStatusInput) (model.User, error) {
	if f.statusErr != nil {
		return model.User{}, f.statusErr
	}
	return f.fakeAuth.SetUserStatus(ctx, in)
}

func (f *errorAuth) ResetPassword(ctx context.Context, in contracts.ResetPasswordInput) error {
	if f.resetErr != nil {
		return f.resetErr
	}
	return f.fakeAuth.ResetPassword(ctx, in)
}

// errGlobalAI 固定返回指定错误，用于覆盖全局 AI 开关编排失败分支。
type errGlobalAI struct{ err error }

func (f errGlobalAI) HaltGlobalAI(context.Context, int64, time.Time) error   { return f.err }
func (f errGlobalAI) EnableGlobalAI(context.Context, int64, time.Time) error { return f.err }

// scriptedSettings 按调用序号注入读/写错误，用于覆盖 settings 端口的失败分支。
// values 默认使用通过校验的基线设置，避免业务校验先失败。
type scriptedSettings struct {
	values contracts.SystemSettings

	settingsErrs  []error // 第 n 次 Settings 调用返回的错误；索引缺失或 nil 表示成功
	webSearchErrs []error
	setErr        error
	setKeyErrs    map[string]error // 按设置键注入写库错误，优先于 setErr

	settingsCalls  int
	webSearchCalls int
	sets           []contracts.SettingsWrite
}

func (f *scriptedSettings) Settings(context.Context) (contracts.SystemSettings, error) {
	call := f.settingsCalls
	f.settingsCalls++
	if call < len(f.settingsErrs) && f.settingsErrs[call] != nil {
		return contracts.SystemSettings{}, f.settingsErrs[call]
	}
	return f.values, nil
}

func (f *scriptedSettings) WebSearch(context.Context) (contracts.WebSearchSettings, error) {
	call := f.webSearchCalls
	f.webSearchCalls++
	if call < len(f.webSearchErrs) && f.webSearchErrs[call] != nil {
		return contracts.WebSearchSettings{}, f.webSearchErrs[call]
	}
	return contracts.WebSearchSettings{}, nil
}

func (f *scriptedSettings) Set(_ context.Context, in contracts.SettingsWrite) error {
	f.sets = append(f.sets, in)
	if err := f.setKeyErrs[in.Key]; err != nil {
		return err
	}
	return f.setErr
}

func (f *scriptedSettings) setKeys() []string {
	keys := make([]string, 0, len(f.sets))
	for _, item := range f.sets {
		keys = append(keys, item.Key)
	}
	return keys
}

// detailOf 把审计 Detail 断言为 map[string]any，便于按字段断言。
func detailOf(t *testing.T, detail any) map[string]any {
	t.Helper()
	values, ok := detail.(map[string]any)
	if !ok {
		t.Fatalf("审计 Detail 类型 = %T，期望 map[string]any", detail)
	}
	return values
}
