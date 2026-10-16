package ai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/great-magician-01/callable"
	"github.com/jackc/pgx/v5"

	"github.com/great-magician-01/timeline/internal/access"
	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/audit"
	"github.com/great-magician-01/timeline/internal/auth"
	"github.com/great-magician-01/timeline/internal/model"
)

// providerListItem 是预设列表项；只暴露密钥掩码，绝不返回明文或密文。
type providerListItem struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	BaseURL      string `json:"base_url"`
	DefaultModel string `json:"default_model"`
	IsEnabled    bool   `json:"is_enabled"`
	APIKeyMasked string `json:"api_key_masked"`
	AICount      int    `json:"ai_count"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

const providerColumns = `id, name, base_url, api_key_cipher, default_model, is_enabled, created_at, updated_at`

// providerRecord 是 model_providers 的行结构；cipher 字段只在本包内部流转。
type providerRecord struct {
	ID           int64
	Name         string
	BaseURL      string
	APIKeyCipher string
	DefaultModel string
	IsEnabled    bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func scanProvider(row model.Row) (providerRecord, error) {
	var p providerRecord
	err := row.Scan(&p.ID, &p.Name, &p.BaseURL, &p.APIKeyCipher, &p.DefaultModel,
		&p.IsEnabled, &p.CreatedAt, &p.UpdatedAt)
	return p, err
}

// loadProvider 读取未删除的预设；不存在时返回 404 not_found。
func loadProvider(ctx context.Context, q access.Querier, providerID int64) (providerRecord, error) {
	record, err := scanProvider(q.QueryRow(ctx,
		`SELECT `+providerColumns+` FROM model_providers WHERE id = $1 AND NOT is_deleted`, providerID))
	if errors.Is(err, pgx.ErrNoRows) {
		return providerRecord{}, api.ErrNotFound("模型接口预设不存在")
	}
	if err != nil {
		return providerRecord{}, err
	}
	return record, nil
}

// enabledProviderExists 在事务内校验引用完整性（无外键，只能应用层校验）。
func enabledProviderExists(ctx context.Context, q access.Querier, providerID int64) (bool, error) {
	var exists bool
	err := q.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM model_providers
			WHERE id = $1 AND NOT is_deleted AND is_enabled
		)`, providerID).Scan(&exists)
	return exists, err
}

// maskProviderKey 只返回掩码；密文损坏或密钥不匹配时降级为 "****" 并告警，
// 绝不让密文或明文进入响应。
func (s *Service) maskProviderKey(record providerRecord) string {
	if record.APIKeyCipher == "" {
		// 未配置密钥（例如本地无鉴权 mock 服务）。
		return ""
	}
	plain, err := DecryptSecret(s.secretKey, record.APIKeyCipher)
	if err != nil {
		s.log.Warn("解密模型接口预设密钥失败", "provider_id", record.ID, "err", err)
		return "****"
	}
	return MaskSecret(plain)
}

func (s *Service) providerItem(record providerRecord, aiCount int) providerListItem {
	return providerListItem{
		ID:           api.FormatID(record.ID),
		Name:         record.Name,
		BaseURL:      record.BaseURL,
		DefaultModel: record.DefaultModel,
		IsEnabled:    record.IsEnabled,
		APIKeyMasked: s.maskProviderKey(record),
		AICount:      aiCount,
		CreatedAt:    api.FormatTime(record.CreatedAt),
		UpdatedAt:    api.FormatTime(record.UpdatedAt),
	}
}

