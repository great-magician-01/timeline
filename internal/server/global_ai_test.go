package server_test

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"github.com/great-magician-01/timeline/internal/testkit"
)

// TestGlobalAISwitchIntegration 覆盖全局 AI 开关的 HTTP 路径：
// 开启后读取为真；紧急停止会取消待执行的 AI 任务、暂停 AI 后台作业并写入审计。
// 全局开关的开启/停止是两个独立实现（agent 与 admin），这里都走真实路由验证。
func TestGlobalAISwitchIntegration(t *testing.T) {
	srv := testkit.NewIntegrationServer(t)
	ctx := context.Background()

	admin := srv.Client(t)
	admin.LoginAndSetCSRF(testkit.IntegrationAdminUsername, testkit.IntegrationAdminPassword)

	// 开启
	status, _, payload, raw := admin.Call(http.MethodPost, "/api/v1/admin/ai/global-start", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if enabled, _ := payload["ai_global_enabled"].(bool); !enabled {
		t.Fatalf("global-start 后应为开启: %v", payload)
	}
	status, _, settings, raw := admin.Call(http.MethodGet, "/api/v1/admin/settings", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if enabled, _ := settings["ai_global_enabled"].(bool); !enabled {
		t.Fatalf("系统设置应读到全局开关为开启: %v", settings)
	}

	// 准备一个待执行任务与一个待执行作业，验证紧急停止的连带效果。
	// 主键由测试环境雪花生成器产生，保证重复运行不会与上一轮数据冲突（测试 schema 不做物理删除）。
	at := srv.Clock.Now()
	aiID, err := srv.IDs.Next()
	if err != nil {
		t.Fatalf("生成 AI 账号 ID 失败: %v", err)
	}
	taskID, err := srv.IDs.Next()
	if err != nil {
		t.Fatalf("生成任务 ID 失败: %v", err)
	}
	jobID, err := srv.IDs.Next()
	if err != nil {
		t.Fatalf("生成作业 ID 失败: %v", err)
	}
	suffix := testkit.IntegrationShortSuffix()
	if _, err := srv.Pool.Exec(ctx, `
		INSERT INTO users (
			id, username, nickname, role, is_ai, status, token_version, created_at, updated_at
		) VALUES ($1, $2, $3, 'user', TRUE, 'active', 1, $4, $4)`,
		aiID, "gai_ai_"+suffix, "全局开关 AI", at); err != nil {
		t.Fatalf("准备 AI 账号失败: %v", err)
	}
	if _, err := srv.Pool.Exec(ctx, `
		INSERT INTO ai_tasks (id, ai_id, trigger_type, status, idempotency_key, created_at)
		VALUES ($1, $2, 'schedule', 'pending', $3, $4)`,
		taskID, aiID, "gai-task-"+suffix, at); err != nil {
		t.Fatalf("准备待执行任务失败: %v", err)
	}
	if _, err := srv.Pool.Exec(ctx, `
		INSERT INTO background_jobs (
			id, job_type, ref_type, ref_id, status, run_at, idempotency_key, created_at, updated_at
		) VALUES ($1, 'ai_schedule', 'ai', $2, 'pending', $3, $4, $3, $3)`,
		jobID, aiID, at, "gai-job-"+suffix); err != nil {
		t.Fatalf("准备待执行作业失败: %v", err)
	}

	// 紧急停止
	status, _, payload, raw = admin.Call(http.MethodPost, "/api/v1/admin/ai/global-stop", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if enabled, _ := payload["ai_global_enabled"].(bool); enabled {
		t.Fatalf("global-stop 后应为关闭: %v", payload)
	}

	var taskStatus, jobStatus string
	if err := srv.Pool.QueryRow(ctx, `SELECT status FROM ai_tasks WHERE id = $1`, taskID).Scan(&taskStatus); err != nil {
		t.Fatal(err)
	}
	if taskStatus != "canceled" {
		t.Fatalf("紧急停止应取消待执行任务，实际 %s", taskStatus)
	}
	if err := srv.Pool.QueryRow(ctx, `SELECT status FROM background_jobs WHERE id = $1`, jobID).Scan(&jobStatus); err != nil {
		t.Fatal(err)
	}
	if jobStatus != "paused" {
		t.Fatalf("紧急停止应暂停 AI 作业，实际 %s", jobStatus)
	}

	// 审计：全局停止必须留痕（只断言动作类型，避免耦合 detail 字段形状）。
	var auditCount int
	if err := srv.Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM admin_audit_logs WHERE action_type = 'ai_global_stop'`,
	).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount == 0 {
		t.Fatal("全局停止未写入审计记录")
	}

	// 关闭状态下的开关读取（由 admin 包实现）应一致
	status, _, settings, raw = admin.Call(http.MethodGet, "/api/v1/admin/settings", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if enabled, _ := settings["ai_global_enabled"].(bool); enabled {
		t.Fatalf("系统设置应读到全局开关为关闭: %v", settings)
	}

	// 收尾：把测试遗留的作业置为取消，避免影响后续运行（逻辑状态，不物理删除）。
	if _, err := srv.Pool.Exec(ctx, `
		UPDATE background_jobs SET status = 'canceled', updated_at = $2
		WHERE id = $1 AND status IN ('pending', 'paused')`, jobID, srv.Clock.Now()); err != nil {
		t.Fatal(err)
	}
}

// aiTaskIDString 把雪花 ID 转成字符串（保留给后续断言使用，避免未使用告警）。
func aiTaskIDString(id int64) string { return strconv.FormatInt(id, 10) }
