package app

import (
	"context"
	"testing"
	"time"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/platform/clock"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// ===== 假端口（admin app 用例层的无数据库测试替身） =====

// fakeTx 直接执行回调，模拟已经开启的事务。
type fakeTx struct{}

func (fakeTx) WithTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

// fakeAudit 记录审计写入；auth/agent 的假端口也写同一实例，
// 这样测试能直接断言「一个管理动作只产生一条审计」。
type fakeAudit struct{ entries []contracts.AuditEntry }

func (f *fakeAudit) Write(_ context.Context, entry contracts.AuditEntry) error {
	f.entries = append(f.entries, entry)
	return nil
}

func (f *fakeAudit) actions() []string {
	actions := make([]string, 0, len(f.entries))
	for _, entry := range f.entries {
		actions = append(actions, entry.Action)
	}
	return actions
}

// fakeAuth 只实现 admin 用例实际调用的 auth 端口方法；未实现的方法由嵌入的接口兜底，
// 一旦被调用会以 nil 接口 panic，从而暴露用例越界调用。
type fakeAuth struct {
	contracts.AuthService
	audit *fakeAudit
	user  model.User

	getUserCalls int
	statusInput  *contracts.UserStatusInput
	resetInput   *contracts.ResetPasswordInput
}

func (f *fakeAuth) GetUser(_ context.Context, userID int64) (model.User, error) {
	f.getUserCalls++
	if f.user.ID == 0 || f.user.ID != userID {
		return model.User{}, apperr.NotFound("用户不存在")
	}
	return f.user, nil
}

// SetUserStatus 模拟 auth 端口：更新状态并在同一事务内写唯一一条审计。
func (f *fakeAuth) SetUserStatus(_ context.Context, in contracts.UserStatusInput) (model.User, error) {
	f.statusInput = &in
	updated := f.user
	action := contracts.AuditActionUserUnsuspend
	if in.Suspend {
		updated.Status = model.StatusSuspended
		action = contracts.AuditActionUserSuspend
	} else {
		updated.Status = model.StatusActive
	}
	f.user = updated
	targetID := in.TargetID
	return updated, f.audit.Write(context.Background(), contracts.AuditEntry{
		AdminID:    in.ActorID,
		Action:     action,
		TargetType: "user",
		TargetID:   &targetID,
		Detail:     map[string]any{"previous_status": updated.Status},
		At:         in.At,
	})
}

// ResetPassword 模拟 auth 端口：写唯一一条重置密码审计。
func (f *fakeAuth) ResetPassword(_ context.Context, in contracts.ResetPasswordInput) error {
	f.resetInput = &in
	targetID := in.TargetID
	return f.audit.Write(context.Background(), contracts.AuditEntry{
		AdminID:    in.ActorID,
		Action:     contracts.AuditActionUserResetPwd,
		TargetType: "user",
		TargetID:   &targetID,
		At:         in.At,
	})
}

// fakeSettings 是系统设置端口的可变内存实现。
type fakeSettings struct {
	values    contracts.SystemSettings
	webSearch contracts.WebSearchSettings
	sets      []contracts.SettingsWrite
}

func (f *fakeSettings) Settings(context.Context) (contracts.SystemSettings, error) {
	return f.values, nil
}

func (f *fakeSettings) WebSearch(context.Context) (contracts.WebSearchSettings, error) {
	return f.webSearch, nil
}

func (f *fakeSettings) Set(_ context.Context, in contracts.SettingsWrite) error {
	f.sets = append(f.sets, in)
	switch in.Key {
	case contracts.SettingKeyAIGlobalEnabled:
		if value, ok := in.Value.(bool); ok {
			f.values.AIGlobalEnabled = value
		}
	case contracts.SettingKeyRegistrationEnabled:
		if value, ok := in.Value.(bool); ok {
			f.values.RegistrationEnabled = value
		}
	case contracts.SettingKeyPostRateLimit:
		if value, ok := in.Value.(int); ok {
			f.values.PostRateLimit = value
		}
	case contracts.SettingKeyWebSearchBaseURL:
		if value, ok := in.Value.(string); ok {
			f.webSearch.BaseURL = value
		}
	case contracts.SettingKeyWebSearchAPIKeyCipher:
		if value, ok := in.Value.(string); ok {
			f.webSearch.APIKeyCipher = value
		}
	}
	return nil
}

func (f *fakeSettings) setKeys() []string {
	keys := make([]string, 0, len(f.sets))
	for _, item := range f.sets {
		keys = append(keys, item.Key)
	}
	return keys
}

// fakeGlobalAI 模拟 agent SetGlobal：写入唯一一条全局开关审计，并记录调用次数。
type fakeGlobalAI struct {
	audit       *fakeAudit
	haltCalls   int
	enableCalls int
}

func (f *fakeGlobalAI) HaltGlobalAI(_ context.Context, actorID int64, at time.Time) error {
	f.haltCalls++
	return f.audit.Write(context.Background(), contracts.AuditEntry{
		AdminID:    actorID,
		Action:     contracts.AuditActionGlobalAIStop,
		TargetType: "system_settings",
		At:         at,
	})
}

func (f *fakeGlobalAI) EnableGlobalAI(_ context.Context, actorID int64, at time.Time) error {
	f.enableCalls++
	return f.audit.Write(context.Background(), contracts.AuditEntry{
		AdminID:    actorID,
		Action:     contracts.AuditActionGlobalAIStart,
		TargetType: "system_settings",
		At:         at,
	})
}

// ===== 测试脚手架 =====

// adminTestEnv 是一次用例测试的 admin 服务与全部假端口。
type adminTestEnv struct {
	service  *Service
	audit    *fakeAudit
	auth     *fakeAuth
	settings *fakeSettings
	globalAI *fakeGlobalAI
	clock    *clock.Fake
}

func newAdminTestEnv(t *testing.T) *adminTestEnv {
	t.Helper()
	audit := &fakeAudit{}
	auth := &fakeAuth{
		audit: audit,
		user: model.User{
			ID: 2, Username: "target", Role: model.RoleUser,
			Status: model.StatusActive, TokenVersion: 1,
		},
	}
	settings := &fakeSettings{values: baseSettings()}
	globalAI := &fakeGlobalAI{audit: audit}
	clk := clock.NewFake()
	service := New(Deps{
		Tx:       fakeTx{},
		Clock:    clk,
		Audit:    audit,
		Auth:     auth,
		Settings: settings,
		GlobalAI: globalAI,
	})
	return &adminTestEnv{
		service: service, audit: audit, auth: auth,
		settings: settings, globalAI: globalAI, clock: clk,
	}
}

// adminActor 是发起管理动作的普通管理员。
func adminActor() model.Viewer {
	return model.Viewer{ID: 1, Authenticated: true, IsAdmin: true}
}

// ===== 用户处置：审计唯一写入方是 auth 端口 =====

func TestSetUserStatusAuditWrittenOnceByAuthPort(t *testing.T) {
	t.Run("停用", func(t *testing.T) {
		env := newAdminTestEnv(t)
		updated, err := env.service.SuspendUser(context.Background(), adminActor(), env.auth.user.ID)
		if err != nil {
			t.Fatalf("停用账号失败: %v", err)
		}
		if updated.Status != model.StatusSuspended {
			t.Fatalf("返回状态 = %q，期望 suspended", updated.Status)
		}
		if env.auth.statusInput == nil || !env.auth.statusInput.Suspend {
			t.Fatalf("auth 端口未收到停用调用: %+v", env.auth.statusInput)
		}
		if env.auth.statusInput.ActorID != adminActor().ID || env.auth.statusInput.TargetID != env.auth.user.ID {
			t.Fatalf("auth 端口入参不符: %+v", env.auth.statusInput)
		}
		entries := env.audit.entries
		if len(entries) != 1 || entries[0].Action != contracts.AuditActionUserSuspend {
			t.Fatalf("停用应只由 auth 端口写一条 user_suspend 审计，实际: %v", env.audit.actions())
		}
		if entries[0].AdminID != adminActor().ID || entries[0].TargetID == nil || *entries[0].TargetID != env.auth.user.ID {
			t.Fatalf("审计主体/目标不符: %+v", entries[0])
		}
	})

	t.Run("启用", func(t *testing.T) {
		env := newAdminTestEnv(t)
		env.auth.user.Status = model.StatusSuspended
		updated, err := env.service.UnsuspendUser(context.Background(), adminActor(), env.auth.user.ID)
		if err != nil {
			t.Fatalf("启用账号失败: %v", err)
		}
		if updated.Status != model.StatusActive {
			t.Fatalf("返回状态 = %q，期望 active", updated.Status)
		}
		entries := env.audit.entries
		if len(entries) != 1 || entries[0].Action != contracts.AuditActionUserUnsuspend {
			t.Fatalf("启用应只由 auth 端口写一条 user_unsuspend 审计，实际: %v", env.audit.actions())
		}
	})
}

func TestSetUserStatusRejectsSuperAdminAndSelf(t *testing.T) {
	t.Run("普通管理员不能操作超管", func(t *testing.T) {
		env := newAdminTestEnv(t)
		env.auth.user.Role = model.RoleAdmin
		env.auth.user.IsSuperAdmin = true
		if _, err := env.service.SuspendUser(context.Background(), adminActor(), env.auth.user.ID); !apperr.IsKind(err, apperr.KindForbidden) {
			t.Fatalf("期望 forbidden，实际: %v", err)
		}
		if env.auth.statusInput != nil || len(env.audit.entries) != 0 {
			t.Fatalf("越权请求不应调用 auth 端口或写审计: %+v / %v", env.auth.statusInput, env.audit.actions())
		}
	})

	t.Run("不能停用自己", func(t *testing.T) {
		env := newAdminTestEnv(t)
		actor := adminActor()
		env.auth.user.ID = actor.ID
		env.auth.user.Role = model.RoleAdmin
		if _, err := env.service.SuspendUser(context.Background(), actor, actor.ID); !apperr.IsKind(err, apperr.KindValidation) {
			t.Fatalf("期望 validation，实际: %v", err)
		}
		if env.auth.statusInput != nil || len(env.audit.entries) != 0 {
			t.Fatalf("自停用不应调用 auth 端口或写审计")
		}
	})
}

func TestResetPasswordAuditWrittenOnceByAuthPort(t *testing.T) {
	env := newAdminTestEnv(t)
	if err := env.service.ResetPassword(context.Background(), adminActor(), env.auth.user.ID, "new-password-123"); err != nil {
		t.Fatalf("重置密码失败: %v", err)
	}
	if env.auth.resetInput == nil || env.auth.resetInput.NewPassword != "new-password-123" {
		t.Fatalf("auth 端口未收到重置密码调用: %+v", env.auth.resetInput)
	}
	entries := env.audit.entries
	if len(entries) != 1 || entries[0].Action != contracts.AuditActionUserResetPwd {
		t.Fatalf("重置密码应只由 auth 端口写一条 user_reset_password 审计，实际: %v", env.audit.actions())
	}
}

func TestResetPasswordRejectsAIAccount(t *testing.T) {
	env := newAdminTestEnv(t)
	env.auth.user.IsAI = true
	err := env.service.ResetPassword(context.Background(), adminActor(), env.auth.user.ID, "new-password-123")
	if !apperr.IsKind(err, apperr.KindValidation) {
		t.Fatalf("AI 账号重置密码期望 validation，实际: %v", err)
	}
	if env.auth.resetInput != nil || len(env.audit.entries) != 0 {
		t.Fatalf("AI 账号不应调用 auth 端口或写审计")
	}
}

// ===== 系统设置：全局 AI 开关审计唯一写入方是 agent 端口 =====

func TestUpdateSettingsGlobalAIAuditWrittenOnceByAgentPort(t *testing.T) {
	t.Run("关闭", func(t *testing.T) {
		env := newAdminTestEnv(t)
		env.settings.values.AIGlobalEnabled = true
		disabled := false
		snapshot, err := env.service.UpdateSettings(context.Background(), adminActor(),
			SettingsPatch{AIGlobalEnabled: &disabled})
		if err != nil {
			t.Fatalf("关闭全局 AI 失败: %v", err)
		}
		if snapshot.AIGlobalEnabled {
			t.Fatal("返回快照仍为开启")
		}
		if env.globalAI.haltCalls != 1 || env.globalAI.enableCalls != 0 {
			t.Fatalf("应调用 agent 端口停用一次: halt=%d enable=%d",
				env.globalAI.haltCalls, env.globalAI.enableCalls)
		}
		entries := env.audit.entries
		if len(entries) != 1 || entries[0].Action != contracts.AuditActionGlobalAIStop {
			t.Fatalf("关闭全局 AI 应只由 agent 端口写一条 ai_global_stop 审计，实际: %v", env.audit.actions())
		}
		if keys := env.settings.setKeys(); len(keys) != 1 || keys[0] != contracts.SettingKeyAIGlobalEnabled {
			t.Fatalf("应只写全局 AI 开关设置，实际: %v", keys)
		}
	})

	t.Run("开启", func(t *testing.T) {
		env := newAdminTestEnv(t)
		enabled := true
		snapshot, err := env.service.UpdateSettings(context.Background(), adminActor(),
			SettingsPatch{AIGlobalEnabled: &enabled})
		if err != nil {
			t.Fatalf("开启全局 AI 失败: %v", err)
		}
		if !snapshot.AIGlobalEnabled {
			t.Fatal("返回快照未生效")
		}
		if env.globalAI.enableCalls != 1 || env.globalAI.haltCalls != 0 {
			t.Fatalf("应调用 agent 端口开启一次: halt=%d enable=%d",
				env.globalAI.haltCalls, env.globalAI.enableCalls)
		}
		entries := env.audit.entries
		if len(entries) != 1 || entries[0].Action != contracts.AuditActionGlobalAIStart {
			t.Fatalf("开启全局 AI 应只由 agent 端口写一条 ai_global_start 审计，实际: %v", env.audit.actions())
		}
	})
}

func TestUpdateSettingsRegistrationAuditStaysInAdmin(t *testing.T) {
	env := newAdminTestEnv(t)
	disabled := false
	if _, err := env.service.UpdateSettings(context.Background(), adminActor(),
		SettingsPatch{RegistrationEnabled: &disabled}); err != nil {
		t.Fatalf("修改注册开关失败: %v", err)
	}
	entries := env.audit.entries
	if len(entries) != 1 || entries[0].Action != contracts.AuditActionRegistrationUpdate {
		t.Fatalf("注册开关审计应仍由 admin 写一条，实际: %v", env.audit.actions())
	}
}

func TestUpdateSettingsNumericAuditStaysInAdmin(t *testing.T) {
	env := newAdminTestEnv(t)
	limit := 10
	if _, err := env.service.UpdateSettings(context.Background(), adminActor(),
		SettingsPatch{PostRateLimit: &limit}); err != nil {
		t.Fatalf("修改发帖限频失败: %v", err)
	}
	entries := env.audit.entries
	if len(entries) != 1 || entries[0].Action != contracts.AuditActionSettingsUpdate {
		t.Fatalf("数值设置审计应仍由 admin 写一条 settings_update，实际: %v", env.audit.actions())
	}
}

// ===== 网页搜索设置：审计动作保持旧契约 settings_update =====

func TestUpdateWebSearchSettingsUsesSettingsUpdateAuditAction(t *testing.T) {
	env := newAdminTestEnv(t)
	env.service.cipher = fakeCipher{}
	snapshot, err := env.service.UpdateWebSearchSettings(context.Background(), adminActor(),
		WebSearchPatch{APIKey: ptr("search-secret")})
	if err != nil {
		t.Fatalf("保存网页搜索密钥失败: %v", err)
	}
	if !snapshot.Configured {
		t.Fatal("保存后应标记已配置")
	}
	entries := env.audit.entries
	if len(entries) != 1 {
		t.Fatalf("应写入一条审计，实际: %v", env.audit.actions())
	}
	if entries[0].Action != contracts.AuditActionSettingsUpdate {
		t.Fatalf("网页搜索设置审计动作应为 settings_update，实际: %q", entries[0].Action)
	}
	if entries[0].TargetType != "web_search_settings" {
		t.Fatalf("审计 target_type 不符: %q", entries[0].TargetType)
	}
}
