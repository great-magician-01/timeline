package domain

import (
	"strings"
	"unicode/utf8"
)

// 记忆类型、状态、分类与推断级别（与 ai_memories 的 CHECK 约束一致）。
const (
	MemoryTypeRecent   = "recent"
	MemoryTypeLongTerm = "long_term"

	MemoryStatusActive      = "active"
	MemoryStatusSuperseded  = "superseded"
	MemoryStatusInvalidated = "invalidated"
	MemoryStatusDeleted     = "deleted"

	MemorySourceManual        = "manual"
	MemorySourceAction        = "action"
	MemorySourceConsolidation = "consolidation"

	MemorySourceTypePost    = "post"
	MemorySourceTypeComment = "comment"

	MemoryInferenceFact      = "fact"
	MemoryInferenceSummary   = "summary"
	MemoryInferenceInference = "inference"

	MemoryCategoryFact         = "fact"
	MemoryCategoryPreference   = "preference"
	MemoryCategoryRelationship = "relationship"
	MemoryCategoryEvent        = "event"
	MemoryCategoryTopic        = "topic"
)

// 记忆字段约束。
const (
	MaxMemoryContentRunes = 1000
	MaxMemoryKeywords     = 20
	MaxMemoryKeywordRunes = 32
	// DefaultMemoryLimit 与最大记忆检索条数。
	DefaultMemoryLimit = 20
	MaxMemoryLimit     = 50
)

// ValidMemoryTypes 是合法记忆类型集合。
var ValidMemoryTypes = map[string]bool{
	MemoryTypeRecent:   true,
	MemoryTypeLongTerm: true,
}

// ValidMemoryStatuses 是合法记忆状态集合。
var ValidMemoryStatuses = map[string]bool{
	MemoryStatusActive:      true,
	MemoryStatusSuperseded:  true,
	MemoryStatusInvalidated: true,
	MemoryStatusDeleted:     true,
}

// ValidMemoryCategories 是合法记忆分类集合。
var ValidMemoryCategories = map[string]bool{
	MemoryCategoryFact:         true,
	MemoryCategoryPreference:   true,
	MemoryCategoryRelationship: true,
	MemoryCategoryEvent:        true,
	MemoryCategoryTopic:        true,
}

// ValidInferenceLevels 是合法推断级别集合。
var ValidInferenceLevels = map[string]bool{
	MemoryInferenceFact:      true,
	MemoryInferenceSummary:   true,
	MemoryInferenceInference: true,
}

// NormalizeMemoryLimit 归一化记忆检索条数（默认 20，最大 50）。
func NormalizeMemoryLimit(limit int) int {
	if limit <= 0 {
		return DefaultMemoryLimit
	}
	if limit > MaxMemoryLimit {
		return MaxMemoryLimit
	}
	return limit
}

// NormalizeKeywordTerms 去空、去重（保持出现顺序）后的关键词列表。
func NormalizeKeywordTerms(keywords []string) []string {
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

// NormalizeCategories 去空、去重（保持出现顺序）后的分类列表。
func NormalizeCategories(categories []string) []string {
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

// EscapeLikePattern 转义 ILIKE 模式中的通配符，避免关键词被当成模式。
func EscapeLikePattern(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(value)
}

// MemoryWrite 是记忆正文写入的归一化结果。
type MemoryWrite struct {
	Content        string
	Category       string
	Keywords       []string
	Importance     int
	InferenceLevel string
}

// NormalizeMemoryWrite 校验并归一化记忆正文、分类、关键词、重要性与推断级别。
// 返回归一化结果与字段级错误详情，空 map 表示通过。
func NormalizeMemoryWrite(
	content, category string,
	keywords []string,
	importance *int,
	inferenceLevel string,
) (MemoryWrite, map[string]string) {
	details := map[string]string{}
	trimmedContent := strings.TrimSpace(content)
	if runes := utf8.RuneCountInString(trimmedContent); runes < 1 || runes > MaxMemoryContentRunes {
		details["content"] = "记忆正文需为 1-1000 个字符"
	}
	finalCategory := strings.TrimSpace(category)
	if finalCategory == "" {
		finalCategory = MemoryCategoryEvent
	} else if !ValidMemoryCategories[finalCategory] {
		details["category"] = "分类只能是 fact、preference、relationship、event 或 topic"
	}
	normalizedKeywords := make([]string, 0, len(keywords))
	seen := map[string]bool{}
	for _, keyword := range keywords {
		trimmed := strings.TrimSpace(keyword)
		if trimmed == "" {
			continue
		}
		if utf8.RuneCountInString(trimmed) > MaxMemoryKeywordRunes {
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
	if len(normalizedKeywords) > MaxMemoryKeywords {
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
		finalInference = MemoryInferenceFact
	} else if !ValidInferenceLevels[finalInference] {
		details["inference_level"] = "推断级别只能是 fact、summary 或 inference"
	}
	return MemoryWrite{
		Content:        trimmedContent,
		Category:       finalCategory,
		Keywords:       normalizedKeywords,
		Importance:     finalImportance,
		InferenceLevel: finalInference,
	}, details
}