// providerAICounts 批量统计每个预设关联的 AI 数量，避免列表接口 N+1。
func providerAICounts(ctx context.Context, q access.Querier, ids []int64) (map[int64]int, error) {
	counts := make(map[int64]int, len(ids))
	if len(ids) == 0 {
		return counts, nil
	}
	rows, err := q.Query(ctx, `
		SELECT ms.provider_id, COUNT(*)
		FROM ai_model_settings ms
		JOIN users u ON u.id = ms.ai_id AND u.is_ai
		WHERE ms.provider_id = ANY($1)
		GROUP BY ms.provider_id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var providerID int64
		var count int
		if err := rows.Scan(&providerID, &count); err != nil {
			return nil, err
		}
		counts[providerID] = count
	}
	return counts, rows.Err()
}

// providerNameTaken 检查预设名称（忽略大小写）是否已被其他未删除预设占用。
func providerNameTaken(ctx context.Context, q access.Querier, name string, excludeID int64) (bool, error) {
	var exists bool
	err := q.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM model_providers
			WHERE LOWER(name) = LOWER($1) AND NOT is_deleted AND id <> $2
		)`, name, excludeID).Scan(&exists)
	return exists, err
}

// validateProviderFields 校验预设的公共字段（不含密钥策略），返回字段级详情。
func validateProviderFields(name, baseURL, defaultModel string) map[string]string {
	details := map[string]string{}
	if runes := utf8.RuneCountInString(name); runes < 1 || runes > 100 {
		details["name"] = "名称需为 1-100 个字符"
	}
	if err := validateProviderBaseURL(baseURL); err != nil {
		details["base_url"] = err.Error()
	}
	if runes := utf8.RuneCountInString(defaultModel); runes < 1 || runes > 200 {
		details["default_model"] = "默认模型不能为空且不超过 200 个字符"
	}
	return details
}

func validateProviderBaseURL(baseURL string) error {
	if baseURL == "" {
		return fmt.Errorf("接口地址不能为空")
	}
	if utf8.RuneCountInString(baseURL) > 500 {
		return fmt.Errorf("接口地址不能超过 500 个字符")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("接口地址必须是 http 或 https 开头的完整地址")
	}
	return nil
}

// ListProviders 返回预设列表：支持名称模糊搜索与启停状态过滤，游标倒序分页。
func (s *Service) ListProviders(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
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

	where := []string{"NOT is_deleted"}
	args := make([]any, 0, 4)
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
		where = append(where, fmt.Sprintf("STRPOS(LOWER(name), LOWER(%s)) > 0", placeholder))
	}
	if status := strings.TrimSpace(c.Query("status")); status != "" {
		switch status {
		case "enabled":
			where = append(where, "is_enabled")
		case "disabled":
			where = append(where, "NOT is_enabled")
		default:
			api.Error(c, http.StatusBadRequest, "invalid_request", "status 只能是 enabled 或 disabled")
			return
		}
	}
	if cursor > 0 {
		where = append(where, "id < "+addArg(cursor))
	}
	args = append(args, limit+1)
	limitPlaceholder := "$" + strconv.Itoa(len(args))

	rows, err := pool.Query(c.Request.Context(), `
		SELECT `+providerColumns+` FROM model_providers
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY id DESC LIMIT `+limitPlaceholder, args...)
	if err != nil {
		s.internalError(c, "查询模型接口预设列表失败", err)
		return
	}
	defer rows.Close()

	records := make([]providerRecord, 0, limit+1)
	for rows.Next() {
		record, scanErr := scanProvider(rows)
		if scanErr != nil {
			s.internalError(c, "读取模型接口预设列表失败", scanErr)
			return
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		s.internalError(c, "遍历模型接口预设列表失败", err)
		return
	}
	hasMore := len(records) > limit
	if hasMore {
		records = records[:limit]
	}
	ids := make([]int64, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.ID)
	}
	counts, err := providerAICounts(c.Request.Context(), pool, ids)
	if err != nil {
		s.internalError(c, "统计预设关联 AI 数量失败", err)
		return
	}
	items := make([]providerListItem, 0, len(records))
	for _, record := range records {
		items = append(items, s.providerItem(record, counts[record.ID]))
	}
	var lastID int64
	if len(records) > 0 {
		lastID = records[len(records)-1].ID
	}
	c.JSON(http.StatusOK, api.ListResponse(items, lastID, hasMore))
}

