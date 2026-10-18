package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"

	adminapp "github.com/great-magician-01/timeline/internal/modules/admin/app"
	adminhttp "github.com/great-magician-01/timeline/internal/modules/admin/http"
	adminrepo "github.com/great-magician-01/timeline/internal/modules/admin/repo"
	agentapp "github.com/great-magician-01/timeline/internal/modules/agent/app"
	agenthttp "github.com/great-magician-01/timeline/internal/modules/agent/http"
	agentrepo "github.com/great-magician-01/timeline/internal/modules/agent/repo"
	aiapp "github.com/great-magician-01/timeline/internal/modules/ai/app"
	aihttp "github.com/great-magician-01/timeline/internal/modules/ai/http"
	authapp "github.com/great-magician-01/timeline/internal/modules/auth/app"
	authhttp "github.com/great-magician-01/timeline/internal/modules/auth/http"
	authrepo "github.com/great-magician-01/timeline/internal/modules/auth/repo"
	contentapp "github.com/great-magician-01/timeline/internal/modules/content/app"
	contenthttp "github.com/great-magician-01/timeline/internal/modules/content/http"
	mediaapp "github.com/great-magician-01/timeline/internal/modules/media/app"
	mediahttp "github.com/great-magician-01/timeline/internal/modules/media/http"
	noticeapp "github.com/great-magician-01/timeline/internal/modules/notice/app"
	noticehttp "github.com/great-magician-01/timeline/internal/modules/notice/http"
	noticerepo "github.com/great-magician-01/timeline/internal/modules/notice/repo"
	socialapp "github.com/great-magician-01/timeline/internal/modules/social/app"
	socialhttp "github.com/great-magician-01/timeline/internal/modules/social/http"
	socialrepo "github.com/great-magician-01/timeline/internal/modules/social/repo"
	platformaudit "github.com/great-magician-01/timeline/internal/platform/audit"
	"github.com/great-magician-01/timeline/internal/platform/clock"
	"github.com/great-magician-01/timeline/internal/platform/config"
	"github.com/great-magician-01/timeline/internal/platform/db"
	platformjobs "github.com/great-magician-01/timeline/internal/platform/jobs"
	platformlogging "github.com/great-magician-01/timeline/internal/platform/logging"
	platformsettings "github.com/great-magician-01/timeline/internal/platform/settings"
	"github.com/great-magician-01/timeline/internal/platform/storage"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/transport/router"
)

// 日志模块标签：业务模块名由组合根维护，platform/logging 只负责渲染与补齐列宽。
const (
	logModuleApp     = "app"
	logModuleJobs    = "jobs"
	logModuleAuth    = "auth"
	logModuleContent = "content"
	logModuleSocial  = "social"
	logModuleNotice  = "notice"
	logModuleMedia   = "media"
	logModuleAI      = "ai"
	logModuleAgent   = "agent"
	logModuleAdmin   = "admin"
)

// 延迟绑定包装：content/social/media/notice/agent 之间存在构造环，
// 组合根先构造占位包装（空实现），全部构造完成后回填真实生产者实例。
// 运行期第一次调用发生在 HTTP 请求/作业执行阶段，此时回填一定已完成。
type lazyRelation struct{ contracts.RelationReader }
type lazySocial struct{ contracts.SocialService }
type lazyMedia struct{ contracts.MediaService }
type lazyNotice struct{ contracts.NoticeService }
type lazyAgent struct{ contracts.AgentService }

// globalAIAdapter 把 agent app 的全局开关能力适配为 admin 的 GlobalAIHalter。
type globalAIAdapter struct{ agent *agentapp.Service }

func (a globalAIAdapter) HaltGlobalAI(ctx context.Context, actorID int64, at time.Time) error {
	return a.agent.SetGlobal(ctx, actorID, false)
}

func (a globalAIAdapter) EnableGlobalAI(ctx context.Context, actorID int64, at time.Time) error {
	return a.agent.SetGlobal(ctx, actorID, true)
}

