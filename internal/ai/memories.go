package ai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/great-magician-01/timeline/internal/access"
	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/audit"
	"github.com/great-magician-01/timeline/internal/auth"
	"github.com/great-magician-01/timeline/internal/model"
)

// 记忆类型、状态、分类与推断级别（与 ai_memories 的 CHECK 约束一致）。
const (
	memoryTypeRecent   = "recent"
	memoryTypeLongTerm = "long_term"

	memoryStatusActive      = "active"
	memoryStatusSuperseded  = "superseded"
	memoryStatusInvalidated = "invalidated"
	memoryStatusDeleted     = "deleted"

	memorySourceManual = "manual"
)

var validMemoryStatuses = map[string]bool{
	memoryStatusActive:      true,
	memoryStatusSuperseded:  true,
	memoryStatusInvalidated: true,
	memoryStatusDeleted:     true,
}

var validMemoryCategories = map[string]bool{
	"fact":         true,
	"preference":   true,
	"relationship": true,
	"event":        true,
	"topic":        true,
}

var validInferenceLevels = map[string]bool{
	"fact":      true,
	"summary":   true,
	"inference": true,
}

const (
	maxMemoryContentRunes = 1000
	maxMemoryKeywords     = 20
	maxMemoryKeywordRunes = 32
)

// Memory 是 ai_memories 的运行时视图；检索函数只返回 status='active' 的记忆。
type Memory struct {
	ID             int64
	AID            int64
	MemoryType     string
	Status         string
	Content        string
	Category       string
	Keywords       []string
	Importance     int
	SourceType     string
	SourceRefType  string
	SourceRefID    *int64
	InferenceLevel string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	LastUsedAt     *time.Time
}

const memoryColumns = `id, ai_id, memory_type, status, content, category,
	COALESCE(keywords, '{}'), importance, source_type, COALESCE(source_ref_type, ''),
	source_ref_id, inference_level, superseded_by_id, created_at, updated_at, last_used_at`

// memoryRecord 在 Memory 之外保留 superseded_by_id（仅管理端 DTO 使用）。
type memoryRecord struct {
	memory         Memory
	supersededByID *int64
}

func scanMemoryRecord(row model.Row) (memoryRecord, error) {
	var record memoryRecord
	err := row.Scan(&record.memory.ID, &record.memory.AID, &record.memory.MemoryType,
		&record.memory.Status, &record.memory.Content, &record.memory.Category,
		&record.memory.Keywords, &record.memory.Importance, &record.memory.SourceType,
		&record.memory.SourceRefType, &record.memory.SourceRefID, &record.memory.InferenceLevel,
		&record.supersededByID, &record.memory.CreatedAt, &record.memory.UpdatedAt,
		&record.memory.LastUsedAt)
	return record, err
}

func scanMemory(row model.Row) (Memory, error) {
	record, err := scanMemoryRecord(row)
	return record.memory, err
}

// memoryResponse 是记忆的管理端 DTO。
type memoryResponse struct {
	ID             string   `json:"id"`
	MemoryType     string   `json:"memory_type"`
	Status         string   `json:"status"`
	Content        string   `json:"content"`
	Category       string   `json:"category"`
	Keywords       []string `json:"keywords"`
	Importance     int      `json:"importance"`
	SourceType     string   `json:"source_type"`
	SourceRefType  string   `json:"source_ref_type"`
	SourceRefID    *string  `json:"source_ref_id"`
	InferenceLevel string   `json:"inference_level"`
	SupersededByID *string  `json:"superseded_by_id"`
	CreatedAt      string   `json:"created_at"`
	UpdatedAt      string   `json:"updated_at"`
	LastUsedAt     *string  `json:"last_used_at"`
}

func formatOptionalTime(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := api.FormatTime(*value)
	return &formatted
}

func formatOptionalID(value *int64) *string {
	if value == nil {
		return nil
	}
	formatted := api.FormatID(*value)
	return &formatted
}

