package contracts

import (
	"context"
	"time"

	"github.com/great-magician-01/timeline/internal/shared/model"
)

// PostCounter* 是帖子聚合计数器（posts 表由 content 模块独占写）。
// 消费者：social（评论/点赞/转发计数）、content 自身。
const (
	PostCounterLike    = "like"
	PostCounterComment = "comment"
	PostCounterRepost  = "repost"
	// PostCounterPost 是“帖子维度计数”的别名，供社交模块以 target_type 语义传参。
	PostCounterPost = "post"
)

// ActorPostInput 描述一次以指定作者身份创建内容（AI 生成、管理员代发）。
// 消费者：agent（AIGenerate / PublishDirect / 定时原创动作）。
//
// 字段与旧的 internal/content.ActorPostInput 对齐；调用方必须先完成鉴权。
type ActorPostInput struct {
	AuthorID      int64
	ContentFormat string
	ContentSource string
	Visibility    string
	AttachmentIDs []int64
	QuotedPostID  *int64
	// AuthorIsAI 是作者账号的 is_ai 快照，写入 posts.author_is_ai 供时间线按作者类型过滤。
	// 由调用方按已知的 authorID 账号属性显式传入（账号 is_ai 创建后不可变）。
	AuthorIsAI bool
	// AsDraft 为 true 时创建草稿（AI 草稿仅管理员可见）；false 时直接发布。
	AsDraft bool
	// ScheduledAt 非空时创建定时帖（优先于 AsDraft），由调用方按 clock 校验后传入。
	ScheduledAt *time.Time
	// At 是本次写入的应用时钟时间（同一事务内复用）。
	At time.Time
}

// PublishPostForActorInput 立即发布指定草稿（AI 草稿发布、管理员代发）。
// 消费者：agent（PublishAIDraft）。
type PublishPostForActorInput struct {
	ActorID int64
	PostID  int64
	At      time.Time
}

// UpdateDraftForActorInput 以指定作者身份覆盖草稿工作区内容。
// 消费者：agent（UpdateAIDraft，管理员编辑 AI 草稿）。
type UpdateDraftForActorInput struct {
	PostID        int64
	ContentFormat string
	ContentSource string
	At            time.Time
}

// DeletePostInput 管理员逻辑删除任意帖子（审计由 content 在同一事务内写入）。
// 消费者：admin（内容审核删除）。
type DeletePostInput struct {
	AdminID int64
	PostID  int64
	At      time.Time
}

// PostCounterInput 原子增减帖子计数器并返回最新值。
// 消费者：social（评论 +1/-1、点赞 ±1、转发 ±1）。
type PostCounterInput struct {
	PostID  int64
	Counter string // PostCounterLike / PostCounterComment / PostCounterRepost
	Delta   int
	At      time.Time
}

// CandidateQuery 是 AI 主动互动的公开候选内容过滤条件。
// 消费者：agent（loadCandidates）。
//
// ScopeType 取 "none"（不产生候选）、"followed"（AI 关注的人）、"tags"、"authors"；
// ScopeTags / ScopeUsernames 与旧 ai_behavior_settings.candidate_scope_params 对齐。
type CandidateQuery struct {
	ExcludeAuthorID int64
	AllowAIToAI     bool
	ScopeType       string
	ScopeTags       []string
	ScopeUsernames  []string
	Limit           int
}

// PostCandidate 是候选公开内容的渲染投影（帖子行 + 作者展示信息 + 摘要/标签）。
// 消费者：agent（buildPromptInput / renderCandidates / 主动互动目标范围复核）。
type PostCandidate struct {
	Post       model.Post
	AuthorName string
	Username   string
	Summary    string
	Tags       []string
}

// DraftListQuery 按作者分页读取草稿/待发布内容。
// 消费者：agent（ListAIDrafts，一次最多取 limit 条）。
type DraftListQuery struct {
	AuthorID int64
	Limit    int
	Cursor   int64
}

// DraftSummary 是 AI 草稿列表的读取结果；发布状态由 agent 再通过 JobsQueue 补充。
// 消费者：agent（ListAIDrafts / UpdateAIDraft / PublishAIDraft 前置读取）。
type DraftSummary struct {
	Post          model.Post
	ContentHTML   string
	DraftRevision int
}

