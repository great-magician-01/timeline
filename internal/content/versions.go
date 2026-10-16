package content

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/great-magician-01/timeline/internal/access"
	"github.com/great-magician-01/timeline/internal/api"
	"github.com/great-magician-01/timeline/internal/model"
)

// VersionItem 是历史版本列表项。
type VersionItem struct {
	ID                    string  `json:"id"`
	VersionNumber         int     `json:"version_number"`
	Visibility            string  `json:"visibility"`
	CreatedAt             string  `json:"created_at"`
	RestoredFromVersionID *string `json:"restored_from_version_id,omitempty"`
	Summary               string  `json:"summary"`
}

// VersionDetail 是历史版本详情，包含该版本的正文与附件快照。
type VersionDetail struct {
	ID                    string                     `json:"id"`
	VersionNumber         int                        `json:"version_number"`
	ContentFormat         string                     `json:"content_format"`
	ContentSource         string                     `json:"content_source"`
	ContentHTML           string                     `json:"content_html"`
	Visibility            string                     `json:"visibility"`
	CreatedAt             string                     `json:"created_at"`
	CreatedBy             string                     `json:"created_by"`
	RestoredFromVersionID *string                    `json:"restored_from_version_id,omitempty"`
	Attachments           []model.AttachmentResponse `json:"attachments"`
}

// ListVersions 返回历史版本列表；历史版本继承当前帖子的访问权限，不单独授权。
func (s *Service) ListVersions(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	postID, ok := pathID(c)
	if !ok {
		return
	}
	viewer := viewerOf(c)
	limit, err := api.PageLimit(c)
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	cursor, err := api.TrimPageCursor(c)
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", "无效的游标")
		return
	}
	ctx := c.Request.Context()

	post, err := loadPost(ctx, pool, postID)
	if errors.Is(err, pgx.ErrNoRows) {
		api.Respond(c, api.ErrNotFound("内容不存在或无权访问"))
		return
	}
	if err != nil {
		s.internalError(c, "读取帖子失败", err)
		return
	}
	if err := access.RequireVisiblePost(ctx, pool, viewer, post); err != nil {
		s.respond(c, "校验帖子可见性失败", err)
		return
	}

	// 版本号倒序等价于 id 倒序（雪花 ID 单调递增），游标只需携带版本 ID。
	sql := `SELECT id, version_number, visibility, created_at, restored_from_version_id, plain_text
		FROM post_versions WHERE post_id = $1`
	args := []any{postID}
	if cursor > 0 {
		args = append(args, cursor)
		sql += fmt.Sprintf(" AND id < $%d", len(args))
	}
	args = append(args, limit+1)
	sql += fmt.Sprintf(" ORDER BY id DESC LIMIT $%d", len(args))

	rows, err := pool.Query(ctx, sql, args...)
	if err != nil {
		s.internalError(c, "读取历史版本列表失败", err)
		return
	}
	defer rows.Close()

	items := make([]VersionItem, 0, limit+1)
	ids := make([]int64, 0, limit+1)
	for rows.Next() {
		var id int64
		var versionNumber int
		var visibility string
		var createdAt time.Time
		var restoredFrom *int64
		var plainText string
		if err := rows.Scan(&id, &versionNumber, &visibility, &createdAt, &restoredFrom, &plainText); err != nil {
			s.internalError(c, "读取历史版本列表失败", err)
			return
		}
		item := VersionItem{
			ID:            api.FormatID(id),
			VersionNumber: versionNumber,
			Visibility:    visibility,
			CreatedAt:     api.FormatTime(createdAt),
			Summary:       summary(plainText),
		}
		if restoredFrom != nil {
			source := api.FormatID(*restoredFrom)
			item.RestoredFromVersionID = &source
		}
		items = append(items, item)
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		s.internalError(c, "读取历史版本列表失败", err)
		return
	}

	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	var lastID int64
	if len(items) > 0 {
		lastID = ids[len(items)-1]
	}
	c.JSON(http.StatusOK, api.ListResponse(items, lastID, hasMore))
}