// New 构造完整应用：平台依赖 → repo → 模块 app → 模块 http → 路由注册 → 作业注册。
// engine 由 internal/server 提供（已装全局中间件/健康检查/前端托管）。
func New(cfg *config.Config, log *slog.Logger, readiness *db.Readiness, ids IDGenerator,
	store *storage.Store, engine *gin.Engine) (*App, error) {
	if log == nil {
		log = slog.Default()
	}
	// 模块标签：text 格式渲染为消息前的 [module] 列，json 格式保留 module 字段。
	moduleLog := func(module string) *slog.Logger {
		return platformlogging.WithModule(log, module)
	}
	pool := readiness.Pool()
	clk := clock.System()
	tx := db.NewTxManager(pool)

	settingsStore := platformsettings.NewStore(pool)
	settingsRead := settingsAdapter{store: settingsStore}
	settingsService := settingsServiceAdapter{settingsAdapter: settingsRead}

	auditWriter := platformaudit.NewWriter(pool, ids)
	auditAdapter := auditAdapter{writer: auditWriter}

	jobRunner := platformjobs.NewRunner(readiness, ids,
		moduleLog(logModuleJobs), clk)
	jobsAdapter := jobsAdapter{pool: pool, runner: jobRunner}

	// admin 只读 read-model：同时服务 admin 审核/概览与 auth 个人主页统计。
	adminReadModel := adminrepo.NewReadModel(pool)

	relations := &lazyRelation{}
	socialLazy := &lazySocial{}
	mediaLazy := &lazyMedia{}
	noticeLazy := &lazyNotice{}
	agentLazy := &lazyAgent{}
	tasksLazy := &lazyTaskReader{}

	// ── auth ──
	authApp := authapp.New(authapp.Deps{
		Users:        authrepo.NewUserRepo(pool),
		Sessions:     authrepo.NewSessionRepo(pool),
		Locks:        authrepo.NewLockRepo(pool),
		Tx:           tx,
		IDs:          ids,
		Clock:        clk,
		Logger:       moduleLog(logModuleAuth),
		Settings:     settingsRead,
		Audit:        auditAdapter,
		Relations:    relations,
		Stats:        profileStatsAdapter{read: adminReadModel},
		CookieSecure: cfg.CookieSecure,
	})

	// ── content（依赖 social/notice/media 的延迟端口） ──
	contentApp := contentapp.New(contentapp.Deps{
		Pools:    readiness,
		Tx:       tx,
		Clock:    clk,
		IDs:      ids,
		Log:      moduleLog(logModuleContent),
		Users:    authApp,
		Media:    mediaLazy,
		Social:   socialLazy,
		Notice:   noticeLazy,
		Jobs:     jobsAdapter,
		Audit:    auditAdapter,
		Settings: settingsRead,
	})

	// ── social ──
	socialApp := socialapp.New(socialapp.Deps{
		Clock:    clk,
		IDs:      ids,
		Tx:       tx,
		Users:    authApp,
		Posts:    contentApp,
		Notices:  noticeLazy,
		Audit:    auditWriter,
		Follows:  socialrepo.NewFollowRepo(pool),
		Requests: socialrepo.NewRequestRepo(pool),
		Likes:    socialrepo.NewLikeRepo(pool),
		Comments: socialrepo.NewCommentRepo(pool),
		Reposts:  socialrepo.NewRepostRepo(pool),
	})

	// ── notice ──
	noticeApp := noticeapp.New(noticeapp.Deps{
		Clock:     clk,
		IDs:       ids,
		Users:     authApp,
		Posts:     contentApp,
		Relations: socialApp,
		Comments:  socialApp,
		Notices:   noticerepo.NewNotificationRepo(pool),
		Mentions:  noticerepo.NewMentionRepo(pool),
	})

	// ── media ──
	mediaApp := mediaapp.New(mediaapp.Deps{
		Pools:    readiness,
		Tx:       tx,
		Clock:    clk,
		IDs:      ids,
		Log:      moduleLog(logModuleMedia),
		Storage:  store,
		Jobs:     jobsAdapter,
		Audit:    auditAdapter,
		Settings: settingsRead,
		Auth:     authApp,
		Content:  contentApp,
	})

	// ── ai ──
	aiApp := aiapp.New(aiapp.Deps{
		Pool:      readiness,
		Tx:        tx,
		IDs:       ids,
		Clock:     clk,
		Log:       moduleLog(logModuleAI),
		SecretKey: cfg.SecretKey,
		Users:     authApp,
		Auth:      authApp,
		Tasks:     tasksLazy,
		Agent:     agentLazy,
		Jobs:      jobsAdapter,
		Audit:     auditAdapter,
		Settings:  settingsRead,
	})

	// ── agent ──
	agentRepo := agentrepo.New(pool)
	agentApp := agentapp.New(agentapp.Deps{
		Clock:          clk,
		IDs:            ids,
		Tx:             tx,
		Log:            moduleLog(logModuleAgent),
		Tasks:          agentRepo,
		CallLogs:       agentRepo,
		Counters:       agentRepo,
		Content:        contentApp,
		Social:         socialApp,
		AI:             aiApp,
		Notices:        noticeApp,
		Users:          authApp,
		Jobs:           jobsAdapter,
		Audit:          auditAdapter,
		Settings:       settingsRead,
		SettingsWriter: settingsRead,
	})

	// ── admin ──
	adminApp := adminapp.New(adminapp.Deps{
		Tx:       tx,
		Clock:    clk,
		Log:      moduleLog(logModuleAdmin),
		Read:     adminReadModel,
		Audit:    auditAdapter,
		Auth:     authApp,
		Content:  contentApp,
		Social:   socialApp,
		Media:    mediaApp,
		Jobs:     jobsAdapter,
		Settings: settingsService,
		Cipher:   aiApp,
		Agent:    agentApp,
		GlobalAI: globalAIAdapter{agent: agentApp},
	})

	// ── 回填循环依赖 ──
	relations.RelationReader = socialApp
	socialLazy.SocialService = socialApp
	mediaLazy.MediaService = mediaApp
	noticeLazy.NoticeService = noticeApp
	agentLazy.AgentService = agentApp
	tasksLazy.TaskReader = aiTaskReaderAdapter{agent: agentApp}

	// ── HTTP handlers + 路由 ──
	handlers := router.Deps{
		Auth:    authhttp.New(authApp),
		Content: contenthttp.New(contentApp),
		Social:  socialhttp.New(socialApp, moduleLog(logModuleSocial)),
		Notice:  noticehttp.New(noticeApp, moduleLog(logModuleNotice)),
		Media:   mediahttp.New(mediaApp),
		AI:      aihttp.New(aiApp),
		Agent:   agenthttp.New(agentApp, moduleLog(logModuleAgent)),
		Admin:   adminhttp.New(adminApp),
	}
	router.Register(engine, handlers, readiness)

	// ── 作业与周期任务 ──
	registerJobs(jobRunner, contentApp, mediaApp, agentApp)

	return newApp(engine, jobRunner, readiness, clk, moduleLog(logModuleApp),
		func(ctx context.Context) (int64, int64, error) {
			status, err := authApp.BootstrapStatus(ctx)
			if err != nil {
				return 0, 0, err
			}
			return status.HumanUsers, status.SuperAdmins, nil
		}, nil), nil
}
