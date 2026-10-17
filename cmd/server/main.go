// 时间线服务入口。
// 启动顺序（docs/DEPLOYMENT.md §6）：加载配置 → 初始化日志 → 启动 HTTP（含健康检查）
// → 后台连接外部数据库（退避重试）→ 执行迁移 → 标记就绪。
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	// 内嵌时区数据库：二进制在任意环境（含精简容器）都能解析 Asia/Shanghai
	_ "time/tzdata"

	"golang.org/x/term"

	"github.com/great-magician-01/timeline/internal/auth"
	"github.com/great-magician-01/timeline/internal/config"
	"github.com/great-magician-01/timeline/internal/db"
	"github.com/great-magician-01/timeline/internal/id"
	"github.com/great-magician-01/timeline/internal/jobs"
	"github.com/great-magician-01/timeline/internal/logging"
	"github.com/great-magician-01/timeline/internal/migrate"
	"github.com/great-magician-01/timeline/internal/server"
	"github.com/great-magician-01/timeline/internal/storage"
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
	// 全系统统一北京时间（docs/REQUIREMENTS.md 时间基准）；
	// tzdata 已内嵌，理论上不会失败；兜底使用固定 +08:00（中国无夏令时，语义等价）。
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

	log.Info("时间线服务启动",
		"version", appVersion, "timezone", loc.String(),
		"log_file", cfg.LogFile, "listen", listenAddr)
	if tzErr != nil {
		log.Warn("系统缺少时区数据，已退回固定 +08:00 时区", "err", tzErr)
	}

	if err := os.MkdirAll(cfg.UploadDir, 0o755); err != nil {
		log.Warn("创建上传目录失败（发布附件前需修复）", "dir", cfg.UploadDir, "err", err)
	}

	gen, err := id.NewGenerator(cfg.SnowflakeWorkerID)
	if err != nil {
		log.Error("雪花 ID 生成器初始化失败", "err", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if len(os.Args) > 1 {
		if err := runCommand(ctx, os.Args[1:], cfg, log, gen); err != nil {
			fmt.Fprintln(os.Stderr, "命令执行失败:", err)
			return 1
		}
		return 0
	}

	store, err := storage.New(cfg.UploadDir)
	if err != nil {
		log.Error("附件存储初始化失败", "dir", cfg.UploadDir, "err", err)
		return 1
	}
	if !cfg.SecretKeyFromEnv {
		log.Warn("未配置 ENCRYPTION_KEY，接口预设的 API Key 使用由数据库连接信息派生的密钥加密；生产环境建议显式配置")
	}

	// 后台作业运行器：作业持久化在数据库，重启后恢复；处理器在 server.New 中注册。
	state := server.NewState()
	runner := jobs.NewRunner(state, gen, log)

	httpSrv := &http.Server{
		Addr:              listenAddr,
		Handler:           server.New(cfg, log, state, gen, runner, store).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	// 后台：连接数据库 → 迁移 → 标记就绪；连接失败按退避重试（ENV-07）
	migrationsDone := make(chan struct{})
	go func() {
		defer close(migrationsDone)
		pool, err := db.Wait(ctx, cfg.DSN(), log)
		if err != nil {
			return // ctx 取消
		}
		state.SetPool(pool)

		if cfg.RunMigrations {
			if err := migrate.Run(ctx, pool, log); err != nil {
				log.Error("数据库迁移失败，服务保持未就绪", "err", err)
				return
			}
		} else {
			log.Info("RUN_MIGRATIONS=false，跳过自动迁移")
		}
		state.MarkMigrationsDone()
		log.Info("数据库与迁移就绪，/readyz 可返回 200")
	}()

	// 后台作业与周期任务：连接池未就绪时空转，不阻塞进程。
	runner.Start(ctx)

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
	if pool := state.Pool(); pool != nil {
		pool.Close()
	}
	log.Info("已退出")
	return 0
}

// runCommand 提供部署文档约定的手动迁移和首个管理员初始化命令。
func runCommand(
	ctx context.Context,
	args []string,
	cfg *config.Config,
	log *slog.Logger,
	ids auth.IDGenerator,
) error {
	if len(args) != 1 || (args[0] != "migrate" && args[0] != "init-admin") {
		return fmt.Errorf("未知命令；可用命令: migrate、init-admin")
	}
	pool, err := db.Wait(ctx, cfg.DSN(), log)
	if err != nil {
		return err
	}
	defer pool.Close()

	// 两个命令都显式保证表结构就绪，不受 RUN_MIGRATIONS=false 的服务配置影响。
	if err := migrate.Run(ctx, pool, log); err != nil {
		return err
	}
	if args[0] == "migrate" {
		log.Info("手动数据库迁移完成")
		return nil
	}

	in, err := promptInitialAdmin()
	if err != nil {
		return err
	}
	u, err := auth.CreateInitialAdmin(ctx, pool, ids, in)
	if err != nil {
		return err
	}
	fmt.Printf("首个管理员创建成功，用户名: %s\n", u.Username)
	return nil
}

func promptInitialAdmin() (auth.RegisterInput, error) {
	reader := bufio.NewReader(os.Stdin)
	var terminal *term.Terminal
	if term.IsTerminal(int(os.Stdin.Fd())) {
		// 同一个 Terminal 处理全部交互输入，避免 bufio 预读把密码留在另一个缓冲区。
		terminal = term.NewTerminal(struct {
			io.Reader
			io.Writer
		}{Reader: os.Stdin, Writer: os.Stdout}, "")
	}
	readLine := func(label string) (string, error) {
		if terminal != nil {
			terminal.SetPrompt(label)
			line, err := terminal.ReadLine()
			if err != nil {
				return "", fmt.Errorf("读取输入失败: %w", err)
			}
			return strings.TrimSpace(line), nil
		}
		fmt.Print(label)
		line, err := reader.ReadString('\n')
		if err != nil && len(line) == 0 {
			return "", fmt.Errorf("读取输入失败: %w", err)
		}
		return strings.TrimSpace(line), nil
	}
	readPassword := func(label string) (string, error) {
		if terminal != nil {
			password, err := terminal.ReadPassword(label)
			if err != nil {
				return "", fmt.Errorf("读取密码失败: %w", err)
			}
			return password, nil
		}
		// 非交互管道用于自动化验收；输入本身不会回显到终端。
		fmt.Print(label)
		line, err := reader.ReadString('\n')
		if err != nil && len(line) == 0 {
			return "", fmt.Errorf("读取密码失败: %w", err)
		}
		return strings.TrimRight(line, "\r\n"), nil
	}

	username, err := readLine("用户名（3-32 位，字母开头，仅字母/数字/下划线）: ")
	if err != nil {
		return auth.RegisterInput{}, err
	}
	email, err := readLine("邮箱: ")
	if err != nil {
		return auth.RegisterInput{}, err
	}
	password, err := readPassword("密码（8-64 个字符）: ")
	if err != nil {
		return auth.RegisterInput{}, err
	}
	confirmation, err := readPassword("再次输入密码: ")
	if err != nil {
		return auth.RegisterInput{}, err
	}
	if password != confirmation {
		return auth.RegisterInput{}, fmt.Errorf("两次输入的密码不一致")
	}
	return auth.RegisterInput{Username: username, Email: email, Password: password}, nil
}
