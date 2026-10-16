package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"

	"github.com/great-magician-01/timeline/internal/access"
	"github.com/great-magician-01/timeline/internal/ai"
	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/social"
)

// 上下文预算（docs/WORKFLOWS.md 7.4、8.4）：先按条数与单条长度裁剪，再按总预算截断。
const (
	maxRecentMemoryCount   = 20
	maxRecentMemoryRunes   = 300
	maxLongTermMemoryCount = 10
	maxCandidateCount      = 8
	maxCandidateRunes      = 400
	// promptBudgetRunes 是用户提示的总预算（按字符计）。
	promptBudgetRunes = 12000
	// maxEventExcerptRunes 是事件正文摘录上限。
	maxEventExcerptRunes = 1200
	// maxThreadCommentCount / maxThreadCommentRunes 是讨论串上下文条数与单条长度。
	maxThreadCommentCount = 5
	maxThreadCommentRunes = 200
	// maxContextKeywords 是长期记忆检索的关键词上限。
	maxContextKeywords = 10
	// maxRelationCandidateCount 是“可取消的关系”候选条数上限（点赞与关注各取最近若干条）。
	maxRelationCandidateCount = 5
)

// 可取消关系的类型标记。
const (
	relationLike   = "like"
	relationFollow = "follow"
)

// sourceContext 是触发事件的来源内容摘要。
type sourceContext struct {
	SourceType  string
	SourceID    int64
	PostID      int64
	AuthorID    int64
	AuthorName  string
	AuthorIsAI  bool
	Excerpt     string
	PostExcerpt string
}

// candidatePost 是一条候选公开内容。
type candidatePost struct {
	ID          int64
	AuthorID    int64
	AuthorName  string
	Username    string
	PublishedAt time.Time
	Summary     string
	Tags        []string
	LikeCount   int
	CommentN    int
	RepostCount int
}

// relationCandidate 是 AI 当前拥有的、可主动取消的关系（取消点赞 / 取关候选）。
// 只有开启了对应动作开关时才会加载，模型也只能从这个列表里选择目标。
type relationCandidate struct {
	Kind     string // relationLike（帖子）或 relationFollow（用户）
	TargetID int64
	Label    string
}

// threadComment 是讨论串中的一条评论。
type threadComment struct {
	ID      int64
	Author  string
	Content string
}

// promptInput 是一次模型调用所需的全部上下文材料。
type promptInput struct {
	TriggerType  string
	Event        string
	Candidates   []candidatePost
	Relations    []relationCandidate
	Recent       []ai.Memory
	LongTerm     []ai.Memory
	InjectedRefs []memoryRef
	Truncated    bool
}

// memoryRef 记录注入过上下文的记忆，用于写入 ai_task_memory_refs。
type memoryRef struct {
	ID         int64
	MemoryType string
}

// scopeParams 是 candidate_scope_params 的已知键（与 internal/ai 的校验一致）。
type scopeParams struct {
	Tags      []string `json:"tags"`
	Usernames []string `json:"usernames"`
}

// parseScopeParams 解析候选范围参数；非法 JSON 视为空参数。
func parseScopeParams(raw json.RawMessage) scopeParams {
	var params scopeParams
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return params
	}
	if err := json.Unmarshal([]byte(trimmed), &params); err != nil {
		return scopeParams{}
	}
	return params
}

// ── 来源内容 ──