// ContentService 是 content 模块对外发布的全部跨模块能力。
//
// 消费者：agent、admin、social、notice、media。
// 实现方：modules/content/app 的 App 类型（组合根注入）。
type ContentService interface {
	// ── 写路径（以指定作者身份执行，调用方已鉴权） ──

	// CreatePostForActor 创建草稿/直接发布/定时发布（含版本、标签、搜索、提及）。
	// 消费者：agent（原创/引用动作、AI 生成任务）。
	CreatePostForActor(ctx context.Context, in ActorPostInput) (model.Post, error)

	// PublishPostForActor 立即发布指定草稿；已发布时幂等返回 nil。
	// 消费者：agent（管理员发布 AI 草稿）。
	PublishPostForActor(ctx context.Context, in PublishPostForActorInput) error

	// UpdateDraftForActor 覆盖草稿工作区内容（已发布内容追加新版本）。
	// 消费者：agent（管理员编辑 AI 草稿）。
	UpdateDraftForActor(ctx context.Context, in UpdateDraftForActorInput) error

	// DeletePostAsAdmin 管理员逻辑删除帖子；实现方在同一事务内写入审计。
	// 消费者：admin（内容审核删除）。
	DeletePostAsAdmin(ctx context.Context, in DeletePostInput) error

	// BumpPostCounter 原子增减帖子计数器并返回最新值；实现方保证同一事务内生效。
	// 消费者：social（评论/点赞/转发计数）。
	BumpPostCounter(ctx context.Context, in PostCounterInput) (int, error)

	// AdjustPostCounter 与 BumpPostCounter 同义（social 模块使用的命名）。
	// 新增方法而不改已有签名；实现方两者可共用同一实现。
	// 消费者：social。
	AdjustPostCounter(ctx context.Context, in PostCounterInput) (int, error)

	// ── 读路径（跨模块只读；可见性由 content 的 access 策略统一实现） ──

	// GetPost 按 ID 读取帖子行（不过滤状态/可见性）；不存在返回领域 NotFound。
	// 消费者：social（互动前加载）、agent（PostByID）、admin（详情补充）。
	GetPost(ctx context.Context, postID int64) (model.Post, error)

	// GetPosts 批量读取帖子行，返回 id → Post；缺失的 ID 不出现在 map 中。
	// 消费者：notice（通知预览）、content 自身的时间线合并。
	GetPosts(ctx context.Context, postIDs []int64) (map[int64]model.Post, error)

	// GetVisiblePost 读取帖子并按访问者做可见性校验；不可见时返回领域 NotFound
	// （不暴露资源存在性）。AI 访问者只能看到公开已发布内容。
	// 消费者：social、media（附件继承帖子可见性）、notice（预览）、agent。
	GetVisiblePost(ctx context.Context, viewer model.Viewer, postID int64) (model.Post, error)

	// GetPostExcerpt 读取已发布帖子的纯文本摘录（版本正文优先，搜索投影兜底）。
	// 消费者：agent（触发事件上下文）。
	GetPostExcerpt(ctx context.Context, postID int64) (string, error)

	// GetPostPlainTexts 批量读取帖子搜索投影的纯文本（post_search），用于通知预览。
	// 消费者：notice。
	GetPostPlainTexts(ctx context.Context, postIDs []int64) (map[int64]string, error)

	// ListCandidatePosts 按候选范围读取公开已发布内容候选。
	// 消费者：agent（主动互动决策）。
	ListCandidatePosts(ctx context.Context, query CandidateQuery) ([]PostCandidate, error)

	// ListDraftsByAuthor 按作者读取草稿列表（不含发布状态；由 agent 通过
	// JobsQueue 补充定时发布状态）。
	// 消费者：agent（AI 草稿管理）。
	ListDraftsByAuthor(ctx context.Context, query DraftListQuery) ([]DraftSummary, error)

	// ListPostIDsByAuthor 读取某作者的全部帖子 ID（含草稿/定时/已发布）。
	// 消费者：admin（停用账号时暂停其定时发布作业）。
	ListPostIDsByAuthor(ctx context.Context, authorID int64) ([]int64, error)

	// ListPublicPublishedPostIDsByAuthor 读取某作者「公开且已发布」的帖子 ID
	// （按 published_at DESC, id DESC，最多 limit 条，命中 posts_author_published 索引）。
	// 消费者：social（AI 被动回复触发扫描的候选帖子收窄，避免全量帖子 ID + 整行回读）。
	ListPublicPublishedPostIDsByAuthor(ctx context.Context, authorID int64, limit int) ([]int64, error)

	// ── 附件关联（post_draft_attachments / post_version_attachments 由 content 拥有） ──

	// DetachAttachmentFromDrafts 解除附件与草稿工作区的生效关联，返回是否发生变更。
	// 消费者：media（附件失效流程）。
	DetachAttachmentFromDrafts(ctx context.Context, attachmentID int64, at time.Time) (bool, error)

	// AttachmentReferencedByVersion 判断附件是否仍被任何历史版本引用。
	// 消费者：media（附件失效流程：被历史版本引用时只解除草稿关联，不整体失效）。
	AttachmentReferencedByVersion(ctx context.Context, attachmentID int64) (bool, error)
}