// CreateProvider 创建模型接口预设；密钥加密存储，审计与写入同事务。
func (s *Service) CreateProvider(c *gin.Context) {
	var in struct {
		Name         string `json:"name"`
		BaseURL      string `json:"base_url"`
		APIKey       string `json:"api_key"`
		DefaultModel string `json:"default_model"`
	}
	if err := api.DecodeJSON(c, &in); err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", "请求体必须是有效的 JSON 对象，且不能包含未知字段")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	in.BaseURL = strings.TrimSpace(in.BaseURL)
	in.DefaultModel = strings.TrimSpace(in.DefaultModel)
	in.APIKey = strings.TrimSpace(in.APIKey)
	if details := validateProviderFields(in.Name, in.BaseURL, in.DefaultModel); len(details) > 0 {
		api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", firstDetail(details), details)
		return
	}
	cipher, err := EncryptSecret(s.secretKey, in.APIKey)
	if err != nil {
		s.internalError(c, "加密模型接口预设密钥失败", err)
		return
	}
	adminUser, _ := auth.CurrentUser(c)
	ctx := c.Request.Context()

	tx, ok := s.beginTx(c, "开启模型接口预设事务失败")
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	taken, err := providerNameTaken(ctx, tx, in.Name, 0)
	if err != nil {
		s.internalError(c, "检查预设名称冲突失败", err)
		return
	}
	if taken {
		api.ErrorWithDetails(c, http.StatusConflict, "duplicate", "同名模型接口预设已存在", gin.H{"name": "名称已被占用"})
		return
	}
	id, err := s.ids.Next()
	if err != nil {
		s.internalError(c, "生成模型接口预设 ID 失败", err)
		return
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO model_providers (id, name, base_url, api_key_cipher, default_model, is_enabled)
		VALUES ($1, $2, $3, $4, $5, TRUE)`,
		id, in.Name, in.BaseURL, cipher, in.DefaultModel,
	); err != nil {
		if isUniqueViolation(err) {
			api.ErrorWithDetails(c, http.StatusConflict, "duplicate", "同名模型接口预设已存在", gin.H{"name": "名称已被占用"})
			return
		}
		s.internalError(c, "创建模型接口预设失败", err)
		return
	}
	// 审计明细只记录名称等非敏感字段。
	if err = s.writeAudit(ctx, tx, adminUser.ID, audit.ActionProviderCreate, targetModelProvider, &id,
		gin.H{"name": in.Name}); err != nil {
		s.internalError(c, "写入模型接口预设审计失败", err)
		return
	}
	record, err := loadProvider(ctx, tx, id)
	if err != nil {
		s.internalError(c, "读取新建模型接口预设失败", err)
		return
	}
	if !s.commitTx(c, tx, "提交模型接口预设事务失败") {
		return
	}
	c.JSON(http.StatusCreated, gin.H{"provider": s.providerItem(record, 0)})
}

// UpdateProvider 更新预设；api_key 缺省或为空表示不变更密钥。
func (s *Service) UpdateProvider(c *gin.Context) {
	providerID, ok := parsePathID(c, "id", "无效的模型接口预设 ID")
	if !ok {
		return
	}
	var in struct {
		Name         *string `json:"name"`
		BaseURL      *string `json:"base_url"`
		APIKey       *string `json:"api_key"`
		DefaultModel *string `json:"default_model"`
		IsEnabled    *bool   `json:"is_enabled"`
	}
	if err := api.DecodeJSON(c, &in); err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", "请求体必须是有效的 JSON 对象，且不能包含未知字段")
		return
	}
	adminUser, _ := auth.CurrentUser(c)
	ctx := c.Request.Context()

	tx, ok := s.beginTx(c, "开启模型接口预设事务失败")
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	current, err := loadProvider(ctx, tx, providerID)
	if err != nil {
		if !api.Respond(c, err) {
			s.internalError(c, "读取待修改模型接口预设失败", err)
		}
		return
	}
	name := current.Name
	if in.Name != nil {
		name = strings.TrimSpace(*in.Name)
	}
	baseURL := current.BaseURL
	if in.BaseURL != nil {
		baseURL = strings.TrimSpace(*in.BaseURL)
	}
	defaultModel := current.DefaultModel
	if in.DefaultModel != nil {
		defaultModel = strings.TrimSpace(*in.DefaultModel)
	}
	if details := validateProviderFields(name, baseURL, defaultModel); len(details) > 0 {
		api.ErrorWithDetails(c, http.StatusBadRequest, "validation_failed", firstDetail(details), details)
		return
	}
	if name != current.Name {
		taken, err := providerNameTaken(ctx, tx, name, providerID)
		if err != nil {
			s.internalError(c, "检查预设名称冲突失败", err)
			return
		}
		if taken {
			api.ErrorWithDetails(c, http.StatusConflict, "duplicate", "同名模型接口预设已存在", gin.H{"name": "名称已被占用"})
			return
		}
	}
	cipher := current.APIKeyCipher
	keyChanged := false
	if in.APIKey != nil && strings.TrimSpace(*in.APIKey) != "" {
		cipher, err = EncryptSecret(s.secretKey, strings.TrimSpace(*in.APIKey))
		if err != nil {
			s.internalError(c, "加密模型接口预设密钥失败", err)
			return
		}
		keyChanged = true
	}
	isEnabled := current.IsEnabled
	if in.IsEnabled != nil {
		isEnabled = *in.IsEnabled
	}
	if _, err = tx.Exec(ctx, `
		UPDATE model_providers
		SET name = $2, base_url = $3, api_key_cipher = $4, default_model = $5,
		    is_enabled = $6, updated_at = now()
		WHERE id = $1`, providerID, name, baseURL, cipher, defaultModel, isEnabled,
	); err != nil {
		if isUniqueViolation(err) {
			api.ErrorWithDetails(c, http.StatusConflict, "duplicate", "同名模型接口预设已存在", gin.H{"name": "名称已被占用"})
			return
		}
		s.internalError(c, "更新模型接口预设失败", err)
		return
	}
	if err = s.writeAudit(ctx, tx, adminUser.ID, audit.ActionProviderUpdate, targetModelProvider, &providerID,
		gin.H{"name": name, "api_key_changed": keyChanged, "is_enabled": isEnabled}); err != nil {
		s.internalError(c, "写入模型接口预设审计失败", err)
		return
	}
	record, err := loadProvider(ctx, tx, providerID)
	if err != nil {
		s.internalError(c, "读取更新后的模型接口预设失败", err)
		return
	}
	counts, err := providerAICounts(ctx, tx, []int64{providerID})
	if err != nil {
		s.internalError(c, "统计预设关联 AI 数量失败", err)
		return
	}
	if !s.commitTx(c, tx, "提交模型接口预设事务失败") {
		return
	}
	c.JSON(http.StatusOK, gin.H{"provider": s.providerItem(record, counts[providerID])})
}

// DeleteProvider 逻辑删除预设；仍有关联 AI 时返回 409 duplicate。
func (s *Service) DeleteProvider(c *gin.Context) {
	providerID, ok := parsePathID(c, "id", "无效的模型接口预设 ID")
	if !ok {
		return
	}
	adminUser, _ := auth.CurrentUser(c)
	ctx := c.Request.Context()

	tx, ok := s.beginTx(c, "开启模型接口预设事务失败")
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := loadProvider(ctx, tx, providerID); err != nil {
		if !api.Respond(c, err) {
			s.internalError(c, "读取待删除模型接口预设失败", err)
		}
		return
	}
	var aiCount int
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM ai_model_settings ms
		JOIN users u ON u.id = ms.ai_id AND u.is_ai
		WHERE ms.provider_id = $1`, providerID).Scan(&aiCount); err != nil {
		s.internalError(c, "统计预设关联 AI 数量失败", err)
		return
	}
	if aiCount > 0 {
		api.ErrorWithDetails(c, http.StatusConflict, "duplicate",
			fmt.Sprintf("仍有 %d 个 AI 关联该预设，请先解除关联或停用对应 AI", aiCount),
			gin.H{"ai_count": aiCount})
		return
	}
	if _, err := tx.Exec(ctx, `
		UPDATE model_providers SET is_deleted = TRUE, is_enabled = FALSE, updated_at = now()
		WHERE id = $1`, providerID); err != nil {
		s.internalError(c, "删除模型接口预设失败", err)
		return
	}
	if err := s.writeAudit(ctx, tx, adminUser.ID, audit.ActionProviderDelete, targetModelProvider, &providerID,
		gin.H{}); err != nil {
		s.internalError(c, "写入模型接口预设审计失败", err)
		return
	}
	if !s.commitTx(c, tx, "提交模型接口预设事务失败") {
		return
	}
	c.Status(http.StatusNoContent)
}

