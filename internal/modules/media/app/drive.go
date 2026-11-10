package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/great-magician-01/timeline/internal/modules/media/domain"
	"github.com/great-magician-01/timeline/internal/modules/media/repo"
	"github.com/great-magician-01/timeline/internal/platform/apperr"
	"github.com/great-magician-01/timeline/internal/shared/access"
	"github.com/great-magician-01/timeline/internal/shared/format"
	"github.com/great-magician-01/timeline/internal/shared/model"
	"github.com/great-magician-01/timeline/internal/shared/search"
)

var errContentPortMissing = errors.New("content port is nil")

// DriveFolder 是用户网盘文件夹及当前有效文件统计。
type DriveFolder struct {
	ID          int64
	OwnerID     int64
	Name        string
	SystemKey   *string
	FileCount   int
	PublicCount int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// DriveFile 是网盘条目、附件展示信息与衍生文件状态。
type DriveFile struct {
	Attachment  model.Attachment
	Derivatives map[string]string
	FolderID    *int64
	FolderName  string
	Visibility  string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// PublicDriveFolder 是公开网盘页的目录摘要；ID 为 nil 表示根目录。
type PublicDriveFolder struct {
	ID        *int64
	Name      string
	SystemKey *string
	FileCount int
}

// DriveFilePage 是 ID 游标分页结果；Usage 仅在本人列表中填充，
// Folders 仅在公开列表中填充。
type DriveFilePage struct {
	Items      []DriveFile
	NextID     int64
	HasMore    bool
	UsedBytes  int64
	QuotaBytes int64
	Folders    []PublicDriveFolder
}

// DriveFileQuery 是本人/公开文件列表共用的过滤条件。
type DriveFileQuery struct {
	FolderID *int64
	RootOnly bool
	Kind     string
	Keyword  string
	Cursor   int64
	Limit    int
}

func driveFolderFromRow(row repo.DriveFolderRow) DriveFolder {
	return DriveFolder{
		ID: row.ID, OwnerID: row.OwnerID, Name: row.Name, SystemKey: row.SystemKey,
		FileCount: row.FileCount, PublicCount: row.PublicCount,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func driveFileFromRow(row repo.DriveFileRow, derivatives map[string]string) DriveFile {
	return DriveFile{
		Attachment: row.Attachment.Attachment, Derivatives: derivatives,
		FolderID: row.FolderID, FolderName: row.FolderName, Visibility: row.Visibility,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func (s *Service) driveFolderForOwner(ctx context.Context, folderID, ownerID int64, forUpdate bool) (repo.DriveFolderRow, error) {
	var row repo.DriveFolderRow
	var err error
	if forUpdate {
		row, err = repo.GetDriveFolderForUpdate(ctx, s.exec(ctx), folderID)
	} else {
		row, err = repo.GetDriveFolder(ctx, s.exec(ctx), folderID)
	}
	if repo.IsNotFound(err) || (err == nil && (row.OwnerID != ownerID || row.Status != "active")) {
		return repo.DriveFolderRow{}, apperr.NotFound("文件夹不存在")
	}
	if err != nil {
		return repo.DriveFolderRow{}, s.internal("读取网盘文件夹失败", err)
	}
	return row, nil
}

// ensurePostFolder 返回当前用户的文章默认目录；单条幂等 SQL 兼容并发上传。
func (s *Service) ensurePostFolder(ctx context.Context, ownerID int64, at time.Time) (repo.DriveFolderRow, error) {
	// 系统目录使用属主雪花 ID 作为确定性 ID（与 0026 存量回填一致），自定义目录仍使用独立 ID。
	row, err := repo.EnsurePostFolder(ctx, s.exec(ctx), ownerID, ownerID, domain.PostFolderName, at)
	if err != nil {
		return repo.DriveFolderRow{}, s.internal("创建文章附件目录失败", err)
	}
	return row, nil
}

// resolveUploadFolder 文章上传固定进入系统目录；网盘直传可选目录或根目录。
func (s *Service) resolveUploadFolder(ctx context.Context, viewer model.Viewer, source string, folderID *int64, at time.Time) (*int64, error) {
	if source == domain.UploadSourcePost {
		folder, err := s.ensurePostFolder(ctx, viewer.ID, at)
		if err != nil {
			return nil, err
		}
		return &folder.ID, nil
	}
	if folderID == nil {
		return nil, nil
	}
	folder, err := s.driveFolderForOwner(ctx, *folderID, viewer.ID, false)
	if err != nil {
		return nil, err
	}
	return &folder.ID, nil
}

// ListDriveFolders 返回当前用户的全部有效一级目录。
func (s *Service) ListDriveFolders(ctx context.Context, viewer model.Viewer) ([]DriveFolder, error) {
	if err := access.RequireWritable(viewer); err != nil {
		return nil, err
	}
	rows, err := repo.ListDriveFolders(ctx, s.exec(ctx), viewer.ID)
	if err != nil {
		return nil, s.internal("读取网盘文件夹失败", err)
	}
	items := make([]DriveFolder, 0, len(rows))
	for _, row := range rows {
		items = append(items, driveFolderFromRow(row))
	}
	return items, nil
}

// CreateDriveFolder 创建自定义一级文件夹。
func (s *Service) CreateDriveFolder(ctx context.Context, viewer model.Viewer, name string) (DriveFolder, error) {
	if err := access.RequireWritable(viewer); err != nil {
		return DriveFolder{}, err
	}
	normalized, err := domain.NormalizeFolderName(name)
	if err != nil {
		return DriveFolder{}, err
	}
	var row repo.DriveFolderRow
	err = s.tx.WithTx(ctx, func(txCtx context.Context) error {
		at := s.clk.Now()
		id, err := s.ids.Next()
		if err != nil {
			return s.internal("生成网盘目录 ID 失败", err)
		}
		row = repo.DriveFolderRow{ID: id, OwnerID: viewer.ID, Name: normalized, CreatedAt: at, UpdatedAt: at}
		if err := repo.InsertDriveFolder(txCtx, s.exec(txCtx), row); err != nil {
			if repo.IsUniqueViolation(err) {
				return apperr.Conflict("folder_name_conflict", "文件夹名称已存在")
			}
			return s.internal("创建网盘文件夹失败", err)
		}
		return nil
	})
	return driveFolderFromRow(row), err
}

// RenameDriveFolder 修改本人目录名称；系统目录允许改名但 system_key 不变。
func (s *Service) RenameDriveFolder(ctx context.Context, viewer model.Viewer, folderID int64, name string) (DriveFolder, error) {
	if err := access.RequireWritable(viewer); err != nil {
		return DriveFolder{}, err
	}
	normalized, err := domain.NormalizeFolderName(name)
	if err != nil {
		return DriveFolder{}, err
	}
	var result DriveFolder
	err = s.tx.WithTx(ctx, func(txCtx context.Context) error {
		row, err := s.driveFolderForOwner(txCtx, folderID, viewer.ID, true)
		if err != nil {
			return err
		}
		at := s.clk.Now()
		if err := repo.RenameDriveFolder(txCtx, s.exec(txCtx), folderID, normalized, at); err != nil {
			if repo.IsUniqueViolation(err) {
				return apperr.Conflict("folder_name_conflict", "文件夹名称已存在")
			}
			return s.internal("重命名网盘文件夹失败", err)
		}
		row.Name, row.UpdatedAt = normalized, at
		result = driveFolderFromRow(row)
		return nil
	})
	return result, err
}

// DeleteDriveFolder 逻辑删除空的自定义目录；系统目录和非空目录拒绝删除。
func (s *Service) DeleteDriveFolder(ctx context.Context, viewer model.Viewer, folderID int64) error {
	if err := access.RequireWritable(viewer); err != nil {
		return err
	}
	return s.tx.WithTx(ctx, func(txCtx context.Context) error {
		row, err := s.driveFolderForOwner(txCtx, folderID, viewer.ID, true)
		if err != nil {
			return err
		}
		if row.SystemKey != nil {
			return apperr.Conflict("system_folder_required", "文章附件默认目录不能删除")
		}
		count, err := repo.CountActiveDriveFiles(txCtx, s.exec(txCtx), folderID)
		if err != nil {
			return s.internal("检查网盘文件夹内容失败", err)
		}
		if count > 0 {
			return apperr.Conflict("folder_not_empty", "请先移出或删除文件夹内的文件")
		}
		changed, err := repo.MarkDriveFolderDeleted(txCtx, s.exec(txCtx), folderID, s.clk.Now())
		if err != nil {
			return s.internal("删除网盘文件夹失败", err)
		}
		if !changed {
			return apperr.NotFound("文件夹不存在")
		}
		return nil
	})
}

func (s *Service) listDriveFilePage(ctx context.Context, filter repo.DriveFileFilter, includeUsage bool) (DriveFilePage, error) {
	if filter.Limit <= 0 {
		filter.Limit = 20
	}
	rows, err := repo.ListDriveFiles(ctx, s.exec(ctx), filter)
	if err != nil {
		return DriveFilePage{}, s.internal("读取网盘文件列表失败", err)
	}
	hasMore := len(rows) > filter.Limit
	if hasMore {
		rows = rows[:filter.Limit]
	}
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.Attachment.ID)
	}
	derivatives, err := repo.StatusesByAttachmentIDs(ctx, s.exec(ctx), ids)
	if err != nil {
		return DriveFilePage{}, s.internal("读取网盘文件衍生状态失败", err)
	}
	items := make([]DriveFile, 0, len(rows))
	for _, row := range rows {
		items = append(items, driveFileFromRow(row, derivatives[row.Attachment.ID]))
	}
	page := DriveFilePage{Items: items, HasMore: hasMore}
	if len(items) > 0 {
		page.NextID = items[len(items)-1].Attachment.ID
	}
	if includeUsage {
		used, err := repo.Usage(ctx, s.exec(ctx), filter.OwnerID)
		if err != nil {
			return DriveFilePage{}, s.internal("读取网盘容量失败", err)
		}
		page.UsedBytes = used
		page.QuotaBytes = s.settingsOrDefaults(ctx).StorageQuotaBytes
	}
	return page, nil
}

// ListDriveFiles 返回当前用户的文件；目录过滤会再次校验归属。
func (s *Service) ListDriveFiles(ctx context.Context, viewer model.Viewer, query DriveFileQuery) (DriveFilePage, error) {
	if err := access.RequireWritable(viewer); err != nil {
		return DriveFilePage{}, err
	}
	if !domain.ValidDriveKind(query.Kind) {
		return DriveFilePage{}, apperr.Invalid("kind 只能是 image、video 或 file")
	}
	if query.FolderID != nil {
		if _, err := s.driveFolderForOwner(ctx, *query.FolderID, viewer.ID, false); err != nil {
			return DriveFilePage{}, err
		}
	}
	keyword := search.EscapeLikePattern(strings.ToLower(strings.TrimSpace(query.Keyword)))
	return s.listDriveFilePage(ctx, repo.DriveFileFilter{
		OwnerID: viewer.ID, FolderID: query.FolderID, RootOnly: query.RootOnly,
		Kind: query.Kind, Keyword: keyword, Cursor: query.Cursor, Limit: query.Limit,
	}, true)
}

// ListPublicDriveFiles 返回指定用户的公开文件，不校验目录是否存在，避免泄露私密目录。
func (s *Service) ListPublicDriveFiles(ctx context.Context, username string, query DriveFileQuery) (DriveFilePage, error) {
	if !domain.ValidDriveKind(query.Kind) {
		return DriveFilePage{}, apperr.Invalid("kind 只能是 image、video 或 file")
	}
	owner, err := s.auth.GetUserByUsername(ctx, strings.TrimSpace(username))
	if err != nil {
		return DriveFilePage{}, s.wrap("读取公开网盘所属用户失败", err)
	}
	keyword := search.EscapeLikePattern(strings.ToLower(strings.TrimSpace(query.Keyword)))
	page, err := s.listDriveFilePage(ctx, repo.DriveFileFilter{
		OwnerID: owner.ID, FolderID: query.FolderID, RootOnly: query.RootOnly,
		Kind: query.Kind, Keyword: keyword, Cursor: query.Cursor, Limit: query.Limit,
		PublicOnly: true,
	}, false)
	if err != nil {
		return DriveFilePage{}, err
	}
	folders, err := repo.ListPublicDriveFolders(ctx, s.exec(ctx), owner.ID)
	if err != nil {
		return DriveFilePage{}, s.internal("读取公开网盘目录失败", err)
	}
	page.Folders = make([]PublicDriveFolder, 0, len(folders))
	for _, folder := range folders {
		page.Folders = append(page.Folders, PublicDriveFolder{
			ID: folder.ID, Name: folder.Name, SystemKey: folder.SystemKey, FileCount: folder.FileCount,
		})
	}
	return page, nil
}

// requireNoRestrictedReference 公开前复核文章/草稿引用；非公开或已删除引用均阻止直接公开。
func (s *Service) requireNoRestrictedReference(ctx context.Context, attachmentID int64) error {
	if s.content == nil {
		return s.internal("内容服务未初始化", errContentPortMissing)
	}
	restricted, err := s.content.HasNonPublicAttachmentReference(ctx, attachmentID)
	if err != nil {
		return s.wrap("检查网盘文件文章引用失败", err)
	}
	if restricted {
		return apperr.Conflict("drive_visibility_conflict", "文件被非公开或已删除文章引用，不能设为公开").
			WithDetails(driveConflictDetails([]int64{attachmentID}))
	}
	return nil
}

// SetDriveFileVisibility 修改单文件级别；公开与文章引用检查处于同一事务。
func (s *Service) SetDriveFileVisibility(ctx context.Context, viewer model.Viewer, attachmentID int64, visibility string) error {
	if err := access.RequireWritable(viewer); err != nil {
		return err
	}
	normalized, err := domain.NormalizeDriveVisibility(visibility)
	if err != nil {
		return err
	}
	return s.tx.WithTx(ctx, func(txCtx context.Context) error {
		row, err := repo.GetDriveFileForUpdate(txCtx, s.exec(txCtx), attachmentID)
		if repo.IsNotFound(err) || (err == nil && (row.OwnerID != viewer.ID || row.Status != "active")) {
			return apperr.NotFound("网盘文件不存在")
		}
		if err != nil {
			return s.internal("读取网盘文件失败", err)
		}
		if !row.Attachment.Available() || row.Attachment.Purpose != domain.PurposePost {
			return apperr.NotFound("网盘文件不存在或已失效")
		}
		if normalized == domain.DriveVisibilityPublic {
			if err := s.requireNoRestrictedReference(txCtx, attachmentID); err != nil {
				return err
			}
		}
		if err := repo.SetDriveFileVisibility(txCtx, s.exec(txCtx), attachmentID, normalized, s.clk.Now()); err != nil {
			return s.internal("修改网盘文件公开级别失败", err)
		}
		return nil
	})
}

// SetDriveFolderVisibility 全有或全无地批量修改目录内文件；移动目录不改变文件级别。
func (s *Service) SetDriveFolderVisibility(ctx context.Context, viewer model.Viewer, folderID int64, visibility string) error {
	if err := access.RequireWritable(viewer); err != nil {
		return err
	}
	normalized, err := domain.NormalizeDriveVisibility(visibility)
	if err != nil {
		return err
	}
	return s.tx.WithTx(ctx, func(txCtx context.Context) error {
		if _, err := s.driveFolderForOwner(txCtx, folderID, viewer.ID, true); err != nil {
			return err
		}
		ids, err := repo.ActiveDriveFileIDsByFolder(txCtx, s.exec(txCtx), folderID)
		if err != nil {
			return s.internal("读取文件夹内文件失败", err)
		}
		if normalized == domain.DriveVisibilityPublic {
			if _, err := repo.DriveFilesForUpdate(txCtx, s.exec(txCtx), ids); err != nil {
				return s.internal("锁定文件夹内文件失败", err)
			}
			conflicts := make([]int64, 0)
			for _, id := range ids {
				err := s.requireNoRestrictedReference(txCtx, id)
				if err == nil {
					continue
				}
				if apperr.IsKind(err, apperr.KindConflict) {
					conflicts = append(conflicts, id)
					continue
				}
				return err
			}
			if len(conflicts) > 0 {
				return apperr.Conflict("drive_visibility_conflict", "部分文件被非公开或已删除文章引用，不能设为公开").
					WithDetails(driveConflictDetails(conflicts))
			}
		}
		if err := repo.SetDriveFolderVisibility(txCtx, s.exec(txCtx), folderID, viewer.ID, normalized, s.clk.Now()); err != nil {
			return s.internal("批量修改文件夹公开级别失败", err)
		}
		return nil
	})
}

// MoveDriveFile 移动本人文件到目录或根目录，不修改公开级别。
func (s *Service) MoveDriveFile(ctx context.Context, viewer model.Viewer, attachmentID int64, folderID *int64) error {
	if err := access.RequireWritable(viewer); err != nil {
		return err
	}
	return s.tx.WithTx(ctx, func(txCtx context.Context) error {
		row, err := repo.GetDriveFile(txCtx, s.exec(txCtx), attachmentID)
		if repo.IsNotFound(err) || (err == nil && (row.OwnerID != viewer.ID || row.Status != "active" || !row.Attachment.Available())) {
			return apperr.NotFound("网盘文件不存在")
		}
		if err != nil {
			return s.internal("读取网盘文件失败", err)
		}
		if folderID != nil {
			if _, err := s.driveFolderForOwner(txCtx, *folderID, viewer.ID, false); err != nil {
				return err
			}
		}
		if err := repo.MoveDriveFile(txCtx, s.exec(txCtx), attachmentID, viewer.ID, folderID, s.clk.Now()); err != nil {
			return s.internal("移动网盘文件失败", err)
		}
		return nil
	})
}

// DeleteDriveFile 删除网盘条目但保留所有文章/草稿引用；无引用时才把附件逻辑失效。
func (s *Service) DeleteDriveFile(ctx context.Context, viewer model.Viewer, attachmentID int64) error {
	if err := access.RequireWritable(viewer); err != nil {
		return err
	}
	return s.tx.WithTx(ctx, func(txCtx context.Context) error {
		row, err := repo.GetDriveFileForUpdate(txCtx, s.exec(txCtx), attachmentID)
		if repo.IsNotFound(err) || (err == nil && row.OwnerID != viewer.ID) {
			return apperr.NotFound("网盘文件不存在")
		}
		if err != nil {
			return s.internal("读取网盘文件失败", err)
		}
		if row.Status == "deleted" {
			return nil
		}
		at := s.clk.Now()
		if _, err := repo.MarkDriveFileDeleted(txCtx, s.exec(txCtx), attachmentID, viewer.ID, at); err != nil {
			return s.internal("删除网盘文件失败", err)
		}
		if s.content == nil {
			return s.internal("内容服务未初始化", errContentPortMissing)
		}
		referenced, err := s.content.ReferencedAttachmentIDs(txCtx, []int64{attachmentID})
		if err != nil {
			return s.wrap("检查网盘文件内容引用失败", err)
		}
		if len(referenced) > 0 {
			return nil
		}
		deleted, err := repo.MarkDeleted(txCtx, s.exec(txCtx), attachmentID, at)
		if err != nil {
			return s.internal("失效无引用网盘附件失败", err)
		}
		if deleted {
			if err := repo.CancelIncompleteForAttachment(txCtx, s.exec(txCtx), attachmentID, at); err != nil {
				return s.internal("取消无引用网盘附件衍生任务失败", err)
			}
		}
		return nil
	})
}

// RevokePublicDriveFiles 供 content 在文章降级/删除的同一事务内撤销公开。
func (s *Service) RevokePublicDriveFiles(ctx context.Context, attachmentIDs []int64, at time.Time) error {
	if len(attachmentIDs) == 0 {
		return nil
	}
	if err := repo.RevokePublicDriveFiles(ctx, s.exec(ctx), attachmentIDs, at); err != nil {
		return s.internal("撤销文章附件的网盘公开状态失败", err)
	}
	return nil
}

// driveConflictDetails 供需要列出冲突文件的场景复用统一字符串 ID 格式。
func driveConflictDetails(ids []int64) map[string]any {
	values := make([]string, 0, len(ids))
	for _, id := range ids {
		values = append(values, format.FormatID(id))
	}
	return map[string]any{"attachment_ids": values}
}