func memoryResponseFor(memory Memory, supersededByID *int64) memoryResponse {
	keywords := memory.Keywords
	if keywords == nil {
		keywords = []string{}
	}
	return memoryResponse{
		ID:             api.FormatID(memory.ID),
		MemoryType:     memory.MemoryType,
		Status:         memory.Status,
		Content:        memory.Content,
		Category:       memory.Category,
		Keywords:       keywords,
		Importance:     memory.Importance,
		SourceType:     memory.SourceType,
		SourceRefType:  memory.SourceRefType,
		SourceRefID:    formatOptionalID(memory.SourceRefID),
		InferenceLevel: memory.InferenceLevel,
		SupersededByID: formatOptionalID(supersededByID),
		CreatedAt:      api.FormatTime(memory.CreatedAt),
		UpdatedAt:      api.FormatTime(memory.UpdatedAt),
		LastUsedAt:     formatOptionalTime(memory.LastUsedAt),
	}
}

// ── 记忆检索（供 internal/agent 复用；只返回 status='active'） ──

func normalizeMemoryLimit(limit int) int {
	if limit <= 0 {
		return 20
	}
	if limit > 50 {
		return 50
	}
	return limit
}

func normalizeKeywordTerms(keywords []string) []string {
	terms := make([]string, 0, len(keywords))
	seen := map[string]bool{}
	for _, keyword := range keywords {
		trimmed := strings.TrimSpace(keyword)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		terms = append(terms, trimmed)
	}
	return terms
}

func normalizeCategories(categories []string) []string {
	result := make([]string, 0, len(categories))
	seen := map[string]bool{}
	for _, category := range categories {
		trimmed := strings.TrimSpace(category)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		result = append(result, trimmed)
	}
	return result
}

// escapeLikePattern 转义 ILIKE 模式中的通配符，避免关键词被当成模式。
func escapeLikePattern(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(value)
}

func scanMemories(rows pgx.Rows) ([]Memory, error) {
	memories := make([]Memory, 0)
	for rows.Next() {
		memory, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		memories = append(memories, memory)
	}
	return memories, rows.Err()
}