// GetVersion 返回指定历史版本的完整快照。
func (s *Service) GetVersion(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	postID, ok := pathID(c)
	if !ok {
		return
	}
	versionID, err := api.ParseID(c.Param("version_id"))
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", "资源 ID 无效")
		return
	}
	viewer := viewerOf(c)
	ctx := c.Request.Context()

	post, err := loadPost(ctx, pool, postID)
	if errors.Is(err, pgx.ErrNoRows) {
		api.Respond(c, api.ErrNotFound("内容不存在或无权访问"))
		return
	}
	if err != nil {
		s.internalError(c, "读取帖子失败", err)
		return
	}
	if err := access.RequireVisiblePost(ctx, pool, viewer, post); err != nil {
		s.respond(c, "校验帖子可见性失败", err)
		return
	}

	version, err := model.ScanPostVersion(pool.QueryRow(ctx,
		`SELECT `+model.PostVersionColumns+` FROM post_versions WHERE id = $1 AND post_id = $2`,
		versionID, postID))
	if errors.Is(err, pgx.ErrNoRows) {
		api.Respond(c, api.ErrNotFound("历史版本不存在"))
		return
	}
	if err != nil {
		s.internalError(c, "读取历史版本失败", err)
		return
	}
	attachments, err := versionAttachments(ctx, pool, version.ID)
	if err != nil {
		s.internalError(c, "读取历史版本附件失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"version": versionDetail(version, attachments)})
}

// RestoreVersion 以历史快照生成一个新的当前版本，不覆盖也不删除既有历史。
func (s *Service) RestoreVersion(c *gin.Context) {
	pool, ok := s.pool(c)
	if !ok {
		return
	}
	viewer := viewerOf(c)
	if err := access.RequireWritable(viewer); err != nil {
		api.Respond(c, err)
		return
	}
	postID, ok := pathID(c)
	if !ok {
		return
	}
	versionID, err := api.ParseID(c.Param("version_id"))
	if err != nil {
		api.Error(c, http.StatusBadRequest, "invalid_request", "资源 ID 无效")
		return
	}
	if !s.consumePostQuota(c, viewer.ID) {
		return
	}
	ctx := c.Request.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		s.internalError(c, "开启恢复版本事务失败", err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	post, err := loadPostForUpdate(ctx, tx, postID)
	if errors.Is(err, pgx.ErrNoRows) {
		api.Respond(c, api.ErrNotFound("内容不存在或无权访问"))
		return
	}
	if err != nil {
		s.internalError(c, "读取帖子失败", err)
		return
	}
	if post.Status == model.PostStatusDeleted {
		api.Respond(c, api.ErrNotFound("内容不存在或无权访问"))
		return
	}
	if !post.SelfPosted(viewer) {
		api.Respond(c, api.ErrForbidden("只有作者可以恢复历史版本"))
		return
	}
	if post.Status != model.PostStatusPublished {
		api.Respond(c, api.ErrForbidden("只有已发布的内容可以恢复历史版本"))
		return
	}

	version, err := model.ScanPostVersion(tx.QueryRow(ctx,
		`SELECT `+model.PostVersionColumns+` FROM post_versions WHERE id = $1 AND post_id = $2`,
		versionID, postID))
	if errors.Is(err, pgx.ErrNoRows) {
		api.Respond(c, api.ErrNotFound("历史版本不存在"))
		return
	}
	if err != nil {
		s.internalError(c, "读取历史版本失败", err)
		return
	}
	if err := ensureVisibilityAllowed(ctx, tx, post.AuthorID, version.Visibility); err != nil {
		s.respond(c, "校验恢复可见性失败", err)
		return
	}

	var maxVersion int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(version_number), 0) FROM post_versions WHERE post_id = $1`,
		postID).Scan(&maxVersion); err != nil {
		s.internalError(c, "读取当前版本号失败", err)
		return
	}
	attachments, err := availableVersionAttachmentIDs(ctx, tx, version.ID)
	if err != nil {
		s.internalError(c, "读取历史版本附件失败", err)
		return
	}
	content := normalizedPost{
		format:        version.ContentFormat,
		source:        version.ContentSource,
		html:          version.ContentHTML,
		plain:         version.PlainText,
		visibility:    version.Visibility,
		attachmentIDs: attachments,
	}
	sourceVersionID := version.ID
	newVersionID, err := s.insertVersion(ctx, tx, postID, post.AuthorID, maxVersion+1, content, &sourceVersionID)
	if err != nil {
		s.internalError(c, "写入恢复版本失败", err)
		return
	}
	if err := linkAttachments(ctx, tx, postID, attachments); err != nil {
		s.internalError(c, "关联恢复版本附件失败", err)
		return
	}
	// 可见性跟随来源版本快照；历史访问权限立即跟随新的当前帖子。
	if _, err := tx.Exec(ctx, `
		UPDATE posts SET current_version_id = $2, visibility = $3, updated_at = now()
		WHERE id = $1`, postID, newVersionID, version.Visibility); err != nil {
		s.internalError(c, "更新当前版本失败", err)
		return
	}
	updated, err := loadPost(ctx, tx, postID)
	if err != nil {
		s.internalError(c, "读取恢复后帖子失败", err)
		return
	}
	if err := s.rebuildDerived(ctx, tx, updated, version.PlainText); err != nil {
		s.respond(c, "重建标签与搜索失败", err)
		return
	}
	detail, err := s.loadDetail(ctx, tx, viewer, updated)
	if err != nil {
		s.respond(c, "组装帖子详情失败", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		s.internalError(c, "提交恢复版本事务失败", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"post": detail})
}

// versionAttachments 返回版本快照引用的全部附件（含已逻辑失效的附件，供前端显示占位）。
func versionAttachments(ctx context.Context, q access.Querier, versionID int64) ([]model.AttachmentResponse, error) {
	byVersion, err := loadVersionAttachments(ctx, q, []int64{versionID}, false)
	if err != nil {
		return nil, err
	}
	list := byVersion[versionID]
	ids := make([]int64, 0, len(list))
	for _, attachment := range list {
		ids = append(ids, attachment.ID)
	}
	derivatives, err := loadDerivatives(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	responses := make([]model.AttachmentResponse, 0, len(list))
	for _, attachment := range list {
		responses = append(responses, model.AttachmentResponseFor(attachment, derivatives[attachment.ID]))
	}
	return responses, nil
}

// availableVersionAttachmentIDs 返回版本快照中仍然有效的附件 ID（按原排序）。
func availableVersionAttachmentIDs(ctx context.Context, q access.Querier, versionID int64) ([]int64, error) {
	byVersion, err := loadVersionAttachments(ctx, q, []int64{versionID}, true)
	if err != nil {
		return nil, err
	}
	list := byVersion[versionID]
	ids := make([]int64, 0, len(list))
	for _, attachment := range list {
		ids = append(ids, attachment.ID)
	}
	return ids, nil
}

// versionDetail 组装历史版本详情 DTO。
func versionDetail(version model.PostVersion, attachments []model.AttachmentResponse) VersionDetail {
	detail := VersionDetail{
		ID:            api.FormatID(version.ID),
		VersionNumber: version.VersionNumber,
		ContentFormat: version.ContentFormat,
		ContentSource: version.ContentSource,
		ContentHTML:   version.ContentHTML,
		Visibility:    version.Visibility,
		CreatedAt:     api.FormatTime(version.CreatedAt),
		CreatedBy:     api.FormatID(version.CreatedBy),
		Attachments:   attachments,
	}
	if detail.Attachments == nil {
		detail.Attachments = []model.AttachmentResponse{}
	}
	if version.RestoredFromVersionID != nil {
		source := api.FormatID(*version.RestoredFromVersionID)
		detail.RestoredFromVersionID = &source
	}
	return detail
}
