package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// newPendingJobsTestEnv 在基础用例环境上装配用户停用编排所需的端口假实现。
func newPendingJobsTestEnv(t *testing.T) (*adminTestEnv, *fakeContent, *fakeJobs, *fakeAgent) {
	t.Helper()
	env := newAdminTestEnv(t)
	content := &fakeContent{postIDs: []int64{101, 102}}
	jobs := &fakeJobs{}
	agent := &fakeAgent{}
	env.service.content = content
	env.service.jobs = jobs
	env.service.agent = agent
	return env, content, jobs, agent
}

// ===== 停用账号：编排待执行作业 =====

func TestSuspendUserPausesPendingPostJobs(t *testing.T) {
	env, content, jobs, agent := newPendingJobsTestEnv(t)
	env.clock.Advance(2 * time.Minute)
	now := env.clock.Now()
	targetID := env.auth.user.ID

	if _, err := env.service.SuspendUser(context.Background(), adminActor(), targetID); err != nil {
		t.Fatalf("停用账号失败: %v", err)
	}
	if content.listCalls != 1 || content.listAuthorID != targetID {
		t.Fatalf("应查询目标账号的帖子，实际: calls=%d author=%d", content.listCalls, content.listAuthorID)
	}
	if len(jobs.paused) != 1 {
		t.Fatalf("人类账号应只暂停定时发布作业，实际: %+v", jobs.paused)
	}
	paused := jobs.paused[0]
	if paused.JobType != contracts.JobTypePostPublish || paused.RefType != "post" ||
		len(paused.RefIDs) != 2 || paused.RefIDs[0] != 101 || paused.RefIDs[1] != 102 || !paused.At.Equal(now) {
		t.Fatalf("PausePending 入参不符: %+v", paused)
	}
	if len(agent.canceled) != 0 {
		t.Fatalf("人类账号不应取消 AI 任务: %+v", agent.canceled)
	}
	// 账号启停审计由 auth 端口写一条，admin 不重复写。
	entries := env.audit.entries
	if len(entries) != 1 || entries[0].Action != contracts.AuditActionUserSuspend {
		t.Fatalf("应只由 auth 端口写一条 user_suspend 审计，实际: %v", env.audit.actions())
	}
}

func TestSuspendUserAIAccountPausesAIJobsAndCancelsTasks(t *testing.T) {
	env, content, jobs, agent := newPendingJobsTestEnv(t)
	env.auth.user.IsAI = true
	env.clock.Advance(time.Hour)
	now := env.clock.Now()
	targetID := env.auth.user.ID

	if _, err := env.service.SuspendUser(context.Background(), adminActor(), targetID); err != nil {
		t.Fatalf("停用 AI 账号失败: %v", err)
	}
	if content.listCalls != 1 {
		t.Fatalf("应先暂停 AI 账号帖子的定时发布作业")
	}
	if len(jobs.paused) != 4 {
		t.Fatalf("AI 账号应暂停 1 条帖子作业 + 3 类 AI 作业，实际: %+v", jobs.paused)
	}
	if jobs.paused[0].JobType != contracts.JobTypePostPublish {
		t.Fatalf("第一项应为定时发布作业: %+v", jobs.paused[0])
	}
	wantTypes := []string{
		contracts.JobTypeAITask,
		contracts.JobTypeAISchedule,
		contracts.JobTypeMemoryConsolidate,
	}
	for i, wantType := range wantTypes {
		item := jobs.paused[i+1]
		if item.JobType != wantType || item.RefType != "ai" ||
			len(item.RefIDs) != 1 || item.RefIDs[0] != targetID || !item.At.Equal(now) {
			t.Fatalf("第 %d 项 AI 作业暂停入参不符: %+v", i+1, item)
		}
	}
	if len(agent.canceled) != 1 {
		t.Fatalf("停用 AI 账号应取消其待执行任务: %+v", agent.canceled)
	}
	canceled := agent.canceled[0]
	if canceled.ActorID != adminActor().ID || canceled.AIID != targetID || !canceled.At.Equal(now) {
		t.Fatalf("CancelPendingTasksForActor 入参不符: %+v", canceled)
	}
}

func TestSuspendUserSkipsPauseWhenJobsPortMissing(t *testing.T) {
	env := newAdminTestEnv(t)
	content := &fakeContent{postIDs: []int64{7}}
	env.service.content = content
	env.service.jobs = nil

	if _, err := env.service.SuspendUser(context.Background(), adminActor(), env.auth.user.ID); err != nil {
		t.Fatalf("停用账号失败: %v", err)
	}
	// content 与 jobs 是组合依赖：jobs 缺失时不应查询帖子，也不应 panic。
	if content.listCalls != 0 {
		t.Fatalf("jobs 端口缺失时不应查询帖子，实际调用 %d 次", content.listCalls)
	}
}

