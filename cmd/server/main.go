// 时间线服务入口：只负责进程生命周期（时区/配置/日志/信号/优雅关闭）、
// HTTP 先启动 + DB 就绪后组合根切换 handler，以及 migrate 子命令。
// 业务装配全部在 internal/app（组合根）。
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	// 内嵌时区数据库：二进制在任意环境（含精简容器）都能解析 Asia/Shanghai。
	_ "time/tzdata"

	"github.com/great-magician-01/timeline/internal/app"
	"github.com/great-magician-01/timeline/internal/platform/config"
	"github.com/great-magician-01/timeline/internal/platform/db"
	"github.com/great-magician-01/timeline/internal/platform/id"
	"github.com/great-magician-01/timeline/internal/platform/logging"
	"github.com/great-magician-01/timeline/internal/platform/migrate"
	"github.com/great-magician-01/timeline/internal/platform/storage"
	"github.com/great-magician-01/timeline/internal/server"
)

const (
	// listenAddr 容器内固定监听端口（宿主机映射见 .env 的 APP_PORT）。
	listenAddr = ":5070"
)

// appVersion 由构建参数注入（Dockerfile: -ldflags "-X main.appVersion=..."）。
var appVersion = "dev"

func main() {
	os.Exit(run())
}

func run() int {
	// 全系统统一北京时间（docs/REQUIREMENTS.md 时间基准）。
	loc, tzErr := time.LoadLocation("Asia/Shanghai")
	if tzErr != nil {
		loc = time.FixedZone("CST", 8*60*60)
	}
	time.Local = loc

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "配置错误:", err)
		return 1
	}
	log, logCloser, err := logging.Init(cfg.LogLevel, cfg.LogFormat, cfg.LogFile, loc)
	if err != nil {
		fmt.Fprintln(os.Stderr, "日志初始化失败:", err)
		return 1
	}
	defer func() { _ = logCloser.Close() }()

	log.Info("时间线服务启动", "version", appVersion, "timezone", loc.String(),
		"log_file", cfg.LogFile, "listen", listenAddr)
	if tzErr != nil {
		log.Warn("系统缺少时区数据，已退回固定 +08:00 时区", "err", tzErr)
	}
	if !cfg.SecretKeyFromEnv {
		log.Warn("未配置 ENCRYPTION_KEY，接口预设的 API Key 使用由数据库连接信息派生的密钥加密；生产环境建议显式配置")
	}
	if err := os.MkdirAll(cfg.UploadDir, 0o755); err != nil {
		log.Warn("创建上传目录失败（发布附件前需修复）", "dir", cfg.UploadDir, "err", err)
	}

	ids, err := id.NewGenerator(cfg.SnowflakeWorkerID)
	if err != nil {
		log.Error("雪花 ID 生成器初始化失败", "err", err)
		return 1
	}
	store, err := storage.New(cfg.UploadDir)
	if err != nil {
		log.Error("附件存储初始化失败", "dir", cfg.UploadDir, "err", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if len(os.Args) > 1 {
		if err := runCommand(ctx, os.Args[1:], cfg, log); err != nil {
			fmt.Fprintln(os.Stderr, "命令执行失败:", err)
			return 1
		}
		return 0
	}

	// HTTP 先启动：bootstrap server 只含健康检查/静态托管；数据库与迁移就绪后，
	// 组合根在一个独立的新 server 上完整装配（路由注册不触碰正在服务的引擎），
	// 最后用 SwapHandler 原子切换到完整 handler。
	readiness := db.NewReadiness()
	bootstrapSrv := server.New(cfg, log, readiness)
	httpSrv := &http.Server{
		Addr:              listenAddr,
		Handler:           bootstrapSrv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	var application atomic.Pointer[app.App]

	// 后台：连接数据库 → 迁移 → 在独立 server 上装配 → 切换 handler → 标记就绪。
	go func() {
		pool, err := db.Wait(ctx, cfg.DSN(), log)
		if err != nil {
			return // ctx 取消
		}
		readiness.SetPool(pool)
		if cfg.RunMigrations {
			if err := migrate.Run(ctx, pool, log); err != nil {
				log.Error("数据库迁移失败，服务保持未就绪", "err", err)
				readiness.SetMigrationError(err)
				return
			}
		} else {
			log.Info("RUN_MIGRATIONS=false，跳过自动迁移")
		}

		// 独立 fullSrv：路由在监听之外构建，避免与在途请求并发修改 Gin 路由树。
		fullSrv := server.New(cfg, log, readiness)
		instance, err := app.New(cfg, log, readiness, ids, store, fullSrv.Engine())
		if err != nil {
			log.Error("组合根装配失败，服务保持未就绪", "err", err)
			readiness.SetMigrationError(err)
			return
		}
		application.Store(instance)
		bootstrapSrv.SwapHandler(fullSrv.Handler())
		// 完整 handler 已接管后再标记就绪：此前 /api 一律 503，就绪后未知路径才 404。
		readiness.MarkMigrationsDone()
		instance.Start(ctx)
		instance.Bootstrap(ctx)
		log.Info("数据库与迁移就绪，/readyz 可返回 200")
	}()

	go func() {
		log.Info("HTTP 服务开始监听", "addr", listenAddr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("HTTP 服务异常退出", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("收到退出信号，开始优雅关闭")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Warn("HTTP 关闭超时", "err", err)
	}
	if instance := application.Load(); instance != nil {
		instance.Close()
	}
	if pool := readiness.Pool(); pool != nil {
		pool.Close()
	}
	log.Info("已退出")
	return 0
}

// runCommand 提供部署文档约定的手动迁移命令。
func runCommand(ctx context.Context, args []string, cfg *config.Config, log *slog.Logger) error {
	if len(args) != 1 || args[0] != "migrate" {
		return fmt.Errorf("未知命令；可用命令: migrate")
	}
	pool, err := db.Wait(ctx, cfg.DSN(), log)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := migrate.Run(ctx, pool, log); err != nil {
		return err
	}
	log.Info("手动数据库迁移完成")
	return nil
}
