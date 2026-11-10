package http

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/great-magician-01/timeline/internal/modules/media/app"
	"github.com/great-magician-01/timeline/internal/shared/view"
	"github.com/great-magician-01/timeline/internal/transport/httpx"
)

// driveFileResponse 在附件安全 DTO 上补充网盘字段；不暴露 storage_key 或文章关联。
type driveFileResponse struct {
	view.AttachmentResponse
	FolderID   *string `json:"folder_id"`
	FolderName string  `json:"folder_name,omitempty"`
	Visibility string  `json:"visibility"`
	UpdatedAt  string  `json:"updated_at"`
}

func optionalID(id *int64) *string {
	if id == nil {
		return nil
	}
	value := httpx.FormatID(*id)
	return &value
}

func driveFileResponseFor(file app.DriveFile) driveFileResponse {
	attachment := view.AttachmentResponseFor(file.Attachment, file.Derivatives)
	attachment.CreatedAt = httpx.FormatTime(file.CreatedAt)
	return driveFileResponse{
		AttachmentResponse: attachment,
		FolderID:           optionalID(file.FolderID),
		FolderName:         file.FolderName,
		Visibility:         file.Visibility,
		UpdatedAt:          httpx.FormatTime(file.UpdatedAt),
	}
}

func driveFileResponses(items []app.DriveFile) []driveFileResponse {
	out := make([]driveFileResponse, 0, len(items))
	for _, item := range items {
		out = append(out, driveFileResponseFor(item))
	}
	return out
}

func driveFolderResponseFor(folder app.DriveFolder) gin.H {
	summary := "private"
	if folder.FileCount > 0 && folder.PublicCount == folder.FileCount {
		summary = "public"
	} else if folder.PublicCount > 0 {
		summary = "mixed"
	}
	return gin.H{
		"id":                 httpx.FormatID(folder.ID),
		"name":               folder.Name,
		"system_key":         folder.SystemKey,
		"file_count":         folder.FileCount,
		"public_count":       folder.PublicCount,
		"visibility_summary": summary,
		"created_at":         httpx.FormatTime(folder.CreatedAt),
		"updated_at":         httpx.FormatTime(folder.UpdatedAt),
	}
}

func parseDrivePathID(c *gin.Context, name, resource string) (int64, bool) {
	id, err := httpx.ParseID(c.Param(name))
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", resource+" ID 无效")
		return 0, false
	}
	return id, true
}

func parseDriveListQuery(c *gin.Context) (app.DriveFileQuery, bool) {
	var query app.DriveFileQuery
	cursor, err := httpx.PageCursor(c)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "无效的游标")
		return query, false
	}
	limit, err := httpx.PageLimit(c)
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", err.Error())
		return query, false
	}
	query.Cursor, query.Limit = cursor, limit
	query.Kind = strings.TrimSpace(c.Query("kind"))
	query.Keyword = strings.TrimSpace(c.Query("q"))
	if raw, present := c.GetQuery("folder_id"); present {
		raw = strings.TrimSpace(raw)
		if raw == "" || raw == "root" {
			query.RootOnly = true
		} else {
			id, err := httpx.ParseID(raw)
			if err != nil {
				httpx.Error(c, http.StatusBadRequest, "invalid_request", "folder_id 无效")
				return query, false
			}
			query.FolderID = &id
		}
	}
	return query, true
}

// ListDriveFolders 返回本人一级目录及文件统计。
func (h *Handler) ListDriveFolders(c *gin.Context) {
	viewer, ok := httpx.RequireViewer(c)
	if !ok {
		return
	}
	items, err := h.svc.ListDriveFolders(c.Request.Context(), viewer)
	if err != nil {
		h.respondError(c, err)
		return
	}
	responses := make([]gin.H, 0, len(items))
	for _, item := range items {
		responses = append(responses, driveFolderResponseFor(item))
	}
	c.JSON(http.StatusOK, gin.H{"items": responses})
}

type driveFolderBody struct {
	Name string `json:"name"`
}

// CreateDriveFolder 创建自定义目录。
func (h *Handler) CreateDriveFolder(c *gin.Context) {
	viewer, ok := httpx.RequireViewer(c)
	if !ok {
		return
	}
	var body driveFolderBody
	if err := httpx.DecodeJSON(c, &body); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "请求体必须是有效的 JSON 对象，且不能包含未知字段")
		return
	}
	folder, err := h.svc.CreateDriveFolder(c.Request.Context(), viewer, body.Name)
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"folder": driveFolderResponseFor(folder)})
}

// RenameDriveFolder 修改本人目录名称。
func (h *Handler) RenameDriveFolder(c *gin.Context) {
	viewer, ok := httpx.RequireViewer(c)
	if !ok {
		return
	}
	folderID, ok := parseDrivePathID(c, "id", "文件夹")
	if !ok {
		return
	}
	var body driveFolderBody
	if err := httpx.DecodeJSON(c, &body); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "请求体必须是有效的 JSON 对象，且不能包含未知字段")
		return
	}
	folder, err := h.svc.RenameDriveFolder(c.Request.Context(), viewer, folderID, body.Name)
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"folder": driveFolderResponseFor(folder)})
}

