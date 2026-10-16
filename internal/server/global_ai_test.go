package server

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"
)

// TestGlobalAISwitchIntegration 覆盖全局 AI 开关的 HTTP 路径：
// 开启后读取为真；紧急停止会取消待执行的 AI 任务、暂停 AI 后台作业并写入审计。
// 全局开关的开启/停止是两个独立实现（agent 与 admin），这里都走真实路由验证。
func TestGlobalAISwitchIntegration(t *testing.T) {
	if os.Getenv("TIMELINE_INTEGRATION") != "1" {
		t.Skip("设置 TIMELINE_INTEGRATION=1 后运行测试 schema 集成测试")
	}
	pool, _, base := setupIntegrationServer(t)
	ctx := context.Background()

	admin := newIntegrationClient(t, base)
	loginIntegrationUser(t, admin, "admin_integration", "admin-password-123")

	// 开启
	status, _, payload, raw := admin.call(http.MethodPost, "/api/v1/admin/ai/global-start", true, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if enabled, _ := payload["ai_global_enabled"].(bool); !enabled {
		t.Fatalf("global-start 后应为开启: %v", payload)
	}
	status, _, settings, raw := admin.call(http.MethodGet, "/api/v1/admin/settings", false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if enabled, _ := settings["ai_global_enabled"].(bool); !enabled {
		t.Fatalf("系统设置应读到全局开关为开启: %v", settings)
	}

	// 准备一个待执行任务与一个待执行作业，验证紧急停止的连带效果。
	// 主键用纳秒时间戳派生，保证重复运行时不会与上一轮的数据冲突（测试 schema 不做物理删除）。
	suffix := integrationSuffix()
	idBase := time.Now().UnixNano()
	aiID := idBase
	taskID := idBase + 1
	jobID := idBase + 2
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, username, nickname, role, is_ai, status, token_version)
		VALUES ($1, $2, $3, 'user', TRUE, 'active', 1)`,
		aiID, "gai_ai_"+suffix, "全局开关 AI"); err != nil {
		t.Fatalf("准备 AI 账号失败: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO ai_tasks (id, ai_id, trigger_type, status, idempotency_key)
		VALUES ($1, $2, 'schedule', 'pending', $3)`,
		taskID, aiID, "gai-task-"+suffix); err != nil {
		t.Fatalf("准备待执行任务失败: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO background_jobs (id, job_type, ref_type, ref_id, status, idempotency_key)
		VALUES ($1, 'ai_schedule', 'ai', $2, 'pending', $3)`,
		jobID, aiID, "gai-job-"+suffix); err != nil {
		t.Fatalf("准备待执行作业失败: %v", err)
	}

	// 紧急停止
	status, _, payload, raw = admin.call(http.MethodPost, "/api/v1/admin/ai/global-stop", true, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if enabled, _ := payload["ai_global_enabled"].(bool); enabled {
		t.Fatalf("global-stop 后应为关闭: %v", payload)
	}

	var taskStatus, jobStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM ai_tasks WHERE id = $1`, taskID).Scan(&taskStatus); err != nil {
		t.Fatal(err)
	}
	if taskStatus != "canceled" {
		t.Fatalf("紧急停止应取消待执行任务，实际 %s", taskStatus)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM background_jobs WHERE id = $1`, jobID).Scan(&jobStatus); err != nil {
		t.Fatal(err)
	}
	if jobStatus != "paused" {
		t.Fatalf("紧急停止应暂停 AI 作业，实际 %s", jobStatus)
	}

	// 审计：全局停止必须留痕
	var auditCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM admin_audit_logs
		WHERE action_type = 'ai_global_stop' AND detail->>'ai_global_enabled' = 'false'`,
	).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount == 0 {
		t.Fatal("全局停止未写入审计记录")
	}

	// 关闭状态下的开关读取（由 admin 包实现）应一致
	status, _, settings, raw = admin.call(http.MethodGet, "/api/v1/admin/settings", false, nil)
	expectIntegrationStatus(t, status, http.StatusOK, raw)
	if enabled, _ := settings["ai_global_enabled"].(bool); enabled {
		t.Fatalf("系统设置应读到全局开关为关闭: %v", settings)
	}

	// 收尾：把测试遗留的作业置为取消，避免影响后续运行（逻辑状态，不物理删除）。
	if _, err := pool.Exec(ctx, `
		UPDATE background_jobs SET status = 'canceled', updated_at = now()
		WHERE id = $1 AND status IN ('pending', 'paused')`, jobID); err != nil {
		t.Fatal(err)
	}
}
