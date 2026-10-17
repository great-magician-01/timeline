// Package app 是 admin 编排模块的用例层：负责事务边界、时钟取时、跨模块端口调用
// 与审计写入，不包含 SQL 与 Gin。
//
// admin 是编排型上下文：没有自有实体与 domain 层；用户处置经 contracts.AuthService，
// 内容审核删除经 contracts.ContentService/SocialService/MediaService，后台任务经
// contracts.JobsQueue，系统设置经 contracts.SettingsService，审计写入经
// contracts.AuditWriter。跨表只读查询集中在 repo/read_model.go（显式只读例外）。
package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/admin/repo"
	"github.com/great-magician-01/timeline/internal/platform/clock"
	"github.com/great-magician-01/timeline/internal/platform/db"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
	"github.com/great-magician-01/timeline/internal/shared/model"
)

// 只读 read-model 的行结构与过滤类型由 repo 定义；app 以别名发布给 http，
// 避免 http 依赖 repo（分层规则：http 只 import 本模块 app）。
type (
	// UsersQuery 是用户列表过滤条件。
	UsersQuery = repo.UserFilter
	// PostsQuery 是帖子审核列表过滤条件。
	PostsQuery = repo.PostFilter
	// CommentsQuery 是评论审核列表过滤条件。
	CommentsQuery = repo.CommentFilter
	// AttachmentsQuery 是附件审核列表过滤条件。
	AttachmentsQuery = repo.AttachmentFilter
	// TasksQuery 是后台作业列表过滤条件。
	TasksQuery = repo.TaskFilter
	// AuditQuery 是审计日志列表过滤条件。
	AuditQuery = repo.AuditFilter

	// UserStats 是用户详情页统计。
	UserStats = repo.UserStats
	// PostItem 是帖子审核列表/详情行。
	PostItem = repo.PostRow
	// VersionItem 是帖子历史版本行。
	VersionItem = repo.VersionRow
	// AttachmentItem 是附件审核行。
	AttachmentItem = repo.AttachmentRow
	// CommentItem 是评论审核行。
	CommentItem = repo.CommentRow
	// TaskItem 是后台作业行。
	TaskItem = repo.TaskRow
	// AuditItem 是审计日志行。
	AuditItem = repo.AuditRow
	// JobSnapshot 是作业端口返回的快照。
	JobSnapshot = contracts.JobSnapshot
)

// Page 是一页只读结果：Items 已裁剪到 limit，HasMore 表示是否还有下一页。
type Page[T any] struct {
	Items   []T
	LastID  int64
	HasMore bool
}

// Overview 是管理概览：计数来自 read-model，全局 AI 开关来自设置端口。
type Overview struct {
	Counts          repo.OverviewCounts
	AIGlobalEnabled bool
}

// UserDetail 是用户详情：账号行 + 活跃会话数 + 内容关系统计。
type UserDetail struct {
	User           model.User
	ActiveSessions int64
	Stats          UserStats
}

// PostDetail 是帖子审核详情：正文、历史版本、附件与评论上下文。
type PostDetail struct {
	Post         PostItem
	ContentHTML  string
	Versions     []VersionItem
	Attachments  []AttachmentItem
	Comments     []CommentItem
	CommentCount int64
}

// SettingsPatch 是系统设置的部分更新请求；nil 字段表示不修改。
type SettingsPatch struct {
	RegistrationEnabled *bool
	AIGlobalEnabled     *bool

	MaxUploadBytes               *int64
	MaxRequestBytes              *int64
	StorageQuotaBytes            *int64
	PostRateLimit                *int
	PostRateWindowSeconds        *int
	UploadRateLimit              *int
	UploadRateWindowSeconds      *int
	TempAttachmentRetentionHours *int
	CallLogMaxBytes              *int
	CallLogRetentionDays         *int
}

