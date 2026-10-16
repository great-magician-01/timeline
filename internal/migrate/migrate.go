// Package migrate 是最小化数据库迁移器（骨架实现）：
//   - SQL 文件来自内嵌的 migrations 包，按文件名排序，只允许追加；
//   - 使用 PostgreSQL advisory lock 防止并发迁移（单实例部署下天然安全）；
//   - 已应用版本记录在 timeline.schema_migrations 表；
//   - 当前实现按 ";" 简单分句逐条执行：迁移文件里不要写函数体等含分号的复合语句，
//     后续如需复杂迁移再升级为完整解析器或更换成熟迁移工具。
package migrate

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/great-magician-01/timeline/migrations"
)

// advisoryLockKey 取 "timeline" 的 ASCII 字节，作为本应用的迁移锁标识。
const advisoryLockKey int64 = 0x74696D656C696E65

// Run 执行所有未应用的迁移；幂等，可重复调用。
func Run(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("获取数据库连接失败: %w", err)
	}
	defer conn.Release()

	// 引导一：确保两个 schema 存在。
	// 注意：只在缺失时执行 CREATE——PostgreSQL 对已存在的 schema 执行
	// CREATE SCHEMA IF NOT EXISTS 仍要求数据库级 CREATE 权限；而托管/预授权的
	// 环境通常由 DBA 预先建好 schema，应用账号只有 schema 内权限。
	for _, schema := range []string{"timeline", `"user"`} {
		if err := ensureSchema(ctx, conn, schema); err != nil {
			return err
		}
	}

	// 引导二：版本表本身不参与版本管理
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS timeline.schema_migrations (
		version    text PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("初始化迁移表失败（需要账号对 timeline schema 有 CREATE 权限）: %w", err)
	}

	// 并发保护：同一时刻只允许一个实例执行迁移
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, advisoryLockKey); err != nil {
		return fmt.Errorf("获取迁移锁失败: %w", err)
	}
	defer func() {
		if _, err := conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, advisoryLockKey); err != nil {
			log.Warn("释放迁移锁失败", "err", err)
		}
	}()

	applied := map[string]bool{}
	rows, err := conn.Query(ctx, `SELECT version FROM timeline.schema_migrations`)
	if err != nil {
		return fmt.Errorf("读取已应用迁移失败: %w", err)
	}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return err
		}
		applied[v] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	names, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)

	for _, name := range names {
		if applied[name] {
			continue
		}
		content, err := migrations.FS.ReadFile(name)
		if err != nil {
			return fmt.Errorf("读取迁移文件 %s 失败: %w", name, err)
		}

		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("开启迁移事务失败: %w", err)
		}
		for _, stmt := range splitStatements(string(content)) {
			if _, err := tx.Exec(ctx, stmt); err != nil {
				_ = tx.Rollback(ctx)
				return fmt.Errorf("迁移 %s 执行失败: %w\n语句: %s", name, err, stmt)
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO timeline.schema_migrations(version) VALUES ($1)`, name); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("记录迁移版本 %s 失败: %w", name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("提交迁移 %s 失败: %w", name, err)
		}
		log.Info("迁移已应用", "version", name)
	}
	return nil
}

// ensureSchema 确保 schema 存在：存在则跳过（不触发权限检查），缺失才创建。
// schema 名称来自代码常量，非用户输入，直接拼接是安全的。
func ensureSchema(ctx context.Context, conn *pgxpool.Conn, schema string) error {
	quoted := schema
	if quoted != `"user"` {
		quoted = `"` + quoted + `"`
	}
	name := strings.Trim(quoted, `"`)

	var exists bool
	if err := conn.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)`, name,
	).Scan(&exists); err != nil {
		return fmt.Errorf("检查 schema %s 是否存在失败: %w", name, err)
	}
	if exists {
		return nil
	}
	if _, err := conn.Exec(ctx, `CREATE SCHEMA `+quoted); err != nil {
		return fmt.Errorf("创建 schema %s 失败（需要数据库级 CREATE 权限，或由 DBA 预先创建并授权）: %w", name, err)
	}
	return nil
}

// splitStatements 按 ";" 分句并清理空句与纯注释句。
func splitStatements(sql string) []string {
	var stmts []string
	for part := range strings.SplitSeq(sql, ";") {
		if stripComments(part) == "" {
			continue
		}
		stmts = append(stmts, strings.TrimSpace(part))
	}
	return stmts
}

func stripComments(s string) string {
	var b strings.Builder
	for line := range strings.SplitSeq(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "--") {
			continue
		}
		b.WriteString(line)
	}
	return b.String()
}