type driveVisibilityBody struct {
	Visibility string `json:"visibility"`
}

// SetDriveFolderVisibility 批量设置目录内文件公开级别。
func (h *Handler) SetDriveFolderVisibility(c *gin.Context) {
	viewer, ok := httpx.RequireViewer(c)
	if !ok {
		return
	}
	folderID, ok := parseDrivePathID(c, "id", "文件夹")
	if !ok {
		return
	}
	var body driveVisibilityBody
	if err := httpx.DecodeJSON(c, &body); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "请求体必须是有效的 JSON 对象，且不能包含未知字段")
		return
	}
	if err := h.svc.SetDriveFolderVisibility(c.Request.Context(), viewer, folderID, body.Visibility); err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"visibility": body.Visibility})
}

// DeleteDriveFolder 删除空的自定义目录。
func (h *Handler) DeleteDriveFolder(c *gin.Context) {
	viewer, ok := httpx.RequireViewer(c)
	if !ok {
		return
	}
	folderID, ok := parseDrivePathID(c, "id", "文件夹")
	if !ok {
		return
	}
	if err := h.svc.DeleteDriveFolder(c.Request.Context(), viewer, folderID); err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

// ListDriveFiles 返回本人文件及容量。
func (h *Handler) ListDriveFiles(c *gin.Context) {
	viewer, ok := httpx.RequireViewer(c)
	if !ok {
		return
	}
	query, ok := parseDriveListQuery(c)
	if !ok {
		return
	}
	page, err := h.svc.ListDriveFiles(c.Request.Context(), viewer, query)
	if err != nil {
		h.respondError(c, err)
		return
	}
	response := httpx.ListResponse(driveFileResponses(page.Items), page.NextID, page.HasMore)
	response["used_bytes"] = page.UsedBytes
	response["quota_bytes"] = page.QuotaBytes
	c.JSON(http.StatusOK, response)
}

// ListPublicDriveFiles 返回指定用户的公开文件与包含公开文件的目录摘要。
// 不返回容量、纯私密目录或文章关联。
func (h *Handler) ListPublicDriveFiles(c *gin.Context) {
	query, ok := parseDriveListQuery(c)
	if !ok {
		return
	}
	page, err := h.svc.ListPublicDriveFiles(c.Request.Context(), c.Param("username"), query)
	if err != nil {
		h.respondError(c, err)
		return
	}
	folders := make([]gin.H, 0, len(page.Folders))
	for _, folder := range page.Folders {
		folders = append(folders, gin.H{
			"id":         optionalID(folder.ID),
			"name":       folder.Name,
			"system_key": folder.SystemKey,
			"file_count": folder.FileCount,
		})
	}
	response := httpx.ListResponse(driveFileResponses(page.Items), page.NextID, page.HasMore)
	response["folders"] = folders
	c.JSON(http.StatusOK, response)
}

// SetDriveFileVisibility 修改单文件公开级别。
func (h *Handler) SetDriveFileVisibility(c *gin.Context) {
	viewer, ok := httpx.RequireViewer(c)
	if !ok {
		return
	}
	attachmentID, ok := parseDrivePathID(c, "id", "文件")
	if !ok {
		return
	}
	var body driveVisibilityBody
	if err := httpx.DecodeJSON(c, &body); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "请求体必须是有效的 JSON 对象，且不能包含未知字段")
		return
	}
	if err := h.svc.SetDriveFileVisibility(c.Request.Context(), viewer, attachmentID, body.Visibility); err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"visibility": body.Visibility})
}

type driveMoveBody struct {
	FolderID *string `json:"folder_id"`
}

// MoveDriveFile 移动文件到目录或根目录，不改变公开级别。
func (h *Handler) MoveDriveFile(c *gin.Context) {
	viewer, ok := httpx.RequireViewer(c)
	if !ok {
		return
	}
	attachmentID, ok := parseDrivePathID(c, "id", "文件")
	if !ok {
		return
	}
	var body driveMoveBody
	if err := httpx.DecodeJSON(c, &body); err != nil {
		httpx.Error(c, http.StatusBadRequest, "invalid_request", "请求体必须是有效的 JSON 对象，且不能包含未知字段")
		return
	}
	var folderID *int64
	if body.FolderID != nil && strings.TrimSpace(*body.FolderID) != "" {
		id, err := httpx.ParseID(*body.FolderID)
		if err != nil {
			httpx.Error(c, http.StatusBadRequest, "invalid_request", "folder_id 无效")
			return
		}
		folderID = &id
	}
	if err := h.svc.MoveDriveFile(c.Request.Context(), viewer, attachmentID, folderID); err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"folder_id": optionalID(folderID)})
}

// DeleteDriveFile 删除网盘条目，不解除任何文章引用。
func (h *Handler) DeleteDriveFile(c *gin.Context) {
	viewer, ok := httpx.RequireViewer(c)
	if !ok {
		return
	}
	attachmentID, ok := parseDrivePathID(c, "id", "文件")
	if !ok {
		return
	}
	if err := h.svc.DeleteDriveFile(c.Request.Context(), viewer, attachmentID); err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}
