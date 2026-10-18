package repo

import (
	"context"
	"strconv"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/ai/domain"
)

// memoryColumns 是 ai_memories 的行列清单（superseded_by_id 供管理端 DTO 使用）。
const memoryColumns = `id, ai_id, memory_type, status, content, category,
	COALESCE(keywords, '{}'), importance, source_type, COALESCE(source_ref_type, ''),
	source_ref_id, inference_level, superseded_by_id, created_at, updated_at, last_used_at`

// scanMemoryRow 读取一行 ai_memories（含 superseded_by_id）。
type rowScanner interface {
	Scan(dest ...any) error
}

func scanMemoryRow(row rowScanner) (domain.MemoryRecord, error) {
	var record domain.MemoryRecord
	err := row.Scan(&record.Memory.ID, &record.Memory.AID, &record.Memory.MemoryType,
		&record.Memory.Status, &record.Memory.Content, &record.Memory.Category,
		&record.Memory.Keywords, &record.Memory.Importance, &record.Memory.SourceType,
		&record.Memory.SourceRefType, &record.Memory.SourceRefID, &record.Memory.InferenceLevel,
		&record.SupersededByID, &record.Memory.CreatedAt, &record.Memory.UpdatedAt,
		&record.Memory.LastUsedAt)
	return record, err
}

// ListMemoriesFilter 是记忆列表过滤条件。
type ListMemoriesFilter struct {
	AIID       int64
	MemoryType string
	Status     string
	Query      string
	Cursor     int64
	Limit      int
}

