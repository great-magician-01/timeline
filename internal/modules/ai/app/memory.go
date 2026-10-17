package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/ai/domain"
	"github.com/great-magician-01/timeline/internal/modules/ai/repo"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/contracts"
)

// MemoryFilter 是记忆列表过滤条件。
type MemoryFilter struct {
	MemoryType string
	Status     string
	Query      string
	Cursor     int64
	Limit      int
}

// MemoryList 是记忆列表结果。
type MemoryList struct {
	Items   []domain.MemoryRecord
	LastID  int64
	HasMore bool
}

// ListMemories 返回某个 AI 的记忆列表：type 必填，status 默认 active。
func (s *Service) ListMemories(ctx context.Context, aiID int64, filter MemoryFilter) (MemoryList, error) {
	if err := s.ensureAIUser(ctx, aiID); err != nil {
		return MemoryList{}, err
	}
	memoryType := strings.TrimSpace(filter.MemoryType)
	if !domain.ValidMemoryTypes[memoryType] {
		return MemoryList{}, apperr.Validation("type 必须是 recent 或 long_term").
			WithDetails(map[string]string{"type": "type 必须是 recent 或 long_term"})
	}
	status := strings.TrimSpace(filter.Status)
	if status == "" {
		status = domain.MemoryStatusActive
	}
	if !domain.ValidMemoryStatuses[status] {
		return MemoryList{}, apperr.Validation("status 取值无效").
			WithDetails(map[string]string{"status": "status 只能是 active、superseded、invalidated 或 deleted"})
	}
	records, err := s.repo.ListMemories(ctx, repo.ListMemoriesFilter{
		AIID:       aiID,
		MemoryType: memoryType,
		Status:     status,
		Query:      strings.TrimSpace(filter.Query),
		Cursor:     filter.Cursor,
		Limit:      filter.Limit + 1,
	})
	if err != nil {
		return MemoryList{}, internalError(err, "查询 AI 记忆列表失败")
	}
	hasMore := len(records) > filter.Limit
	if hasMore {
		records = records[:filter.Limit]
	}
	var lastID int64
	if len(records) > 0 {
		lastID = records[len(records)-1].Memory.ID
	}
	return MemoryList{Items: records, LastID: lastID, HasMore: hasMore}, nil
}

// CreateMemoryRequest 是管理端手工添加记忆的请求数据。
type CreateMemoryRequest struct {
	MemoryType     string
	Content        string
	Category       string
	Keywords       []string
	Importance     *int
	InferenceLevel string
}

// CreateAIMemory 是管理端手工添加记忆用例（HTTP handler 方法名为 CreateMemory）。
func (s *Service) CreateAIMemory(
	ctx context.Context,
	adminID, aiID int64,
	in CreateMemoryRequest,
) (domain.MemoryRecord, error) {
	details := map[string]string{}
	memoryType := strings.TrimSpace(in.MemoryType)
	if !domain.ValidMemoryTypes[memoryType] {
		details["memory_type"] = "memory_type 必须是 recent 或 long_term"
	}
	write, writeDetails := domain.NormalizeMemoryWrite(in.Content, in.Category, in.Keywords,
		in.Importance, in.InferenceLevel)
	for key, message := range writeDetails {
		details[key] = message
	}
	if len(details) > 0 {
		return domain.MemoryRecord{}, validationError(details)
	}
	at := s.now()
	var record domain.MemoryRecord
	err := s.withTx(ctx, func(txCtx context.Context) error {
		if err := s.ensureAIUser(txCtx, aiID); err != nil {
			return err
		}
		id, err := s.newID()
		if err != nil {
			return err
		}
		if err := s.repo.InsertMemory(txCtx, repo.InsertMemoryInput{
			ID:             id,
			AIID:           aiID,
			MemoryType:     memoryType,
			Status:         domain.MemoryStatusActive,
			Content:        write.Content,
			Category:       write.Category,
			Keywords:       write.Keywords,
			Importance:     write.Importance,
			SourceType:     domain.MemorySourceManual,
			InferenceLevel: write.InferenceLevel,
			At:             at,
		}); err != nil {
			return internalError(err, "创建记忆失败")
		}
		if err := s.writeAudit(txCtx, adminID, contracts.AuditActionMemoryCreate, targetAIMemory, &id,
			map[string]any{"ai_id": formatID(aiID), "memory_type": memoryType, "category": write.Category}, at); err != nil {
			return err
		}
		created, err := s.repo.GetMemory(txCtx, aiID, id)
		if err != nil {
			return internalError(err, "读取新建记忆失败")
		}
		record = created
		return nil
	})
	if err != nil {
		return domain.MemoryRecord{}, err
	}
	return record, nil
}