// loadSourceContext 读取触发事件的来源内容，只接受公开可见内容（AI 访问者视角）。
func (s *Service) loadSourceContext(
	ctx context.Context,
	q access.Querier,
	aiID int64,
	source SourceRef,
) (sourceContext, error) {
	viewer := aiViewer(aiID)
	switch source.Type {
	case sourcePost:
		post, err := social.LoadVisiblePost(ctx, q, viewer, source.ID)
		if err != nil {
			return sourceContext{}, err
		}
		excerpt, err := s.postExcerpt(ctx, q, post.ID)
		if err != nil {
			return sourceContext{}, err
		}
		author, err := loadActor(ctx, q, post.AuthorID)
		if err != nil {
			return sourceContext{}, err
		}
		return sourceContext{
			SourceType: sourcePost,
			SourceID:   post.ID,
			PostID:     post.ID,
			AuthorID:   post.AuthorID,
			AuthorName: author.displayName(),
			AuthorIsAI: author.IsAI,
			Excerpt:    excerpt,
		}, nil

	case sourceComment:
		var comment struct {
			PostID  int64
			Author  int64
			Excerpt string
		}
		err := q.QueryRow(ctx, `
			SELECT post_id, author_id, COALESCE(plain_text, '')
			FROM comments WHERE id = $1 AND status = 'published'`, source.ID,
		).Scan(&comment.PostID, &comment.Author, &comment.Excerpt)
		if errors.Is(err, pgx.ErrNoRows) {
			return sourceContext{}, api.ErrNotFound("评论不存在或无权访问")
		}
		if err != nil {
			return sourceContext{}, err
		}
		post, err := social.LoadVisiblePost(ctx, q, viewer, comment.PostID)
		if err != nil {
			return sourceContext{}, err
		}
		postExcerpt, err := s.postExcerpt(ctx, q, post.ID)
		if err != nil {
			return sourceContext{}, err
		}
		author, err := loadActor(ctx, q, comment.Author)
		if err != nil {
			return sourceContext{}, err
		}
		return sourceContext{
			SourceType:  sourceComment,
			SourceID:    source.ID,
			PostID:      post.ID,
			AuthorID:    comment.Author,
			AuthorName:  author.displayName(),
			AuthorIsAI:  author.IsAI,
			Excerpt:     comment.Excerpt,
			PostExcerpt: postExcerpt,
		}, nil

	default:
		return sourceContext{}, api.ErrValidation("触发来源类型无效")
	}
}