// ListMemories 返回管理端记忆列表（按 ID 倒序；调用方多取一条判断 has_more）。
func (s *Store) ListMemories(ctx context.Context, f ListMemoriesFilter) ([]domain.MemoryRecord, error) {
	where := []string{"ai_id = $1", "memory_type = $2", "status = $3"}
	args := []any{f.AIID, f.MemoryType, f.Status}
	addArg := func(value any) string {
		args = append(args, value)
		return "$" + strconv.Itoa(len(args))
	}
	if f.Query != "" {
		placeholder := addArg(f.Query)
		where = append(where, `(STRPOS(LOWER(content), LOWER(`+placeholder+`)) > 0
			OR EXISTS (SELECT 1 FROM unnest(keywords) kw WHERE STRPOS(LOWER(kw), LOWER(`+placeholder+`)) > 0))`)
	}
	if f.Cursor > 0 {
		where = append(where, "id < "+addArg(f.Cursor))
	}
	args = append(args, f.Limit)
	query := `SELECT ` + memoryColumns + ` FROM ai_memories WHERE ` + joinAnd(where) +
		` ORDER BY id DESC LIMIT $` + strconv.Itoa(len(args))
	rows, err := s.exec(ctx).Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := make([]domain.MemoryRecord, 0, f.Limit)
	for rows.Next() {
		record, err := scanMemoryRow(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

// GetMemory 读取单条记忆；不存在返回 pgx.ErrNoRows。
func (s *Store) GetMemory(ctx context.Context, aiID, memoryID int64) (domain.MemoryRecord, error) {
	record, err := scanMemoryRow(s.exec(ctx).QueryRow(ctx,
		`SELECT `+memoryColumns+` FROM ai_memories WHERE id = $1 AND ai_id = $2`, memoryID, aiID))
	return record, normalizeNotFound(err)
}

// MemoryExists 判断记忆是否属于该 AI。
func (s *Store) MemoryExists(ctx context.Context, aiID, memoryID int64) (bool, error) {
	var exists bool
	err := s.exec(ctx).QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM ai_memories WHERE id = $1 AND ai_id = $2)`,
		memoryID, aiID).Scan(&exists)
	return exists, err
}

// MemoryExistsByID 判断记忆是否存在（不校验归属）。
func (s *Store) MemoryExistsByID(ctx context.Context, memoryID int64) (bool, error) {
	var exists bool
	err := s.exec(ctx).QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM ai_memories WHERE id = $1)`, memoryID).Scan(&exists)
	return exists, err
}

// InsertMemoryInput 是写入记忆的参数。
type InsertMemoryInput struct {
	ID             int64
	AIID           int64
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
	At             time.Time
}

// InsertMemory 写入一条记忆（显式写入应用时间）。
func (s *Store) InsertMemory(ctx context.Context, in InsertMemoryInput) error {
	_, err := s.exec(ctx).Exec(ctx, `
		INSERT INTO ai_memories (
			id, ai_id, memory_type, status, content, category, keywords, importance,
			source_type, source_ref_type, source_ref_id, inference_level, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $13)`,
		in.ID, in.AIID, in.MemoryType, in.Status, in.Content, in.Category, in.Keywords,
		in.Importance, in.SourceType, in.SourceRefType, in.SourceRefID, in.InferenceLevel, in.At)
	return err
}

// UpdateMemoryInput 是更新记忆的参数。
type UpdateMemoryInput struct {
	AIID           int64
	MemoryID       int64
	Content        string
	Category       string
	Keywords       []string
	Importance     int
	Status         string
	InferenceLevel string
	At             time.Time
}

// UpdateMemory 更新记忆正文/分类/关键词/重要性/状态与 updated_at。
func (s *Store) UpdateMemory(ctx context.Context, in UpdateMemoryInput) error {
	_, err := s.exec(ctx).Exec(ctx, `
		UPDATE ai_memories
		SET content = $3, category = $4, keywords = $5, importance = $6, status = $7,
		    inference_level = $8, updated_at = $9
		WHERE id = $1 AND ai_id = $2`,
		in.MemoryID, in.AIID, in.Content, in.Category, in.Keywords, in.Importance,
		in.Status, in.InferenceLevel, in.At)
	return err
}

// SoftDeleteMemory 逻辑删除单条记忆。
func (s *Store) SoftDeleteMemory(ctx context.Context, aiID, memoryID int64, at time.Time) error {
	_, err := s.exec(ctx).Exec(ctx, `
		UPDATE ai_memories SET status = 'deleted', updated_at = $3
		WHERE id = $1 AND ai_id = $2`, memoryID, aiID, at)
	return err
}

// ClearMemories 把某类 active 记忆置为 deleted，返回影响行数。
func (s *Store) ClearMemories(ctx context.Context, aiID int64, memoryType string, at time.Time) (int, error) {
	tag, err := s.exec(ctx).Exec(ctx, `
		UPDATE ai_memories SET status = 'deleted', updated_at = $3
		WHERE ai_id = $1 AND memory_type = $2 AND status = 'active'`, aiID, memoryType, at)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// RecentMemories 按时间倒序读取有效的近期记忆。
func (s *Store) RecentMemories(ctx context.Context, aiID int64, limit int) ([]domain.Memory, error) {
	rows, err := s.exec(ctx).Query(ctx, `
		SELECT `+memoryColumns+` FROM ai_memories
		WHERE ai_id = $1 AND memory_type = 'recent' AND status = 'active'
		ORDER BY id DESC LIMIT $2`, aiID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMemoryList(rows)
}

// SearchLongTermInput 是长期记忆检索条件。
type SearchLongTermInput struct {
	AIID       int64
	Categories []string
	Keywords   []string
	Limit      int
}

// SearchLongTerm 用结构化关键词检索长期记忆：关键词数组重叠或正文模糊命中。
func (s *Store) SearchLongTerm(ctx context.Context, in SearchLongTermInput) ([]domain.Memory, error) {
	where := []string{"ai_id = $1", "memory_type = 'long_term'", "status = 'active'"}
	args := []any{in.AIID}
	addArg := func(value any) string {
		args = append(args, value)
		return "$" + strconv.Itoa(len(args))
	}
	terms := domain.NormalizeKeywordTerms(in.Keywords)
	if len(terms) > 0 {
		patterns := make([]string, 0, len(terms))
		for _, term := range terms {
			patterns = append(patterns, "%"+domain.EscapeLikePattern(term)+"%")
		}
		where = append(where, "(keywords && "+addArg(terms)+"::text[] OR content ILIKE ANY("+addArg(patterns)+"::text[]))")
	}
	if categories := domain.NormalizeCategories(in.Categories); len(categories) > 0 {
		where = append(where, "category = ANY("+addArg(categories)+")")
	}
	args = append(args, in.Limit)
	query := `SELECT ` + memoryColumns + ` FROM ai_memories WHERE ` + joinAnd(where) +
		` ORDER BY importance DESC, last_used_at DESC NULLS LAST, created_at DESC LIMIT $` +
		strconv.Itoa(len(args))
	rows, err := s.exec(ctx).Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMemoryList(rows)
}

// TouchMemories 更新 last_used_at（记忆注入上下文后调用）。
func (s *Store) TouchMemories(ctx context.Context, memoryIDs []int64, at time.Time) error {
	if len(memoryIDs) == 0 {
		return nil
	}
	_, err := s.exec(ctx).Exec(ctx,
		`UPDATE ai_memories SET last_used_at = $2 WHERE id = ANY($1)`, memoryIDs, at)
	return err
}

// CountRecentMemories 统计有效近期记忆数量。
func (s *Store) CountRecentMemories(ctx context.Context, aiID int64) (int, error) {
	var count int
	err := s.exec(ctx).QueryRow(ctx, `
		SELECT COUNT(*) FROM ai_memories
		WHERE ai_id = $1 AND memory_type = 'recent' AND status = 'active'`, aiID).Scan(&count)
	return count, err
}

// InvalidateBySource 直接来源（source_ref_*）失效时停用相关记忆。
func (s *Store) InvalidateBySource(
	ctx context.Context,
	aiID int64,
	sourceRefType string,
	sourceRefID int64,
	at time.Time,
) error {
	if sourceRefType == "" || sourceRefID <= 0 {
		return nil
	}
	_, err := s.exec(ctx).Exec(ctx, `
		UPDATE ai_memories
		SET status = 'invalidated', updated_at = $4
		WHERE ai_id = $1 AND source_ref_type = $2 AND source_ref_id = $3 AND status = 'active'`,
		aiID, sourceRefType, sourceRefID, at)
	return err
}

// InsertMemorySources 批量写入记忆来源关联（唯一索引冲突时幂等跳过）。
func (s *Store) InsertMemorySources(ctx context.Context, sources []domain.MemorySource) (int, error) {
	inserted := 0
	for _, source := range sources {
		tag, err := s.exec(ctx).Exec(ctx, `
			INSERT INTO ai_memory_sources (id, memory_id, source_type, source_id, created_at)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (memory_id, source_type, source_id) DO NOTHING`,
			source.ID, source.MemoryID, source.SourceType, source.SourceID, source.CreatedAt)
		if err != nil {
			return inserted, err
		}
		inserted += int(tag.RowsAffected())
	}
	return inserted, nil
}

// ListMemorySources 读取一批记忆记录的内容来源（post/comment 去重）。
func (s *Store) ListMemorySources(ctx context.Context, memoryIDs []int64) ([]domain.MemorySource, error) {
	if len(memoryIDs) == 0 {
		return nil, nil
	}
	rows, err := s.exec(ctx).Query(ctx, `
		SELECT DISTINCT source_type, source_id FROM ai_memory_sources
		WHERE memory_id = ANY($1) AND source_type IN ('post', 'comment')
		ORDER BY source_type, source_id`, memoryIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sources := make([]domain.MemorySource, 0)
	for rows.Next() {
		var source domain.MemorySource
		if err := rows.Scan(&source.SourceType, &source.SourceID); err != nil {
			return nil, err
		}
		sources = append(sources, source)
	}
	return sources, rows.Err()
}

// ListActiveMemorySourceRefs 读取某 AI 生效记忆引用的内容来源（去重、有限条数）：
// 同时覆盖直接引用（ai_memories.source_ref_*）与沉淀时写入的来源关联（ai_memory_sources）。
func (s *Store) ListActiveMemorySourceRefs(ctx context.Context, aiID int64, limit int) ([]domain.MemorySource, error) {
	if aiID <= 0 {
		return nil, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.exec(ctx).Query(ctx, `
		SELECT source_type, source_id FROM (
			SELECT DISTINCT source_ref_type AS source_type, source_ref_id AS source_id
			FROM ai_memories
			WHERE ai_id = $1 AND status = 'active'
			  AND source_ref_type IN ('post', 'comment') AND source_ref_id IS NOT NULL
			UNION
			SELECT DISTINCT s.source_type, s.source_id
			FROM ai_memory_sources AS s
			JOIN ai_memories AS m ON m.id = s.memory_id
			WHERE m.ai_id = $1 AND m.status = 'active'
		) AS sources
		ORDER BY source_type, source_id
		LIMIT $2`, aiID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sources := make([]domain.MemorySource, 0, limit)
	for rows.Next() {
		var source domain.MemorySource
		if err := rows.Scan(&source.SourceType, &source.SourceID); err != nil {
			return nil, err
		}
		sources = append(sources, source)
	}
	return sources, rows.Err()
}

// InvalidateMemoriesBySources 按记忆来源关联批量失效记忆，返回影响行数。
func (s *Store) InvalidateMemoriesBySources(
	ctx context.Context,
	aiID int64,
	sourceTypes []string,
	sourceIDs []int64,
	at time.Time,
) (int, error) {
	if len(sourceTypes) == 0 || len(sourceIDs) == 0 || len(sourceTypes) != len(sourceIDs) {
		return 0, nil
	}
	tag, err := s.exec(ctx).Exec(ctx, `
		UPDATE ai_memories
		SET status = 'invalidated', updated_at = $4
		WHERE ai_id = $1 AND status = 'active' AND id IN (
			SELECT memory_id FROM ai_memory_sources
			WHERE (source_type, source_id) IN (
				SELECT * FROM unnest($2::text[], $3::bigint[])
			)
		)`, aiID, sourceTypes, sourceIDs, at)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// SupersedeMemories 把一批记忆标记为被新记忆取代，返回影响行数。
func (s *Store) SupersedeMemories(
	ctx context.Context,
	aiID int64,
	memoryIDs []int64,
	supersededByID int64,
	at time.Time,
) (int, error) {
	if len(memoryIDs) == 0 {
		return 0, nil
	}
	tag, err := s.exec(ctx).Exec(ctx, `
		UPDATE ai_memories
		SET status = 'superseded', superseded_by_id = $3, updated_at = $4
		WHERE ai_id = $1 AND id = ANY($2) AND status = 'active'`,
		aiID, memoryIDs, supersededByID, at)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// CountMemoriesByType 统计某 AI 有效的近期/长期记忆条数。
func (s *Store) CountMemoriesByType(ctx context.Context, aiID int64) (recent, longTerm int, err error) {
	rows, err := s.exec(ctx).Query(ctx, `
		SELECT memory_type, COUNT(*)
		FROM ai_memories
		WHERE ai_id = $1 AND status = 'active'
		GROUP BY memory_type`, aiID)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var memoryType string
		var count int
		if err := rows.Scan(&memoryType, &count); err != nil {
			return 0, 0, err
		}
		switch memoryType {
		case domain.MemoryTypeRecent:
			recent = count
		case domain.MemoryTypeLongTerm:
			longTerm = count
		}
	}
	return recent, longTerm, rows.Err()
}

// CountAIStates 统计给定 AI 中启用数量（供 admin 概览等场景复用）。
func (s *Store) CountAIStates(ctx context.Context, aiIDs []int64) (int, error) {
	if len(aiIDs) == 0 {
		return 0, nil
	}
	var count int
	err := s.exec(ctx).QueryRow(ctx, `
		SELECT COUNT(*) FROM ai_profiles WHERE user_id = ANY($1) AND is_enabled`, aiIDs).Scan(&count)
	return count, err
}

// scanMemoryList 遍历 pgx.Rows 并读取 Memory（不含 superseded_by_id）。
func scanMemoryList(rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}) ([]domain.Memory, error) {
	memories := make([]domain.Memory, 0)
	for rows.Next() {
		record, err := scanMemoryRow(rows)
		if err != nil {
			return nil, err
		}
		memories = append(memories, record.Memory)
	}
	return memories, rows.Err()
}