// Empty 判断请求是否没有携带任何设置项。
func (p SettingsPatch) Empty() bool {
	return p.RegistrationEnabled == nil && p.AIGlobalEnabled == nil &&
		p.MaxUploadBytes == nil && p.MaxRequestBytes == nil && p.StorageQuotaBytes == nil &&
		p.PostRateLimit == nil && p.PostRateWindowSeconds == nil &&
		p.UploadRateLimit == nil && p.UploadRateWindowSeconds == nil &&
		p.TempAttachmentRetentionHours == nil && p.CallLogMaxBytes == nil &&
		p.CallLogRetentionDays == nil
}

// SettingsSnapshot 是系统设置读取结果（与 contracts.SystemSettings 对齐）。
type SettingsSnapshot = contracts.SystemSettings

// WebSearchPatch 是全局网页搜索配置的部分更新请求；api_key 只写，clear_api_key 逻辑清除。
type WebSearchPatch struct {
	BaseURL     *string
	APIKey      *string
	ClearAPIKey *bool
}

// Empty 判断请求是否没有携带任何搜索设置项。
func (p WebSearchPatch) Empty() bool {
	return p.BaseURL == nil && p.APIKey == nil && p.ClearAPIKey == nil
}

// WebSearchSnapshot 是全局网页搜索配置的读取结果；永不包含明文或密文。
type WebSearchSnapshot struct {
	BaseURL      string
	Configured   bool
	Usable       bool
	APIKeyMasked string
}

// GlobalAIHalter 是全局停止 AI 时的跨模块编排端口（契约缺口，可选注入）：
// 当前 contracts 没有「全局取消待执行 AI 任务 + 暂停 AI 作业」的能力，
// 组合根可用 modules/agent 的用例实现该端口；未注入时仅写开关与审计。
type GlobalAIHalter interface {
	HaltGlobalAI(ctx context.Context, actorID int64, at time.Time) error
}

// Deps 是 admin app 的构造依赖；必填端口由组合根注入并负责非 nil。
type Deps struct {
	Tx    *db.TxManager
	Clock clock.Clock
	Log   *slog.Logger
	Read  *repo.ReadModel

	Audit    contracts.AuditWriter
	Auth     contracts.AuthService
	Content  contracts.ContentService
	Social   contracts.SocialService
	Media    contracts.MediaService
	Jobs     contracts.JobsQueue
	Settings contracts.SettingsService

	// 可选依赖：网页搜索密钥加密/可用性校验与停用 AI 账号时的任务取消编排。
	Cipher   contracts.SecretCipher
	Agent    contracts.AgentService
	GlobalAI GlobalAIHalter
}

// Service 是 admin 用例入口。
type Service struct {
	tx       *db.TxManager
	clock    clock.Clock
	log      *slog.Logger
	read     *repo.ReadModel
	audit    contracts.AuditWriter
	auth     contracts.AuthService
	content  contracts.ContentService
	social   contracts.SocialService
	media    contracts.MediaService
	jobs     contracts.JobsQueue
	settings contracts.SettingsService

	cipher   contracts.SecretCipher
	agent    contracts.AgentService
	globalAI GlobalAIHalter
}

// New 构造 admin 用例服务；Log 为 nil 时回退 slog.Default。
func New(deps Deps) *Service {
	log := deps.Log
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		tx:       deps.Tx,
		clock:    deps.Clock,
		log:      log,
		read:     deps.Read,
		audit:    deps.Audit,
		auth:     deps.Auth,
		content:  deps.Content,
		social:   deps.Social,
		media:    deps.Media,
		jobs:     deps.Jobs,
		settings: deps.Settings,
		cipher:   deps.Cipher,
		agent:    deps.Agent,
		globalAI: deps.GlobalAI,
	}
}

// now 返回本次用例的唯一应用时间。
func (s *Service) now() time.Time { return s.clock.Now() }

// Logger 返回服务持有的日志器，供 http 层记录未映射为领域错误的内部错误。
func (s *Service) Logger() *slog.Logger { return s.log }