// UpdateMemoryRequest 是编辑记忆的补丁；nil 表示不变更。
type UpdateMemoryRequest struct {
	Content    *string
	Category   *string
	Keywords   *[]string
	Importance *int
	Status     *string
}

// UpdateMemory 编辑记忆正文、分类、关键词、重要性与状态。
func (s *Service) UpdateMemory(
	ctx context.Context,
	adminID, aiID, memoryID int64,
	in UpdateMemoryRequest,
) (domain.MemoryRecord, error) {
	at := s.now()
	var record domain.MemoryRecord
	err := s.withTx(ctx, func(txCtx context.Context) error {
		current, err := s.repo.GetMemory(txCtx, aiID, memoryID)
		if errors.Is(err, repo.ErrNotFound) {
			return apperr.NotFound("记忆不存在")
		}
		if err != nil {
			return internalError(err, "读取待更新记忆失败")
		}
		memory := current.Memory
		content := memory.Content
		if in.Content != nil {
			content = *in.Content
		}
		category := memory.Category
		if in.Category != nil {
			category = *in.Category
		}
		keywords := memory.Keywords
		if in.Keywords != nil {
			keywords = *in.Keywords
		}
		importance := memory.Importance
		if in.Importance != nil {
			importance = *in.Importance
		}
		status := memory.Status
		if in.Status != nil {
			status = strings.TrimSpace(*in.Status)
		}
		write, details := domain.NormalizeMemoryWrite(content, category, keywords, &importance,
			memory.InferenceLevel)
		if !domain.ValidMemoryStatuses[status] {
			details["status"] = "status 只能是 active、superseded、invalidated 或 deleted"
		}
		if len(details) > 0 {
			return validationError(details)
		}
		if err := s.repo.UpdateMemory(txCtx, repo.UpdateMemoryInput{
			AIID:           aiID,
			MemoryID:       memoryID,
			Content:        write.Content,
			Category:       write.Category,
			Keywords:       write.Keywords,
			Importance:     write.Importance,
			Status:         status,
			InferenceLevel: write.InferenceLevel,
			At:             at,
		}); err != nil {
			return internalError(err, "更新记忆失败")
		}
		if err := s.writeAudit(txCtx, adminID, contracts.AuditActionMemoryUpdate, targetAIMemory, &memoryID,
			map[string]any{"ai_id": formatID(aiID), "status": status}, at); err != nil {
			return err
		}
		updated, err := s.repo.GetMemory(txCtx, aiID, memoryID)
		if err != nil {
			return internalError(err, "读取更新后的记忆失败")
		}
		record = updated
		return nil
	})
	if err != nil {
		return domain.MemoryRecord{}, err
	}
	return record, nil
}

// DeleteMemory 逻辑删除单条记忆（status = 'deleted'）。
func (s *Service) DeleteMemory(ctx context.Context, adminID, aiID, memoryID int64) error {
	at := s.now()
	return s.withTx(ctx, func(txCtx context.Context) error {
		exists, err := s.repo.MemoryExists(txCtx, aiID, memoryID)
		if err != nil {
			return internalError(err, "读取待删除记忆失败")
		}
		if !exists {
			return apperr.NotFound("记忆不存在")
		}
		if err := s.repo.SoftDeleteMemory(txCtx, aiID, memoryID, at); err != nil {
			return internalError(err, "删除记忆失败")
		}
		return s.writeAudit(txCtx, adminID, contracts.AuditActionMemoryDelete, targetAIMemory, &memoryID,
			map[string]any{"ai_id": formatID(aiID)}, at)
	})
}