// TestProvider 用 ListModels 做连接测试；无论成功失败 HTTP 都为 200，
// 错误信息经脱敏，绝不包含 API Key。
func (s *Service) TestProvider(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	providerID, ok := parsePathID(c, "id", "无效的模型接口预设 ID")
	if !ok {
		return
	}
	record, err := loadProvider(c.Request.Context(), pool, providerID)
	if err != nil {
		if !api.Respond(c, err) {
			s.internalError(c, "读取待测试模型接口预设失败", err)
		}
		return
	}
	apiKey, err := DecryptSecret(s.secretKey, record.APIKeyCipher)
	if err != nil {
		s.log.Warn("解密模型接口预设密钥失败", "provider_id", record.ID, "err", err)
		c.JSON(http.StatusOK, gin.H{
			"ok":         false,
			"latency_ms": 0,
			"error":      "API Key 解密失败，请在预设中重新保存密钥",
		})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), providerTestTimeout)
	defer cancel()
	client := s.NewClient(ProviderConfig{
		ID:           record.ID,
		Name:         record.Name,
		BaseURL:      record.BaseURL,
		APIKey:       apiKey,
		DefaultModel: record.DefaultModel,
		IsEnabled:    record.IsEnabled,
	}, GenerationParams{}, providerTestTimeout)

	started := time.Now()
	models, err := client.ListModels(ctx)
	latency := time.Since(started).Milliseconds()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"ok":         false,
			"latency_ms": latency,
			"error":      providerTestErrorMessage(err, apiKey),
		})
		return
	}
	const maxModels = 200
	names := make([]string, 0, len(models))
	for _, info := range models {
		if info.ID == "" {
			continue
		}
		names = append(names, info.ID)
		if len(names) >= maxModels {
			break
		}
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "latency_ms": latency, "models": names})
}

// providerTestErrorMessage 把底层错误映射为不含密钥的提示。
func providerTestErrorMessage(err error, apiKey string) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "连接超时（超过 15 秒）"
	}
	// 先脱敏再截断，避免密钥正好跨越截断边界而漏出。
	raw := err.Error()
	if apiKey != "" {
		raw = strings.ReplaceAll(raw, apiKey, "***")
	}
	message := truncateRunes(raw, 300)
	var apiErr *callable.APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.StatusCode == 401 || apiErr.StatusCode == 403:
			return "连接失败：认证被拒绝，请检查 API Key"
		case apiErr.StatusCode == 404:
			return "连接失败：接口地址不正确（404）"
		case apiErr.StatusCode == 429:
			return "连接失败：请求被上游限流（429）"
		case apiErr.StatusCode >= 500:
			return fmt.Sprintf("连接失败：上游服务错误（%d）", apiErr.StatusCode)
		case apiErr.StatusCode == 0:
			return "连接失败：无法访问接口地址（" + message + "）"
		default:
			return fmt.Sprintf("连接失败：HTTP %d", apiErr.StatusCode)
		}
	}
	return "连接失败：" + message
}