func TestSuspendUserAIPausesAIJobsWithoutContentPort(t *testing.T) {
	env := newAdminTestEnv(t)
	jobs := &fakeJobs{}
	agent := &fakeAgent{}
	env.service.content = nil
	env.service.jobs = jobs
	env.service.agent = agent
	env.auth.user.IsAI = true

	if _, err := env.service.SuspendUser(context.Background(), adminActor(), env.auth.user.ID); err != nil {
		t.Fatalf("停用 AI 账号失败: %v", err)
	}
	// 未装配 content 端口时跳过帖子相关暂停，但仍应处理 AI 作业与任务。
	if len(jobs.paused) != 3 {
		t.Fatalf("应暂停 3 类 AI 作业，实际: %+v", jobs.paused)
	}
	if len(agent.canceled) != 1 {
		t.Fatalf("应取消 AI 待执行任务，实际: %+v", agent.canceled)
	}
}

func TestSuspendUserAISkipsAgentWhenMissing(t *testing.T) {
	env := newAdminTestEnv(t)
	jobs := &fakeJobs{}
	env.service.jobs = jobs
	env.service.agent = nil
	env.auth.user.IsAI = true

	if _, err := env.service.SuspendUser(context.Background(), adminActor(), env.auth.user.ID); err != nil {
		t.Fatalf("停用 AI 账号失败: %v", err)
	}
	if len(jobs.paused) != 3 {
		t.Fatalf("应暂停 3 类 AI 作业，实际: %+v", jobs.paused)
	}
}

func TestSuspendUserPendingJobErrorsArePropagated(t *testing.T) {
	t.Run("查询帖子失败", func(t *testing.T) {
		env, content, jobs, _ := newPendingJobsTestEnv(t)
		sentinel := errors.New("查询帖子失败")
		content.postIDsErr = sentinel

		if _, err := env.service.SuspendUser(context.Background(), adminActor(), env.auth.user.ID); !errors.Is(err, sentinel) {
			t.Fatalf("期望透传端口错误，实际: %v", err)
		}
		if len(jobs.paused) != 0 {
			t.Fatalf("查询失败时不应继续暂停作业")
		}
	})

	t.Run("暂停作业失败", func(t *testing.T) {
		env, _, jobs, _ := newPendingJobsTestEnv(t)
		sentinel := errors.New("暂停作业失败")
		jobs.pauseErr = sentinel

		if _, err := env.service.SuspendUser(context.Background(), adminActor(), env.auth.user.ID); !errors.Is(err, sentinel) {
			t.Fatalf("期望透传端口错误，实际: %v", err)
		}
		if len(jobs.paused) != 1 {
			t.Fatalf("失败前应已尝试暂停一次，实际: %+v", jobs.paused)
		}
	})

	t.Run("取消 AI 任务失败", func(t *testing.T) {
		env, _, _, agent := newPendingJobsTestEnv(t)
		env.auth.user.IsAI = true
		sentinel := errors.New("取消任务失败")
		agent.err = sentinel

		if _, err := env.service.SuspendUser(context.Background(), adminActor(), env.auth.user.ID); !errors.Is(err, sentinel) {
			t.Fatalf("期望透传端口错误，实际: %v", err)
		}
		if len(agent.canceled) != 1 {
			t.Fatalf("失败前应已尝试取消任务，实际: %+v", agent.canceled)
		}
	})
}

func TestSuspendUserRejectsNonAdminActors(t *testing.T) {
	cases := []struct {
		name  string
		actor model.Viewer
	}{
		{"游客", model.GuestViewer()},
		{"普通用户", model.Viewer{ID: 3, Authenticated: true}},
		{"AI 管理员", model.Viewer{ID: 4, Authenticated: true, IsAdmin: true, IsAI: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newAdminTestEnv(t)
			if _, err := env.service.SuspendUser(context.Background(), tc.actor, env.auth.user.ID); !apperr.IsKind(err, apperr.KindForbidden) {
				t.Fatalf("期望 forbidden，实际: %v", err)
			}
			if env.auth.statusInput != nil || len(env.audit.entries) != 0 {
				t.Fatalf("越权请求不应调用 auth 端口或写审计")
			}
		})
	}
}

func TestSuspendUserGetUserErrorIsPropagated(t *testing.T) {
	env := newAdminTestEnv(t)
	if _, err := env.service.SuspendUser(context.Background(), adminActor(), 99); !apperr.IsKind(err, apperr.KindNotFound) {
		t.Fatalf("期望 not_found，实际: %v", err)
	}
	if env.auth.statusInput != nil || len(env.audit.entries) != 0 {
		t.Fatalf("目标不存在时不应更新状态或写审计")
	}
}