// ClearMemories 清空某类记忆：只把该类 active 记忆置为 deleted，两类互不影响。
func (s *Service) ClearMemories(ctx context.Context, adminID, aiID int64, memoryType string) (int, error) {
	memoryType = strings.TrimSpace(memoryType)
	if !domain.ValidMemoryTypes[memoryType] {
		return 0, apperr.Validation("type 必须是 recent 或 long_term").
			WithDetails(map[string]string{"type": "type 必须是 recent 或 long_term"})
	}
	at := s.now()
	updated := 0
	err := s.withTx(ctx, func(txCtx context.Context) error {
		if err := s.ensureAIUser(txCtx, aiID); err != nil {
			return err
		}
		count, err := s.repo.ClearMemories(txCtx, aiID, memoryType, at)
		if err != nil {
			return internalError(err, "清空记忆失败")
		}
		updated = count
		return s.writeAudit(txCtx, adminID, contracts.AuditActionMemoryClear, targetAIMemory, &aiID,
			map[string]any{"ai_id": formatID(aiID), "memory_type": memoryType, "updated": count}, at)
	})
	if err != nil {
		return 0, err
	}
	return updated, nil
}

// ── contracts.AIService 记忆能力 ──

// RecentMemories 读取有效近期记忆（按 ID 倒序）。消费者：agent。
func (s *Service) RecentMemories(ctx context.Context, aiID int64, limit int) ([]contracts.Memory, error) {
	memories, err := s.repo.RecentMemories(ctx, aiID, domain.NormalizeMemoryLimit(limit))
	if err != nil {
		return nil, internalError(err, "读取近期记忆失败")
	}
	return memoriesToContracts(memories), nil
}

// SearchLongTerm 按分类/关键词检索有效长期记忆。消费者：agent。
func (s *Service) SearchLongTerm(ctx context.Context, in contracts.SearchLongTermInput) ([]contracts.Memory, error) {
	memories, err := s.repo.SearchLongTerm(ctx, repo.SearchLongTermInput{
		AIID:       in.AIID,
		Categories: in.Categories,
		Keywords:   in.Keywords,
		Limit:      domain.NormalizeMemoryLimit(in.Limit),
	})
	if err != nil {
		return nil, internalError(err, "检索长期记忆失败")
	}
	return memoriesToContracts(memories), nil
}

// TouchMemories 更新记忆最近使用时间。消费者：agent。
func (s *Service) TouchMemories(ctx context.Context, memoryIDs []int64, at time.Time) error {
	if err := s.repo.TouchMemories(ctx, memoryIDs, at); err != nil {
		return internalError(err, "更新记忆使用时间失败")
	}
	return nil
}

// CountRecentMemories 统计有效近期记忆数量（沉淀触发判断）。消费者：agent。
func (s *Service) CountRecentMemories(ctx context.Context, aiID int64) (int, error) {
	count, err := s.repo.CountRecentMemories(ctx, aiID)
	if err != nil {
		return 0, internalError(err, "统计近期记忆失败")
	}
	return count, nil
}

// InvalidateBySource 来源内容变为非公开/删除时停用相关记忆。消费者：agent。
func (s *Service) InvalidateBySource(ctx context.Context, in contracts.InvalidateBySourceInput) error {
	if err := s.repo.InvalidateBySource(ctx, in.AIID, in.SourceType, in.SourceID, in.At); err != nil {
		return internalError(err, "失效来源记忆失败")
	}
	return nil
}

// CreateMemory 写入一条记忆并返回写入后的记忆行。消费者：agent（记忆形成/沉淀）。
func (s *Service) CreateMemory(ctx context.Context, in contracts.CreateMemoryInput) (contracts.Memory, error) {
	memoryType := strings.TrimSpace(in.MemoryType)
	if !domain.ValidMemoryTypes[memoryType] {
		return contracts.Memory{}, apperr.Invalid("memory_type 必须是 recent 或 long_term")
	}
	importance := in.Importance
	write, details := domain.NormalizeMemoryWrite(in.Content, in.Category, in.Keywords, &importance,
		in.InferenceLevel)
	if len(details) > 0 {
		return contracts.Memory{}, validationError(details)
	}
	sourceType := strings.TrimSpace(in.SourceType)
	if sourceType == "" {
		sourceType = domain.MemorySourceAction
	}
	id, err := s.newID()
	if err != nil {
		return contracts.Memory{}, err
	}
	at := in.At
	if at.IsZero() {
		at = s.now()
	}
	if err := s.repo.InsertMemory(ctx, repo.InsertMemoryInput{
		ID:             id,
		AIID:           in.AIID,
		MemoryType:     memoryType,
		Status:         domain.MemoryStatusActive,
		Content:        write.Content,
		Category:       write.Category,
		Keywords:       write.Keywords,
		Importance:     write.Importance,
		SourceType:     sourceType,
		SourceRefType:  strings.TrimSpace(in.SourceRefType),
		SourceRefID:    in.SourceRefID,
		InferenceLevel: write.InferenceLevel,
		At:             at,
	}); err != nil {
		return contracts.Memory{}, internalError(err, "写入记忆失败")
	}
	return contracts.Memory{
		ID:             id,
		AID:            in.AIID,
		MemoryType:     memoryType,
		Status:         domain.MemoryStatusActive,
		Content:        write.Content,
		Category:       write.Category,
		Keywords:       write.Keywords,
		Importance:     write.Importance,
		SourceType:     sourceType,
		SourceRefType:  strings.TrimSpace(in.SourceRefType),
		SourceRefID:    in.SourceRefID,
		InferenceLevel: write.InferenceLevel,
		CreatedAt:      at,
		UpdatedAt:      at,
	}, nil
}