func scanMemoryRecords(rows pgx.Rows) ([]memoryRecord, error) {
	records := make([]memoryRecord, 0)
	for rows.Next() {
		record, err := scanMemoryRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

// RecentMemories 按时间倒序读取有效的近期记忆。
func (s *Service) RecentMemories(ctx context.Context, q access.Querier, aiID int64, limit int) ([]Memory, error) {
	rows, err := q.Query(ctx, `
		SELECT `+memoryColumns+` FROM ai_memories
		WHERE ai_id = $1 AND memory_type = 'recent' AND status = 'active'
		ORDER BY id DESC
		LIMIT $2`, aiID, normalizeMemoryLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMemories(rows)
}

// SearchLongTerm 用结构化关键词检索长期记忆：
// keywords 与 keywords 列做数组重叠，或 content 模糊命中任一关键词模式；
// categories 可选。关键词与分类都为空时退化为按重要性+时间返回。
func (s *Service) SearchLongTerm(
	ctx context.Context,
	q access.Querier,
	aiID int64,
	categories, keywords []string,
	limit int,
) ([]Memory, error) {
	where := []string{"ai_id = $1", "memory_type = 'long_term'", "status = 'active'"}
	args := []any{aiID}
	addArg := func(value any) string {
		args = append(args, value)
		return "$" + strconv.Itoa(len(args))
	}
	terms := normalizeKeywordTerms(keywords)
	if len(terms) > 0 {
		termPlaceholder := addArg(terms)
		patterns := make([]string, 0, len(terms))
		for _, term := range terms {
			patterns = append(patterns, "%"+escapeLikePattern(term)+"%")
		}
		patternPlaceholder := addArg(patterns)
		where = append(where, fmt.Sprintf(
			"(keywords && %s::text[] OR content ILIKE ANY(%s::text[]))",
			termPlaceholder, patternPlaceholder))
	}
	if categories := normalizeCategories(categories); len(categories) > 0 {
		where = append(where, "category = ANY("+addArg(categories)+")")
	}
	args = append(args, normalizeMemoryLimit(limit))
	rows, err := q.Query(ctx, `
		SELECT `+memoryColumns+` FROM ai_memories
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY importance DESC, last_used_at DESC NULLS LAST, created_at DESC
		LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMemories(rows)
}

// TouchMemories 更新 last_used_at（记忆注入上下文后调用）。
func (s *Service) TouchMemories(ctx context.Context, q access.Querier, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := q.Exec(ctx, `UPDATE ai_memories SET last_used_at = now() WHERE id = ANY($1)`, ids)
	return err
}

// CountRecentMemories 统计有效近期记忆数量，用于判断是否触发沉淀。
func (s *Service) CountRecentMemories(ctx context.Context, q access.Querier, aiID int64) (int, error) {
	var count int
	err := q.QueryRow(ctx, `
		SELECT COUNT(*) FROM ai_memories
		WHERE ai_id = $1 AND memory_type = 'recent' AND status = 'active'`, aiID).Scan(&count)
	return count, err
}

// InvalidateBySource 来源内容变为非公开或被删除时停用相关记忆。
func (s *Service) InvalidateBySource(
	ctx context.Context,
	q access.Querier,
	aiID int64,
	sourceRefType string,
	sourceRefID int64,
) error {
	if strings.TrimSpace(sourceRefType) == "" || sourceRefID <= 0 {
		return nil
	}
	_, err := q.Exec(ctx, `
		UPDATE ai_memories
		SET status = 'invalidated', updated_at = now()
		WHERE ai_id = $1 AND source_ref_type = $2 AND source_ref_id = $3 AND status = 'active'`,
		aiID, sourceRefType, sourceRefID)
	return err
}

// ── 管理接口 ──

// ListMemories 返回某个 AI 的记忆列表：type 必填，status 默认 active。
func (s *Service) ListMemories(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	aiID, ok := parsePathID(c, "id", "无效的 AI ID")
	if !ok {
		return
	}
	if err := ensureAIUser(c.Request.Context(), pool, aiID); err != nil {
		if !api.Respond(c, err) {
			s.internalError(c, "校验 AI 用户失败", err)
		}
		return
	}
	memoryType := strings.TrimSpace(c.Query("type"))
	if memoryType != memoryTypeRecent && memoryType != memoryTypeLongTerm {
		api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", "type 必须是 recent 或 long_term",
			gin.H{"type": "type 必须是 recent 或 long_term"})
		return
	}
	status := strings.TrimSpace(c.Query("status"))
	if status == "" {
		status = memoryStatusActive
	}
	if !validMemoryStatuses[status] {
		api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", "status 取值无效",
			gin.H{"status": "status 只能是 active、superseded、invalidated 或 deleted"})
		return
	}
	limit, err := api.PageLimit(c)
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	cursor, err := api.TrimPageCursor(c)
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	where := []string{"ai_id = $1", "memory_type = $2", "status = $3"}
	args := []any{aiID, memoryType, status}
	addArg := func(value any) string {
		args = append(args, value)
		return "$" + strconv.Itoa(len(args))
	}
	if q := strings.TrimSpace(c.Query("q")); q != "" {
		if utf8.RuneCountInString(q) > 100 {
			api.Error(c, http.StatusBadRequest, "invalid_request", "搜索关键词过长")
			return
		}
		placeholder := addArg(q)
		where = append(where, fmt.Sprintf(`(
			STRPOS(LOWER(content), LOWER(%s)) > 0 OR
			EXISTS (SELECT 1 FROM unnest(keywords) kw WHERE STRPOS(LOWER(kw), LOWER(%s)) > 0)
		)`, placeholder, placeholder))
	}
	if cursor > 0 {
		where = append(where, "id < "+addArg(cursor))
	}
	args = append(args, limit+1)
	limitPlaceholder := "$" + strconv.Itoa(len(args))

	rows, err := pool.Query(c.Request.Context(), `
		SELECT `+memoryColumns+` FROM ai_memories
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY id DESC LIMIT `+limitPlaceholder, args...)
	if err != nil {
		s.internalError(c, "查询 AI 记忆列表失败", err)
		return
	}
	defer rows.Close()
	records, err := scanMemoryRecords(rows)
	if err != nil {
		s.internalError(c, "读取 AI 记忆列表失败", err)
		return
	}
	hasMore := len(records) > limit
	if hasMore {
		records = records[:limit]
	}
	items := make([]memoryResponse, 0, len(records))
	for _, record := range records {
		items = append(items, memoryResponseFor(record.memory, record.supersededByID))
	}
	var lastID int64
	if len(records) > 0 {
		lastID = records[len(records)-1].memory.ID
	}
	c.JSON(http.StatusOK, api.ListResponse(items, lastID, hasMore))
}

// normalizeMemoryWrite 校验并归一化记忆正文、分类、关键词、重要性与推断级别。
// 返回字段级错误详情，空 map 表示通过。
func normalizeMemoryWrite(
	content string,
	category string,
	keywords []string,
	importance *int,
	inferenceLevel string,
) (string, string, []string, int, string, map[string]string) {
	details := map[string]string{}
	trimmedContent := strings.TrimSpace(content)
	if runes := utf8.RuneCountInString(trimmedContent); runes < 1 || runes > maxMemoryContentRunes {
		details["content"] = "记忆正文需为 1-1000 个字符"
	}
	finalCategory := strings.TrimSpace(category)
	if finalCategory == "" {
		finalCategory = "event"
	} else if !validMemoryCategories[finalCategory] {
		details["category"] = "分类只能是 fact、preference、relationship、event 或 topic"
	}
	normalizedKeywords := make([]string, 0, len(keywords))
	seen := map[string]bool{}
	for _, keyword := range keywords {
		trimmed := strings.TrimSpace(keyword)
		if trimmed == "" {
			continue
		}
		if utf8.RuneCountInString(trimmed) > maxMemoryKeywordRunes {
			details["keywords"] = "单个关键词不能超过 32 个字符"
			break
		}
		folded := strings.ToLower(trimmed)
		if seen[folded] {
			continue
		}
		seen[folded] = true
		normalizedKeywords = append(normalizedKeywords, trimmed)
	}
	if len(normalizedKeywords) > maxMemoryKeywords {
		details["keywords"] = "关键词最多 20 个"
	}
	finalImportance := 3
	if importance != nil {
		finalImportance = *importance
		if finalImportance < 1 || finalImportance > 5 {
			details["importance"] = "重要性必须是 1-5 的整数"
		}
	}
	finalInference := strings.TrimSpace(inferenceLevel)
	if finalInference == "" {
		finalInference = "fact"
	} else if !validInferenceLevels[finalInference] {
		details["inference_level"] = "推断级别只能是 fact、summary 或 inference"
	}
	return trimmedContent, finalCategory, normalizedKeywords, finalImportance, finalInference, details
}

// CreateMemory 手工添加记忆（source_type 固定为 manual）。
func (s *Service) CreateMemory(c *gin.Context) {
	aiID, ok := parsePathID(c, "id", "无效的 AI ID")
	if !ok {
		return
	}
	var in struct {
		MemoryType     string   `json:"memory_type"`
		Content        string   `json:"content"`
		Category       string   `json:"category"`
		Keywords       []string `json:"keywords"`
		Importance     *int     `json:"importance"`
		InferenceLevel string   `json:"inference_level"`
	}
	if err := api.DecodeJSON(c, &in); err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", "请求体必须是有效的 JSON 对象，且不能包含未知字段")
		return
	}
	details := map[string]string{}
	if in.MemoryType != memoryTypeRecent && in.MemoryType != memoryTypeLongTerm {
		details["memory_type"] = "memory_type 必须是 recent 或 long_term"
	}
	content, category, keywords, importance, inferenceLevel, writeDetails := normalizeMemoryWrite(
		in.Content, in.Category, in.Keywords, in.Importance, in.InferenceLevel)
	for key, message := range writeDetails {
		details[key] = message
	}
	if len(details) > 0 {
		api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", firstDetail(details), details)
		return
	}
	adminUser, _ := auth.CurrentUser(c)
	ctx := c.Request.Context()

	tx, ok := s.beginTx(c, "开启创建记忆事务失败")
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := ensureAIUser(ctx, tx, aiID); err != nil {
		if !api.Respond(c, err) {
			s.internalError(c, "校验 AI 用户失败", err)
		}
		return
	}
	memoryID, err := s.ids.Next()
	if err != nil {
		s.internalError(c, "生成记忆 ID 失败", err)
		return
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO ai_memories (
			id, ai_id, memory_type, status, content, category, keywords, importance,
			source_type, source_ref_type, source_ref_id, inference_level
		) VALUES ($1, $2, $3, 'active', $4, $5, $6, $7, 'manual', NULL, NULL, $8)`,
		memoryID, aiID, in.MemoryType, content, category, keywords, importance, inferenceLevel,
	); err != nil {
		s.internalError(c, "创建记忆失败", err)
		return
	}
	if err = s.writeAudit(ctx, tx, adminUser.ID, audit.ActionMemoryCreate, targetAIMemory, &memoryID, gin.H{
		"ai_id":       api.FormatID(aiID),
		"memory_type": in.MemoryType,
		"category":    category,
	}); err != nil {
		s.internalError(c, "写入记忆创建审计失败", err)
		return
	}
	record, err := scanMemoryRecord(tx.QueryRow(ctx,
		`SELECT `+memoryColumns+` FROM ai_memories WHERE id = $1`, memoryID))
	if err != nil {
		s.internalError(c, "读取新建记忆失败", err)
		return
	}
	if !s.commitTx(c, tx, "提交创建记忆事务失败") {
		return
	}
	c.JSON(http.StatusCreated, gin.H{"memory": memoryResponseFor(record.memory, record.supersededByID)})
}

// UpdateMemory 编辑记忆正文、分类、关键词、重要性与状态。
func (s *Service) UpdateMemory(c *gin.Context) {
	aiID, ok := parsePathID(c, "id", "无效的 AI ID")
	if !ok {
		return
	}
	memoryID, ok := parsePathID(c, "memory_id", "无效的记忆 ID")
	if !ok {
		return
	}
	var in struct {
		Content    *string   `json:"content"`
		Category   *string   `json:"category"`
		Keywords   *[]string `json:"keywords"`
		Importance *int      `json:"importance"`
		Status     *string   `json:"status"`
	}
	if err := api.DecodeJSON(c, &in); err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", "请求体必须是有效的 JSON 对象，且不能包含未知字段")
		return
	}
	adminUser, _ := auth.CurrentUser(c)
	ctx := c.Request.Context()

	tx, ok := s.beginTx(c, "开启更新记忆事务失败")
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	current, err := scanMemory(tx.QueryRow(ctx,
		`SELECT `+memoryColumns+` FROM ai_memories WHERE id = $1 AND ai_id = $2`, memoryID, aiID))
	if errors.Is(err, pgx.ErrNoRows) {
		api.Error(c, http.StatusNotFound, "not_found", "记忆不存在")
		return
	}
	if err != nil {
		s.internalError(c, "读取待更新记忆失败", err)
		return
	}
	content := current.Content
	if in.Content != nil {
		content = *in.Content
	}
	category := current.Category
	if in.Category != nil {
		category = *in.Category
	}
	keywords := current.Keywords
	if in.Keywords != nil {
		keywords = *in.Keywords
	}
	importance := current.Importance
	if in.Importance != nil {
		importance = *in.Importance
	}
	status := current.Status
	if in.Status != nil {
		status = strings.TrimSpace(*in.Status)
	}
	finalContent, finalCategory, finalKeywords, finalImportance, finalInference, details :=
		normalizeMemoryWrite(content, category, keywords, &importance, current.InferenceLevel)
	if !validMemoryStatuses[status] {
		details["status"] = "status 只能是 active、superseded、invalidated 或 deleted"
	}
	if len(details) > 0 {
		api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", firstDetail(details), details)
		return
	}
	if _, err = tx.Exec(ctx, `
		UPDATE ai_memories
		SET content = $3, category = $4, keywords = $5, importance = $6, status = $7,
		    inference_level = $8, updated_at = now()
		WHERE id = $1 AND ai_id = $2`,
		memoryID, aiID, finalContent, finalCategory, finalKeywords, finalImportance, status, finalInference,
	); err != nil {
		s.internalError(c, "更新记忆失败", err)
		return
	}
	if err = s.writeAudit(ctx, tx, adminUser.ID, audit.ActionMemoryUpdate, targetAIMemory, &memoryID, gin.H{
		"ai_id":  api.FormatID(aiID),
		"status": status,
	}); err != nil {
		s.internalError(c, "写入记忆更新审计失败", err)
		return
	}
	updated, err := scanMemoryRecord(tx.QueryRow(ctx,
		`SELECT `+memoryColumns+` FROM ai_memories WHERE id = $1`, memoryID))
	if err != nil {
		s.internalError(c, "读取更新后的记忆失败", err)
		return
	}
	if !s.commitTx(c, tx, "提交更新记忆事务失败") {
		return
	}
	c.JSON(http.StatusOK, gin.H{"memory": memoryResponseFor(updated.memory, updated.supersededByID)})
}

// DeleteMemory 逻辑删除单条记忆（status = 'deleted'）。
func (s *Service) DeleteMemory(c *gin.Context) {
	aiID, ok := parsePathID(c, "id", "无效的 AI ID")
	if !ok {
		return
	}
	memoryID, ok := parsePathID(c, "memory_id", "无效的记忆 ID")
	if !ok {
		return
	}
	adminUser, _ := auth.CurrentUser(c)
	ctx := c.Request.Context()

	tx, ok := s.beginTx(c, "开启删除记忆事务失败")
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM ai_memories WHERE id = $1 AND ai_id = $2)`,
		memoryID, aiID).Scan(&exists); err != nil {
		s.internalError(c, "读取待删除记忆失败", err)
		return
	}
	if !exists {
		api.Error(c, http.StatusNotFound, "not_found", "记忆不存在")
		return
	}
	if _, err := tx.Exec(ctx, `
		UPDATE ai_memories SET status = 'deleted', updated_at = now()
		WHERE id = $1 AND ai_id = $2`, memoryID, aiID); err != nil {
		s.internalError(c, "删除记忆失败", err)
		return
	}
	if err := s.writeAudit(ctx, tx, adminUser.ID, audit.ActionMemoryDelete, targetAIMemory, &memoryID,
		gin.H{"ai_id": api.FormatID(aiID)}); err != nil {
		s.internalError(c, "写入记忆删除审计失败", err)
		return
	}
	if !s.commitTx(c, tx, "提交删除记忆事务失败") {
		return
	}
	c.Status(http.StatusNoContent)
}

// ClearMemories 清空某类记忆：只把该类 active 记忆置为 deleted，两类互不影响。
func (s *Service) ClearMemories(c *gin.Context) {
	aiID, ok := parsePathID(c, "id", "无效的 AI ID")
	if !ok {
		return
	}
	var in struct {
		Type string `json:"type"`
	}
	if err := api.DecodeJSON(c, &in); err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", "请求体必须是有效的 JSON 对象，且不能包含未知字段")
		return
	}
	memoryType := strings.TrimSpace(in.Type)
	if memoryType != memoryTypeRecent && memoryType != memoryTypeLongTerm {
		api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", "type 必须是 recent 或 long_term",
			gin.H{"type": "type 必须是 recent 或 long_term"})
		return
	}
	adminUser, _ := auth.CurrentUser(c)
	ctx := c.Request.Context()

	tx, ok := s.beginTx(c, "开启清空记忆事务失败")
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := ensureAIUser(ctx, tx, aiID); err != nil {
		if !api.Respond(c, err) {
			s.internalError(c, "校验 AI 用户失败", err)
		}
		return
	}
	tag, err := tx.Exec(ctx, `
		UPDATE ai_memories SET status = 'deleted', updated_at = now()
		WHERE ai_id = $1 AND memory_type = $2 AND status = 'active'`, aiID, memoryType)
	if err != nil {
		s.internalError(c, "清空记忆失败", err)
		return
	}
	updated := int(tag.RowsAffected())
	if err = s.writeAudit(ctx, tx, adminUser.ID, audit.ActionMemoryClear, targetAIMemory, &aiID, gin.H{
		"ai_id":       api.FormatID(aiID),
		"memory_type": memoryType,
		"updated":     updated,
	}); err != nil {
		s.internalError(c, "写入清空记忆审计失败", err)
		return
	}
	if !s.commitTx(c, tx, "提交清空记忆事务失败") {
		return
	}
	c.JSON(http.StatusOK, gin.H{"updated": updated})
}