func TestSuspendUserStatusPortErrorIsPropagated(t *testing.T) {
	env := newAdminTestEnv(t)
	sentinel := errors.New("更新状态失败")
	env.service.auth = &errorAuth{fakeAuth: env.auth, statusErr: sentinel}

	if _, err := env.service.SuspendUser(context.Background(), adminActor(), env.auth.user.ID); !errors.Is(err, sentinel) {
		t.Fatalf("期望透传端口错误，实际: %v", err)
	}
}

func TestUnsuspendUserDoesNotPausePendingJobs(t *testing.T) {
	env, content, jobs, _ := newPendingJobsTestEnv(t)
	env.auth.user.Status = model.StatusSuspended

	updated, err := env.service.UnsuspendUser(context.Background(), adminActor(), env.auth.user.ID)
	if err != nil {
		t.Fatalf("启用账号失败: %v", err)
	}
	if updated.Status != model.StatusActive {
		t.Fatalf("返回状态 = %q，期望 active", updated.Status)
	}
	if content.listCalls != 0 || len(jobs.paused) != 0 {
		t.Fatalf("启用账号不应暂停待执行作业（不自动补发）")
	}
	entries := env.audit.entries
	if len(entries) != 1 || entries[0].Action != contracts.AuditActionUserUnsuspend {
		t.Fatalf("应只由 auth 端口写一条 user_unsuspend 审计，实际: %v", env.audit.actions())
	}
}

// ===== 重置密码 =====

func TestResetPasswordRejectsNonAdminActor(t *testing.T) {
	env := newAdminTestEnv(t)
	actor := model.Viewer{ID: 3, Authenticated: true}

	if err := env.service.ResetPassword(context.Background(), actor, env.auth.user.ID, "new-password-123"); !apperr.IsKind(err, apperr.KindForbidden) {
		t.Fatalf("期望 forbidden，实际: %v", err)
	}
	if env.auth.resetInput != nil || len(env.audit.entries) != 0 {
		t.Fatalf("越权请求不应调用 auth 端口或写审计")
	}
}

func TestResetPasswordRejectsSuperAdminTarget(t *testing.T) {
	env := newAdminTestEnv(t)
	env.auth.user.Role = model.RoleAdmin
	env.auth.user.IsSuperAdmin = true

	if err := env.service.ResetPassword(context.Background(), adminActor(), env.auth.user.ID, "new-password-123"); !apperr.IsKind(err, apperr.KindForbidden) {
		t.Fatalf("普通管理员重置超管密码期望 forbidden，实际: %v", err)
	}
	if env.auth.resetInput != nil {
		t.Fatalf("不应调用 auth 端口")
	}
}

func TestResetPasswordGetUserErrorIsPropagated(t *testing.T) {
	env := newAdminTestEnv(t)
	if err := env.service.ResetPassword(context.Background(), adminActor(), 99, "new-password-123"); !apperr.IsKind(err, apperr.KindNotFound) {
		t.Fatalf("期望 not_found，实际: %v", err)
	}
}

func TestResetPasswordPortErrorIsPropagated(t *testing.T) {
	env := newAdminTestEnv(t)
	sentinel := errors.New("重置密码失败")
	env.service.auth = &errorAuth{fakeAuth: env.auth, resetErr: sentinel}

	if err := env.service.ResetPassword(context.Background(), adminActor(), env.auth.user.ID, "new-password-123"); !errors.Is(err, sentinel) {
		t.Fatalf("期望透传端口错误，实际: %v", err)
	}
	if len(env.audit.entries) != 0 {
		t.Fatalf("端口失败时不应写审计")
	}
}

func TestResetPasswordUsesClockActorAndTarget(t *testing.T) {
	env := newAdminTestEnv(t)
	env.clock.Advance(45 * time.Second)
	now := env.clock.Now()

	if err := env.service.ResetPassword(context.Background(), adminActor(), env.auth.user.ID, "new-password-123"); err != nil {
		t.Fatalf("重置密码失败: %v", err)
	}
	if env.auth.resetInput == nil {
		t.Fatal("auth 端口未收到重置密码调用")
	}
	input := *env.auth.resetInput
	if input.ActorID != adminActor().ID || input.TargetID != env.auth.user.ID ||
		input.NewPassword != "new-password-123" || !input.At.Equal(now) {
		t.Fatalf("ResetPassword 入参不符: %+v", input)
	}
}