// AddMemorySources 写入记忆与来源内容的关联。消费者：agent。
func (s *Service) AddMemorySources(ctx context.Context, memoryID int64, sources []contracts.MemorySourceInput) error {
	if len(sources) == 0 {
		return nil
	}
	exists, err := s.repo.MemoryExistsByID(ctx, memoryID)
	if err != nil {
		return internalError(err, "读取记忆失败")
	}
	if !exists {
		return apperr.NotFound("记忆不存在")
	}
	at := s.now()
	records := make([]domain.MemorySource, 0, len(sources))
	for _, source := range sources {
		id, err := s.newID()
		if err != nil {
			return err
		}
		sourceAt := source.At
		if sourceAt.IsZero() {
			sourceAt = at
		}
		records = append(records, domain.MemorySource{
			ID:         id,
			MemoryID:   memoryID,
			SourceType: strings.TrimSpace(source.SourceType),
			SourceID:   source.SourceID,
			CreatedAt:  sourceAt,
		})
	}
	if _, err := s.repo.InsertMemorySources(ctx, records); err != nil {
		return internalError(err, "写入记忆来源失败")
	}
	return nil
}

// SupersedeMemories 把一批记忆标记为被新记忆取代。消费者：agent。
func (s *Service) SupersedeMemories(ctx context.Context, in contracts.SupersedeMemoryInput) error {
	if _, err := s.repo.SupersedeMemories(ctx, in.AIID, in.MemoryIDs, in.SupersededByID, in.At); err != nil {
		return internalError(err, "标记记忆失效失败")
	}
	return nil
}

// InvalidateMemoriesBySources 批量按来源失效记忆，返回受影响数量。消费者：agent。
func (s *Service) InvalidateMemoriesBySources(
	ctx context.Context,
	in contracts.InvalidateMemoriesBySourcesInput,
) (int, error) {
	if len(in.Sources) == 0 {
		return 0, nil
	}
	types := make([]string, 0, len(in.Sources))
	ids := make([]int64, 0, len(in.Sources))
	for _, source := range in.Sources {
		types = append(types, strings.TrimSpace(source.SourceType))
		ids = append(ids, source.SourceID)
	}
	affected, err := s.repo.InvalidateMemoriesBySources(ctx, in.AIID, types, ids, in.At)
	if err != nil {
		return 0, internalError(err, "按来源失效记忆失败")
	}
	return affected, nil
}

// memoriesToContracts 把领域记忆映射为跨模块视图。
func memoriesToContracts(memories []domain.Memory) []contracts.Memory {
	result := make([]contracts.Memory, 0, len(memories))
	for _, memory := range memories {
		result = append(result, contracts.Memory{
			ID:             memory.ID,
			AID:            memory.AID,
			MemoryType:     memory.MemoryType,
			Status:         memory.Status,
			Content:        memory.Content,
			Category:       memory.Category,
			Keywords:       memory.Keywords,
			Importance:     memory.Importance,
			SourceType:     memory.SourceType,
			SourceRefType:  memory.SourceRefType,
			SourceRefID:    memory.SourceRefID,
			InferenceLevel: memory.InferenceLevel,
			CreatedAt:      memory.CreatedAt,
			UpdatedAt:      memory.UpdatedAt,
			LastUsedAt:     memory.LastUsedAt,
		})
	}
	return result
}
