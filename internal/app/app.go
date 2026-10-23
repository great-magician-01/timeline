// Package app 是组合根：唯一装配全部平台依赖、跨模块端口、模块 app/http 与作业处理器，
// 并在 server 的 Gin 引擎上注册 transport/router 的地方。
//
// 启动顺序：main 先起 bootstrap server（健康检查/静态托管）→ 后台等待数据库并迁移
// → New() 构造完整 App 并 SwapHandler → Start() 启动作业运行器 → Bootstrap() 输出初始化状态。
package app

import (
	"context"
	"log/slog"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/platform/clock"
	"github.com/great-magician-01/timeline/internal/platform/db"
	platformjobs "github.com/great-magician-01/timeline/internal/platform/jobs"
)

// IDGenerator 生成雪花 ID（*platform/id.Generator 满足）。
type IDGenerator interface {
	Next() (int64, error)
}

// BootstrapStatusFn 返回人类用户数与超级管理员数（auth app.BootstrapStatus 适配）。
type BootstrapStatusFn func(ctx context.Context) (humanUsers, superAdmins int64, err error)

// App 是装配完成的后端应用。
type App struct {
	engine    *gin.Engine
	runner    *platformjobs.Runner
	readiness *db.Readiness
	clk       clock.Clock
	log       *slog.Logger

	bootstrap BootstrapStatusFn
	onClose   func()
	closeOnce sync.Once

	started bool
}

// newApp 由 wire.go 调用；engine 已注册全部业务路由。
func newApp(engine *gin.Engine, runner *platformjobs.Runner, readiness *db.Readiness,
	clk clock.Clock, log *slog.Logger, bootstrap BootstrapStatusFn, onClose func()) *App {
	if log == nil {
		log = slog.Default()
	}
	if clk == nil {
		clk = clock.System()
	}
	return &App{
		engine:    engine,
		runner:    runner,
		readiness: readiness,
		clk:       clk,
		log:       log,
		bootstrap: bootstrap,
		onClose:   onClose,
	}
}

// Engine 返回已注册业务路由的 Gin 引擎。
func (a *App) Engine() *gin.Engine { return a.engine }

// Handler 返回完整应用 handler（main 用它替换 bootstrap handler）。
func (a *App) Handler() http.Handler { return a.engine }

// Runner 返回后台作业运行器（组合根注册作业后由 Start 启动）。
func (a *App) Runner() *platformjobs.Runner { return a.runner }

// Readiness 返回运行时就绪状态。
func (a *App) Readiness() *db.Readiness { return a.readiness }

// Start 启动作业运行器与周期任务；重复调用安全。
//
// 同时监听进程退出信号（ctx 取消）：先断开全部 SSE 长连接再让 main 调用
// http.Server.Shutdown，避免优雅关闭被长连接拖满超时；关闭回调幂等，Close()
// 的后续调用安全。
func (a *App) Start(ctx context.Context) {
	if a.started {
		return
	}
	a.started = true
	if a.runner != nil {
		a.runner.Start(ctx)
	}
	if a.onClose != nil {
		go func() {
			<-ctx.Done()
			a.runOnClose()
		}()
	}
}

// Bootstrap 输出初始化状态日志：空库提示首注册成为超管、无超管时告警。
func (a *App) Bootstrap(ctx context.Context) {
	if a.bootstrap == nil {
		return
	}
	humanUsers, superAdmins, err := a.bootstrap(ctx)
	if err != nil {
		a.log.Warn("读取初始化状态失败", "err", err)
		return
	}
	switch {
	case humanUsers == 0:
		a.log.Warn("数据库尚无人类用户：首个注册用户将自动成为唯一超级管理员，完成初始化前请勿对外开放")
	case superAdmins == 0:
		a.log.Warn("当前没有超级管理员：管理员授权/撤销接口不可用，需按文档另行制定迁移方案")
	default:
		a.log.Info("初始化状态正常", "human_users", humanUsers, "super_admins", superAdmins)
	}
}

// Close 释放组合根持有的资源；外部传入的连接池由调用方关闭。
// 与 Start 布防的退出信号监听共用同一个 Once，幂等。
func (a *App) Close() {
	a.runOnClose()
}

// runOnClose 幂等执行关闭回调；onClose 在构造后只读，无并发写。
func (a *App) runOnClose() {
	if a.onClose == nil {
		return
	}
	a.closeOnce.Do(a.onClose)
}