// postExcerpt 读取已发布帖子的纯文本摘录。
func (s *Service) postExcerpt(ctx context.Context, q access.Querier, postID int64) (string, error) {
	var excerpt string
	err := q.QueryRow(ctx, `
		SELECT COALESCE(NULLIF(v.plain_text, ''), s.plain_text, '')
		FROM posts p
		LEFT JOIN post_versions v ON v.id = p.current_version_id
		LEFT JOIN post_search s ON s.post_id = p.id
		WHERE p.id = $1`, postID).Scan(&excerpt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return excerpt, err
}

// ── 候选内容 ──

// loadCandidates 按候选范围读取公开内容：一律要求 status='published' AND visibility='public'，
// 排除 AI 自己的内容；未开启 AI 间互动时同时排除其他 AI 作者。
func (s *Service) loadCandidates(
	ctx context.Context,
	q access.Querier,
	rt ai.Runtime,
	limit int,
) ([]candidatePost, error) {
	if limit <= 0 || limit > maxCandidateCount {
		limit = maxCandidateCount
	}
	scope := strings.TrimSpace(rt.Behavior.CandidateScopeType)
	if !validCandidateScope(scope) {
		return nil, nil
	}

	where := []string{
		"p.status = 'published'",
		"p.visibility = 'public'",
		"p.author_id <> $1",
		"EXISTS (SELECT 1 FROM users u WHERE u.id = p.author_id AND u.status = 'active')",
	}
	args := []any{rt.AIID}
	addArg := func(value any) string {
		args = append(args, value)
		return "$" + strconv.Itoa(len(args))
	}

	params := parseScopeParams(rt.Behavior.CandidateScopeParams)
	switch scope {
	case scopeFollowed:
		where = append(where, `p.author_id IN (
			SELECT followee_id FROM follows WHERE follower_id = $1 AND is_active)`)
	case scopeTags:
		tags := normalizeTerms(params.Tags, 20)
		if len(tags) == 0 {
			return nil, nil
		}
		where = append(where, `p.id IN (
			SELECT pt.post_id FROM post_tags pt
			JOIN tags t ON t.id = pt.tag_id
			WHERE pt.is_active AND t.name = ANY(`+addArg(tags)+`))`)
	case scopeAuthors:
		usernames := normalizeTerms(params.Usernames, 20)
		if len(usernames) == 0 {
			return nil, nil
		}
		where = append(where, `p.author_id IN (
			SELECT id FROM users WHERE LOWER(username) = ANY(`+addArg(lowerAll(usernames))+`))`)
	}
	if !rt.Behavior.AllowAIToAI {
		where = append(where, `NOT EXISTS (SELECT 1 FROM users u WHERE u.id = p.author_id AND u.is_ai)`)
	}

	args = append(args, limit)
	rows, err := q.Query(ctx, `
		SELECT p.id, p.author_id, COALESCE(u.nickname, ''), u.username, COALESCE(p.published_at, p.created_at),
		       COALESCE(NULLIF(v.plain_text, ''), s.plain_text, ''),
		       COALESCE((SELECT array_agg(t.name ORDER BY t.name)
		                 FROM post_tags pt JOIN tags t ON t.id = pt.tag_id
		                 WHERE pt.post_id = p.id AND pt.is_active), '{}'),
		       p.like_count, p.comment_count, p.repost_count
		FROM posts p
		JOIN users u ON u.id = p.author_id
		LEFT JOIN post_versions v ON v.id = p.current_version_id
		LEFT JOIN post_search s ON s.post_id = p.id
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY p.published_at DESC NULLS LAST, p.id DESC
		LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	candidates := make([]candidatePost, 0, limit)
	for rows.Next() {
		var item candidatePost
		if err := rows.Scan(&item.ID, &item.AuthorID, &item.AuthorName, &item.Username,
			&item.PublishedAt, &item.Summary, &item.Tags,
			&item.LikeCount, &item.CommentN, &item.RepostCount); err != nil {
			return nil, err
		}
		item.Summary = truncateRunes(strings.TrimSpace(item.Summary), maxCandidateRunes)
		candidates = append(candidates, item)
	}
	return candidates, rows.Err()
}

// loadThread 读取公开帖子的最近讨论（用于被提及或收到评论时的上下文）。
func (s *Service) loadThread(
	ctx context.Context,
	q access.Querier,
	postID int64,
	limit int,
) ([]threadComment, error) {
	if postID <= 0 || limit <= 0 {
		return nil, nil
	}
	rows, err := q.Query(ctx, `
		SELECT c.id, COALESCE(u.nickname, ''), COALESCE(c.plain_text, '')
		FROM comments c
		JOIN users u ON u.id = c.author_id
		WHERE c.post_id = $1 AND c.status = 'published' AND u.status = 'active'
		ORDER BY c.id DESC LIMIT $2`, postID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	comments := make([]threadComment, 0, limit)
	for rows.Next() {
		var item threadComment
		if err := rows.Scan(&item.ID, &item.Author, &item.Content); err != nil {
			return nil, err
		}
		item.Content = truncateRunes(strings.TrimSpace(item.Content), maxThreadCommentRunes)
		comments = append(comments, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// 反转成时间正序，便于模型理解讨论进展。
	for left, right := 0, len(comments)-1; left < right; left, right = left+1, right-1 {
		comments[left], comments[right] = comments[right], comments[left]
	}
	return comments, nil
}

// ── 记忆 ──

// loadMemories 读取近期记忆与相关长期记忆；单条长度按预算截断，条数按上限裁剪。
func (s *Service) loadMemories(
	ctx context.Context,
	q access.Querier,
	aiID int64,
	keywords []string,
) ([]ai.Memory, []ai.Memory, error) {
	recent, err := s.ai.RecentMemories(ctx, q, aiID, maxRecentMemoryCount)
	if err != nil {
		return nil, nil, err
	}
	for index := range recent {
		recent[index].Content = truncateRunes(strings.TrimSpace(recent[index].Content), maxRecentMemoryRunes)
	}
	longTerm, err := s.ai.SearchLongTerm(ctx, q, aiID, nil, keywords, maxLongTermMemoryCount)
	if err != nil {
		return nil, nil, err
	}
	for index := range longTerm {
		longTerm[index].Content = truncateRunes(strings.TrimSpace(longTerm[index].Content), maxRecentMemoryRunes)
	}
	return recent, longTerm, nil
}

// memoryRefs 汇总注入的记忆引用；只记录仍有正文的记忆。
func memoryRefs(recent, longTerm []ai.Memory) []memoryRef {
	refs := make([]memoryRef, 0, len(recent)+len(longTerm))
	for _, memory := range recent {
		if strings.TrimSpace(memory.Content) != "" {
			refs = append(refs, memoryRef{ID: memory.ID, MemoryType: "recent"})
		}
	}
	for _, memory := range longTerm {
		if strings.TrimSpace(memory.Content) != "" {
			refs = append(refs, memoryRef{ID: memory.ID, MemoryType: "long_term"})
		}
	}
	return refs
}

// ── 提示词拼装（纯函数，便于测试） ──

// promptPart 是用户提示中的一个片段，数组顺序即优先级（高到低）。
type promptPart struct {
	Name string
	Text string
}

// buildUserPrompt 按优先级拼装用户提示：
// 依次放入片段，放不下时先截断当前片段再放弃后续低优先级片段，总长度不超过 budget 字符。
func buildUserPrompt(parts []promptPart, budget int) (string, bool) {
	if budget <= 0 {
		return "", len(parts) > 0
	}
	var builder strings.Builder
	used := 0
	truncated := false
	for _, part := range parts {
		text := strings.TrimSpace(part.Text)
		if text == "" {
			continue
		}
		separator := 0
		if used > 0 {
			separator = len([]rune(partSeparator))
		}
		remaining := budget - used - separator
		if remaining <= 0 {
			truncated = true
			break
		}
		runes := []rune(text)
		if len(runes) > remaining {
			text = string(runes[:remaining])
			truncated = true
		}
		if used > 0 {
			builder.WriteString(partSeparator)
		}
		builder.WriteString(text)
		used += separator + len([]rune(text))
		if truncated {
			break
		}
	}
	return builder.String(), truncated
}

// partSeparator 是用户提示片段之间的分隔符。
const partSeparator = "\n\n"

// renderIdentity 生成身份与行为策略（系统提示的一部分）。
func renderIdentity(rt ai.Runtime) string {
	var builder strings.Builder
	builder.WriteString("你是中文时间线社区的 AI 用户「")
	builder.WriteString(firstNonEmpty(rt.Nickname, rt.Username, api.FormatID(rt.AIID)))
	builder.WriteString("」。\n")
	writeProfileField(&builder, "身份背景", rt.Profile.Background)
	writeProfileField(&builder, "性格", rt.Profile.Personality)
	writeProfileField(&builder, "价值观", rt.Profile.CoreValues)
	writeProfileField(&builder, "语言风格", rt.Profile.LanguageStyle)
	writeProfileField(&builder, "关注领域", rt.Profile.Topics)
	writeProfileField(&builder, "目标语言", rt.Profile.TargetLanguage)
	writeProfileField(&builder, "禁忌内容", rt.Profile.Taboos)
	writeProfileField(&builder, "自定义设定", rt.Profile.SystemPrompt)
	return builder.String()
}

// renderBehavior 生成本次任务允许的动作与限制说明。
func renderBehavior(rt ai.Runtime) string {
	allowed := make([]string, 0, 8)
	labels := []struct {
		action string
		label  string
	}{
		{actionOriginalPost, "发布原创帖（original_post）"},
		{actionComment, "评论公开内容（comment）"},
		{actionReply, "回复评论（reply）"},
		{actionLike, "点赞（like）"},
		{actionFollow, "关注（follow）"},
		{actionQuote, "引用转发（quote）"},
		{actionRepost, "纯转发（repost）"},
	}
	for _, item := range labels {
		if actionAllowed(rt.Behavior, item.action) {
			allowed = append(allowed, item.label)
		}
	}
	if len(allowed) == 0 {
		return "本次没有开启任何可执行动作，必须选择 noop。"
	}
	var builder strings.Builder
	builder.WriteString("本次允许的动作：")
	builder.WriteString(strings.Join(allowed, "、"))
	builder.WriteString("。\n不允许执行未列出的动作；没有合适的动作时必须选择 noop。\n")
	builder.WriteString(fmt.Sprintf("行为限制：冷却 %d 秒，回复深度上限 %d 层，只允许与公开内容互动。",
		rt.Behavior.CooldownSeconds, rt.Behavior.MaxReplyDepth))
	return builder.String()
}

// renderRecentMemories 渲染近期记忆片段。
func renderRecentMemories(memories []ai.Memory) string {
	if len(memories) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("【近期记忆】\n")
	for _, memory := range memories {
		builder.WriteString("- ")
		builder.WriteString(memory.Content)
		builder.WriteString("\n")
	}
	return builder.String()
}

// renderLongTermMemories 渲染长期记忆片段。
func renderLongTermMemories(memories []ai.Memory) string {
	if len(memories) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("【相关长期记忆】\n")
	for _, memory := range memories {
		builder.WriteString(fmt.Sprintf("- [%s|重要性%d] %s\n", memory.Category, memory.Importance, memory.Content))
	}
	return builder.String()
}

// loadRelations 读取 AI 当前持有的、可主动取消的关系：
// 仅当对应动作开关开启时才加载，避免把不打算使用的信息放进模型上下文。
func (s *Service) loadRelations(ctx context.Context, q access.Querier, rt ai.Runtime) ([]relationCandidate, error) {
	var relations []relationCandidate
	if rt.Behavior.AllowLike {
		rows, err := q.Query(ctx, `
			SELECT l.target_id, COALESCE(ps.plain_text, '')
			FROM likes AS l
			LEFT JOIN posts AS p ON p.id = l.target_id
			LEFT JOIN post_search AS ps ON ps.post_id = p.id
			WHERE l.user_id = $1 AND l.target_type = 'post' AND l.is_active
			  AND p.status = 'published' AND p.visibility = 'public'
			ORDER BY l.id DESC
			LIMIT $2`, rt.AIID, maxRelationCandidateCount)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var item relationCandidate
			var summary string
			if err := rows.Scan(&item.TargetID, &summary); err != nil {
				rows.Close()
				return nil, err
			}
			item.Kind = relationLike
			item.Label = truncateRunes(summary, 80)
			relations = append(relations, item)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	if rt.Behavior.AllowUnfollow {
		rows, err := q.Query(ctx, `
			SELECT f.followee_id, u.username, u.nickname
			FROM follows AS f
			JOIN users AS u ON u.id = f.followee_id
			WHERE f.follower_id = $1 AND f.is_active
			ORDER BY f.id DESC
			LIMIT $2`, rt.AIID, maxRelationCandidateCount)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var item relationCandidate
			var username, nickname string
			if err := rows.Scan(&item.TargetID, &username, &nickname); err != nil {
				rows.Close()
				return nil, err
			}
			item.Kind = relationFollow
			item.Label = firstNonEmpty(nickname, "@"+username)
			relations = append(relations, item)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return relations, nil
}

// renderRelations 渲染可取消的关系片段：模型选择 unlike/unfollow 时必须使用其中的 ID。
func renderRelations(relations []relationCandidate) string {
	if len(relations) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("【可取消的关系】\n")
	builder.WriteString("以下是你自己当前持有的关系；只有这些目标允许 unlike / unfollow：\n")
	for index, item := range relations {
		target := "post_id"
		label := "帖子"
		if item.Kind == relationFollow {
			target = "user_id"
			label = "用户"
		}
		builder.WriteString(fmt.Sprintf("%d) %s=%s（%s：%s）\n",
			index+1, target, api.FormatID(item.TargetID), label, item.Label))
	}
	return builder.String()
}

// renderCandidates 渲染候选内容片段。
func renderCandidates(candidates []candidatePost) string {
	if len(candidates) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("【候选公开内容】\n")
	for index, item := range candidates {
		builder.WriteString(fmt.Sprintf("%d) post_id=%s 作者=%s（@%s） 发布于 %s\n",
			index+1, api.FormatID(item.ID), item.AuthorName, item.Username,
			api.FormatTime(item.PublishedAt)))
		builder.WriteString(fmt.Sprintf("   互动：点赞 %d / 评论 %d / 转发 %d\n",
			item.LikeCount, item.CommentN, item.RepostCount))
		if len(item.Tags) > 0 {
			builder.WriteString("   标签：" + strings.Join(item.Tags, "、") + "\n")
		}
		builder.WriteString("   摘要：" + item.Summary + "\n")
	}
	return builder.String()
}

// renderEvent 渲染当前事件片段。
func renderEvent(triggerType string, source sourceContext, thread []threadComment, instruction string) string {
	var builder strings.Builder
	builder.WriteString("【当前事件】\n")
	switch triggerType {
	case triggerMention:
		builder.WriteString("你在公开内容中被提及，需要决定是否回应。\n")
	case triggerComment:
		builder.WriteString("你的公开内容收到了新的评论，需要决定是否回应。\n")
	case triggerSchedule:
		builder.WriteString("到达了定时原创时间，需要决定是否发布原创内容。\n")
	case triggerProactive:
		builder.WriteString("你正在主动浏览公开内容，需要决定是否互动。\n")
	case triggerManual:
		builder.WriteString("管理员要求你生成内容，需要给出原创帖或选择 noop。\n")
	default:
		builder.WriteString("需要根据当前上下文做出一次决策。\n")
	}
	if source.SourceID > 0 {
		builder.WriteString(fmt.Sprintf("来源类型：%s，来源 ID：%s，作者：%s\n",
			source.SourceType, api.FormatID(source.SourceID), source.AuthorName))
		if source.SourceType == sourceComment {
			builder.WriteString("来源内容（评论）：" + truncateRunes(source.Excerpt, maxEventExcerptRunes) + "\n")
			if strings.TrimSpace(source.PostExcerpt) != "" {
				builder.WriteString("所在帖子摘要：" + truncateRunes(source.PostExcerpt, maxEventExcerptRunes) + "\n")
			}
			builder.WriteString(fmt.Sprintf("所在帖子 ID：%s\n", api.FormatID(source.PostID)))
		} else {
			builder.WriteString("来源内容（帖子）：" + truncateRunes(source.Excerpt, maxEventExcerptRunes) + "\n")
		}
	}
	if len(thread) > 0 {
		builder.WriteString("最近讨论（时间正序）：\n")
		for _, comment := range thread {
			builder.WriteString(fmt.Sprintf("- comment_id=%s %s：%s\n",
				api.FormatID(comment.ID), comment.Author, comment.Content))
		}
	}
	if strings.TrimSpace(instruction) != "" {
		builder.WriteString("附加要求：" + truncateRunes(instruction, 500) + "\n")
	}
	builder.WriteString("动作目标必须来自上方列表；内容使用 Markdown。")
	return builder.String()
}

// writeProfileField 写入一行非空档案字段。
func writeProfileField(builder *strings.Builder, label, value string) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return
	}
	builder.WriteString(label)
	builder.WriteString("：")
	builder.WriteString(trimmed)
	builder.WriteString("\n")
}

// displayName 返回账号展示名。
func (a actor) displayName() string {
	return firstNonEmpty(a.Nickname, a.Username, api.FormatID(a.ID))
}

// normalizeTerms 去空白、去重并限制数量。
func normalizeTerms(values []string, limit int) []string {
	terms := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if seen[key] {
			continue
		}
		seen[key] = true
		terms = append(terms, trimmed)
		if limit > 0 && len(terms) >= limit {
			break
		}
	}
	return terms
}

// lowerAll 返回小写化后的列表。
func lowerAll(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, strings.ToLower(value))
	}
	return out
}

// ── 上下文组装 ──

// buildPromptInput 组装一次决策所需的全部材料：来源事件、讨论串、候选内容与记忆。
// 注入过的记忆会写入 ai_task_memory_refs 并更新 last_used_at（失败只记日志）。
func (s *Service) buildPromptInput(
	ctx context.Context,
	q access.Querier,
	rt ai.Runtime,
	task aiTask,
) (promptInput, error) {
	prompt := promptInput{TriggerType: task.TriggerType}

	var source sourceContext
	hasSource := false
	if task.TriggerRefID != nil &&
		(task.TriggerRefType == sourcePost || task.TriggerRefType == sourceComment) {
		loaded, err := s.loadSourceContext(ctx, q, rt.AIID, SourceRef{
			Type: task.TriggerRefType,
			ID:   *task.TriggerRefID,
		})
		if err != nil {
			return prompt, err
		}
		source = loaded
		hasSource = true
	}

	var thread []threadComment
	if hasSource && source.PostID > 0 {
		loaded, err := s.loadThread(ctx, q, source.PostID, maxThreadCommentCount)
		if err != nil {
			return prompt, err
		}
		thread = loaded
	}

	candidates, err := s.loadCandidates(ctx, q, rt, maxCandidateCount)
	if err != nil {
		return prompt, err
	}
	prompt.Candidates = candidates

	relations, err := s.loadRelations(ctx, q, rt)
	if err != nil {
		return prompt, err
	}
	prompt.Relations = relations

	keywords := make([]string, 0, maxContextKeywords)
	for _, item := range candidates {
		keywords = append(keywords, item.Tags...)
	}
	if hasSource {
		keywords = append(keywords, keywordTerms(source.Excerpt, 5)...)
		keywords = append(keywords, source.AuthorName)
	}
	recent, longTerm, err := s.loadMemories(ctx, q, rt.AIID, normalizeTerms(keywords, maxContextKeywords))
	if err != nil {
		return prompt, err
	}
	prompt.Recent = recent
	prompt.LongTerm = longTerm
	prompt.InjectedRefs = memoryRefs(recent, longTerm)

	event := renderEvent(task.TriggerType, source, thread, "")
	prompt.Event = event
	s.recordMemoryRefs(ctx, task.ID, prompt.InjectedRefs)
	return prompt, nil
}

// assemblePrompts 生成系统提示与用户提示：
// 身份、行为策略与输出要求放在系统提示（始终完整保留），
// 事件、记忆与候选内容按优先级放入用户提示并受总预算约束。
func assemblePrompts(rt ai.Runtime, prompt promptInput) (string, string) {
	systemParts := []string{
		renderIdentity(rt),
		renderBehavior(rt),
		"决策要求：只返回符合结构化输出要求的 JSON 对象；target_id 必须是上方列表中出现过的 ID" +
			"（点赞/评论/转发/引用用【候选公开内容】，unlike/unfollow 只能用【可取消的关系】里的 ID）；" +
			"没有合适的动作时必须选择 noop。",
	}
	system := strings.Join(nonEmpty(systemParts), "\n\n")

	memoryText := strings.TrimSpace(strings.Join(nonEmpty([]string{
		renderRecentMemories(prompt.Recent),
		renderLongTermMemories(prompt.LongTerm),
	}), "\n"))
	user, truncated := buildUserPrompt([]promptPart{
		{Name: "event", Text: prompt.Event},
		{Name: "memory", Text: memoryText},
		{Name: "candidates", Text: renderCandidates(prompt.Candidates)},
		{Name: "relations", Text: renderRelations(prompt.Relations)},
	}, promptBudgetRunes)
	if truncated {
		system += "\n\n注：本次上下文受预算限制，低优先级的记忆或候选内容可能已被截断。"
	}
	return system, user
}

// nonEmpty 过滤空白字符串。
func nonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, strings.TrimSpace(value))
		}
	}
	return out
}

// keywordTerms 从文本中提取用于长期记忆检索的关键词：
// ASCII 词（长度 ≥ 2）与中文片段（长度 ≤ 4 保留整段，更长时切分为 2 字组）。
func keywordTerms(text string, limit int) []string {
	if limit <= 0 {
		limit = maxContextKeywords
	}
	terms := make([]string, 0, limit)
	seen := map[string]bool{}
	appendTerm := func(term string) bool {
		if term == "" || seen[term] {
			return len(terms) >= limit
		}
		seen[term] = true
		terms = append(terms, term)
		return len(terms) >= limit
	}

	var word []rune
	var cjk []rune
	flushWord := func() bool {
		if len(word) >= 2 {
			if appendTerm(strings.ToLower(string(word))) {
				return true
			}
		}
		word = word[:0]
		return false
	}
	flushCJK := func() bool {
		runes := cjk
		cjk = cjk[:0]
		if len(runes) == 0 {
			return false
		}
		if len(runes) <= 4 {
			return appendTerm(string(runes))
		}
		for index := 0; index+2 <= len(runes); index += 2 {
			if appendTerm(string(runes[index : index+2])) {
				return true
			}
		}
		return false
	}

	for _, r := range text {
		switch {
		case unicode.Is(unicode.Han, r):
			if flushWord() {
				return terms
			}
			cjk = append(cjk, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if flushCJK() {
				return terms
			}
			word = append(word, r)
		default:
			if flushWord() {
				return terms
			}
			if flushCJK() {
				return terms
			}
		}
	}
	if flushWord() {
		return terms
	}
	flushCJK()
	return terms
}
