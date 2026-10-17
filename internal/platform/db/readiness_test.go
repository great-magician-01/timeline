package db

import (
	"errors"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestReadinessZeroValue 覆盖零值 Readiness 的初始状态。
func TestReadinessZeroValue(t *testing.T) {
	var r Readiness
	if r.Ready() {
		t.Fatal("零值 Readiness 不应就绪")
	}
	if r.Pool() != nil {
		t.Fatalf("零值 Readiness Pool() = %v, want nil", r.Pool())
	}
	if r.MigrationsDone() {
		t.Fatal("零值 Readiness 不应标记迁移完成")
	}
	if r.MigrationError() != nil {
		t.Fatalf("零值 Readiness MigrationError() = %v, want nil", r.MigrationError())
	}
}

// TestReadinessLifecycle 覆盖 连接池设置 → 迁移完成 → 迁移失败的完整状态流转。
func TestReadinessLifecycle(t *testing.T) {
	r := NewReadiness()

	// 仅设置连接池：数据库可用但迁移未完成，仍不就绪。
	pool := &pgxpool.Pool{}
	r.SetPool(pool)
	if r.Pool() != pool {
		t.Fatal("Pool 未返回设置的连接池")
	}
	if r.Ready() {
		t.Fatal("迁移未完成时不应就绪")
	}

	r.MarkMigrationsDone()
	if !r.MigrationsDone() {
		t.Fatal("MigrationsDone 应为 true")
	}
	if !r.Ready() {
		t.Fatal("连接池与迁移都就绪时应 Ready")
	}
	if r.MigrationError() != nil {
		t.Fatalf("迁移成功后不应有错误: %v", r.MigrationError())
	}

	// 迁移失败：记录原因并回到未就绪，但连接池保留供 /readyz 展示。
	migrateErr := errors.New("迁移 0001 执行失败")
	r.SetMigrationError(migrateErr)
	if r.Ready() {
		t.Fatal("迁移失败后不应就绪")
	}
	if r.MigrationsDone() {
		t.Fatal("迁移失败后 MigrationsDone 应为 false")
	}
	if !errors.Is(r.MigrationError(), migrateErr) {
		t.Fatalf("MigrationError = %v, want %v", r.MigrationError(), migrateErr)
	}
	if r.Pool() != pool {
		t.Fatal("迁移失败不应清空连接池")
	}

	// 修复后重新标记完成：清除错误并就绪。
	r.MarkMigrationsDone()
	if !r.Ready() {
		t.Fatal("重新标记迁移完成后应就绪")
	}
	if r.MigrationError() != nil {
		t.Fatalf("重新标记完成后应清除迁移错误: %v", r.MigrationError())
	}

	// SetMigrationError(nil) 只清错误，不改变已完成的迁移标记。
	r.SetMigrationError(nil)
	if !r.Ready() {
		t.Fatal("SetMigrationError(nil) 不应影响就绪状态")
	}
}

// TestReadinessPoolNil 覆盖数据库未连接的状态。
func TestReadinessPoolNil(t *testing.T) {
	r := NewReadiness()
	r.SetPool(nil)
	r.MarkMigrationsDone()
	if r.Ready() {
		t.Fatal("连接池为 nil 时即使迁移完成也不应就绪")
	}

	r.SetPool(&pgxpool.Pool{})
	if !r.Ready() {
		t.Fatal("连接池恢复后应就绪")
	}
}

// TestReadinessConcurrent 验证并发读写不竞态、不死锁（配合 -race 使用）。
func TestReadinessConcurrent(t *testing.T) {
	r := NewReadiness()
	pool := &pgxpool.Pool{}
	err := errors.New("迁移失败")

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				r.SetPool(pool)
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = r.Ready()
				_ = r.Pool()
				_ = r.MigrationsDone()
				_ = r.MigrationError()
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				r.SetMigrationError(err)
				r.MarkMigrationsDone()
			}
		}()
	}
	wg.Wait()

	if !r.Ready() {
		t.Fatal("末尾一次 MarkMigrationsDone 后应就绪")
	}
}
