package server_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/great-magician-01/timeline/internal/testkit"
)

// TestDriveIntegration 覆盖个人网盘（0026）：文章上传自动进入系统目录、网盘直传与
// 文件夹移动、公开级别与多文章复用、公开冲突、文章降级撤销公开、跨用户隔离、
// 网盘删除保留文章引用，以及按文章精确解除附件关联。
// 只在隔离测试 schema 中执行，默认跳过，需 TIMELINE_INTEGRATION=1。
func TestDriveIntegration(t *testing.T) {
	srv := testkit.NewIntegrationServer(t)
	suffix := testkit.IntegrationShortSuffix()

	alice := srv.Client(t)
	bob := srv.Client(t)
	guest := srv.Client(t)
	aliceName := "dr_alice_" + suffix
	alice.RegisterAndLogin(aliceName, "dr-alice-"+suffix+"@example.com")
	bob.RegisterAndLogin("dr_bob_"+suffix, "dr-bob-"+suffix+"@example.com")
	guest.RefreshCSRF()

	getItems := func(c *testkit.Client, path string) []any {
		t.Helper()
		status, _, payload, raw := c.Call(http.MethodGet, path, false, nil)
		testkit.ExpectStatus(t, status, http.StatusOK, raw)
		items, _ := payload["items"].([]any)
		return items
	}
	itemIDs := func(items []any) map[string]bool {
		ids := make(map[string]bool, len(items))
		for _, entry := range items {
			item, _ := entry.(map[string]any)
			if item == nil {
				continue
			}
			ids[testkit.MustString(t, item, "id")] = true
		}
		return ids
	}
	findSystemFolder := func(items []any) map[string]any {
		for _, entry := range items {
			folder, _ := entry.(map[string]any)
			if folder != nil && folder["system_key"] == "post" {
				return folder
			}
		}
		t.Fatalf("网盘文件夹中缺少文章附件系统目录: %v", items)
		return nil
	}
	postAttachmentIDs := func(payload map[string]any) map[string]bool {
		post := testkit.MustNested(t, payload, "post")
		attachments, _ := post["attachments"].([]any)
		ids := make(map[string]bool, len(attachments))
		for _, entry := range attachments {
			attachment, _ := entry.(map[string]any)
			if attachment == nil {
				continue
			}
			ids[testkit.MustString(t, attachment, "id")] = true
		}
		return ids
	}
	createPost := func(c *testkit.Client, visibility, action string, attachmentIDs ...string) (string, map[string]any) {
		t.Helper()
		attachmentValues := make([]any, 0, len(attachmentIDs))
		for _, id := range attachmentIDs {
			attachmentValues = append(attachmentValues, id)
		}
		status, _, payload, raw := c.Call(http.MethodPost, "/api/v1/posts", true, map[string]any{
			"content_format": "richtext",
			"content_source": fmt.Sprintf("<p>网盘集成测试 %s</p>", suffix),
			"visibility":     visibility,
			"attachment_ids": attachmentValues,
			"action":         action,
		})
		testkit.ExpectStatus(t, status, http.StatusCreated, raw)
		post := testkit.MustNested(t, payload, "post")
		return testkit.MustString(t, post, "id"), post
	}

	// ── 文章上传自动进入“文章附件”系统目录且默认私密 ──
	status, _, payload, raw := alice.UploadWithHeaders()
	testkit.ExpectStatus(t, status, http.StatusCreated, raw)
	attachment := testkit.MustNested(t, payload, "attachment")
	attachmentID := testkit.MustString(t, attachment, "id")
	contentURL := testkit.MustString(t, attachment, "content_url")
	driveFile := testkit.MustNested(t, payload, "drive_file")
	if testkit.MustString(t, driveFile, "visibility") != "private" {
		t.Fatalf("文章上传应默认私密: %s", raw)
	}
	folderID := testkit.MustString(t, driveFile, "folder_id")
	if folderID == "" {
		t.Fatalf("文章上传应关联默认目录: %s", raw)
	}

	// 头像不入网盘；文章附件出现在本人文件列表与系统目录统计中。
	avatar := alice.UploadWithPurpose("avatar")
	avatarID := testkit.MustString(t, avatar, "id")
	folders := getItems(alice, "/api/v1/drive/folders")
	systemFolder := findSystemFolder(folders)
	if testkit.MustString(t, systemFolder, "id") != folderID {
		t.Fatalf("上传响应 folder_id 与系统目录不一致: %v/%v", folderID, systemFolder)
	}
	if count, _ := systemFolder["file_count"].(float64); count != 1 {
		t.Fatalf("系统目录文件数 = %v，期望 1", systemFolder["file_count"])
	}
	aliceFiles := itemIDs(getItems(alice, "/api/v1/drive/files"))
	if !aliceFiles[attachmentID] {
		t.Fatalf("文章附件未出现在本人网盘: %v", aliceFiles)
	}
	if aliceFiles[avatarID] {
		t.Fatalf("头像附件不应进入网盘: %v", aliceFiles)
	}

	// ── 同一附件复用于多篇公开文章 ──
	postA, _ := createPost(alice, "public", "publish", attachmentID)
	postB, _ := createPost(alice, "public", "publish", attachmentID)
	if postA == postB {
		t.Fatal("测试帖子 ID 重复")
	}

	// 未设公开前，游客不能通过网盘读取（仍可通过公开文章读取）。
	if code, _, _ := guest.RawGET(contentURL); code != http.StatusOK {
		t.Fatalf("公开文章引用时游客读取附件状态 = %d，期望 200", code)
	}

	// ── 设为公开：公开网盘列表出现，目录摘要只包含有公开文件的目录 ──
	status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/drive/files/"+attachmentID, true,
		map[string]any{"visibility": "public"})
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	status, _, publicPage, raw := bob.Call(http.MethodGet, "/api/v1/users/"+aliceName+"/drive/files", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if !testkit.ListContains(publicPage, "id", attachmentID) {
		t.Fatalf("公开网盘应包含公开文件: %v", publicPage)
	}
	publicFolders, _ := publicPage["folders"].([]any)
	if len(publicFolders) == 0 {
		t.Fatalf("公开网盘响应缺少目录摘要: %v", publicPage)
	}
	firstFolder, _ := publicFolders[0].(map[string]any)
	if firstFolder == nil || testkit.MustString(t, firstFolder, "name") != "文章附件" {
		t.Fatalf("公开目录摘要应优先返回文章附件目录: %v", publicFolders)
	}
	if code, _, _ := guest.RawGET(contentURL); code != http.StatusOK {
		t.Fatalf("公开网盘文件游客读取状态 = %d，期望 200", code)
	}

	// ── 公开文件不能新加入非公开文章 ──
	status, _, conflictPayload, raw := alice.Call(http.MethodPost, "/api/v1/posts", true, map[string]any{
		"content_format": "richtext",
		"content_source": "<p>私密引用公开文件</p>",
		"visibility":     "private",
		"attachment_ids": []any{attachmentID},
		"action":         "draft",
	})
	testkit.ExpectStatus(t, status, http.StatusConflict, raw)
	if code := testkit.MustNested(t, conflictPayload, "error")["code"]; code != "drive_visibility_conflict" {
		t.Fatalf("冲突错误码 = %v，期望 drive_visibility_conflict: %s", code, raw)
	}

	// ── 文章降级：既有公开文件随文章撤销公开，不自动恢复 ──
	for _, postID := range []string{postA, postB} {
		status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/posts/"+postID, true, map[string]any{
			"content_format": "richtext",
			"content_source": "<p>降级为仅关注者</p>",
			"visibility":     "followers",
			"attachment_ids": []any{attachmentID},
		})
		testkit.ExpectStatus(t, status, http.StatusOK, raw)
	}
	filesAfterDowngrade := itemIDs(getItems(alice, "/api/v1/drive/files"))
	if !filesAfterDowngrade[attachmentID] {
		t.Fatal("降级后文件应从本人网盘可见")
	}
	status, _, publicAfterDowngrade, raw := bob.Call(http.MethodGet, "/api/v1/users/"+aliceName+"/drive/files", false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if testkit.ListContains(publicAfterDowngrade, "id", attachmentID) {
		t.Fatal("降级后不应继续出现在公开网盘")
	}
	if code, _, _ := guest.RawGET(contentURL); code != http.StatusNotFound {
		t.Fatalf("文章降级且撤销公开后游客读取状态 = %d，期望 404", code)
	}
	if code, _, _ := alice.RawGET(contentURL); code != http.StatusOK {
		t.Fatalf("上传者仍应能读取附件，状态 = %d", code)
	}

	// ── 文件夹批量公开逐文件检查引用：非公开文章引用时整批失败 ──
	status, _, folderConflict, raw := alice.Call(http.MethodPost, "/api/v1/drive/folders/"+folderID+"/visibility", true,
		map[string]any{"visibility": "public"})
	testkit.ExpectStatus(t, status, http.StatusConflict, raw)
	if code := testkit.MustNested(t, folderConflict, "error")["code"]; code != "drive_visibility_conflict" {
		t.Fatalf("文件夹公开冲突错误码 = %v: %s", code, raw)
	}
	status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/drive/folders/"+folderID+"/visibility", true,
		map[string]any{"visibility": "private"})
	testkit.ExpectStatus(t, status, http.StatusOK, raw)

	// ── 跨用户隔离：他人不能修改公开级别或看到私密文件 ──
	status, _, _, raw = bob.Call(http.MethodPost, "/api/v1/drive/files/"+attachmentID, true,
		map[string]any{"visibility": "public"})
	testkit.ExpectStatus(t, status, http.StatusNotFound, raw)
	if itemIDs(getItems(bob, "/api/v1/drive/files"))[attachmentID] {
		t.Fatal("其他用户不应看到他人的网盘文件")
	}

	// ── 网盘删除只移除索引，文章与历史版本引用仍可读取 ──
	status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/drive/files/"+attachmentID+"/delete", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if itemIDs(getItems(alice, "/api/v1/drive/files"))[attachmentID] {
		t.Fatal("删除后文件不应继续出现在网盘")
	}
	status, _, postPayload, raw := alice.Call(http.MethodGet, "/api/v1/posts/"+postA, false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if !postAttachmentIDs(postPayload)[attachmentID] {
		t.Fatalf("网盘删除不应解除文章引用: %v", postPayload)
	}
	if code, _, _ := alice.RawGET(contentURL); code != http.StatusOK {
		t.Fatalf("网盘删除后仍有文章引用，上传者读取状态 = %d", code)
	}

	// ── 网盘直传、目录创建/重命名/移动与系统目录保护 ──
	status, _, folderPayload, raw := alice.Call(http.MethodPost, "/api/v1/drive/folders", true,
		map[string]any{"name": "资料"})
	testkit.ExpectStatus(t, status, http.StatusCreated, raw)
	customFolderID := testkit.MustString(t, testkit.MustNested(t, folderPayload, "folder"), "id")

	status, drivePayload, raw := alice.UploadDriveFile("drive-file.png", customFolderID)
	testkit.ExpectStatus(t, status, http.StatusCreated, raw)
	driveAttachmentID := testkit.MustString(t, testkit.MustNested(t, drivePayload, "drive_file"), "id")
	driveFilePayload := testkit.MustNested(t, drivePayload, "drive_file")
	if testkit.MustString(t, driveFilePayload, "folder_id") != customFolderID {
		t.Fatalf("网盘直传未进入指定目录: %s", raw)
	}
	status, _, renamePayload, raw := alice.Call(http.MethodPost, "/api/v1/drive/folders/"+customFolderID, true,
		map[string]any{"name": "资料改名"})
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if testkit.MustString(t, testkit.MustNested(t, renamePayload, "folder"), "name") != "资料改名" {
		t.Fatalf("文件夹重命名失败: %s", raw)
	}
	status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/drive/files/"+driveAttachmentID+"/move", true,
		map[string]any{"folder_id": nil})
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/drive/folders/"+customFolderID+"/delete", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/drive/folders/"+folderID+"/delete", true, nil)
	testkit.ExpectStatus(t, status, http.StatusConflict, raw)

	// ── 多草稿复用同一附件时，解除仅作用于指定文章 ──
	status, detachUpload, raw := alice.UploadDriveFile("detach.png", "")
	testkit.ExpectStatus(t, status, http.StatusCreated, raw)
	detachFileID := testkit.MustString(t, testkit.MustNested(t, detachUpload, "drive_file"), "id")
	draftA, _ := createPost(alice, "private", "draft", detachFileID)
	draftB, _ := createPost(alice, "private", "draft", detachFileID)
	status, _, _, raw = alice.Call(http.MethodPost, "/api/v1/posts/"+draftA+"/attachments/"+detachFileID+"/detach", true, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	status, _, draftAPayload, raw := alice.Call(http.MethodGet, "/api/v1/posts/"+draftA, false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if postAttachmentIDs(draftAPayload)[detachFileID] {
		t.Fatal("解除后 draftA 不应再关联该附件")
	}
	status, _, draftBPayload, raw := alice.Call(http.MethodGet, "/api/v1/posts/"+draftB, false, nil)
	testkit.ExpectStatus(t, status, http.StatusOK, raw)
	if !postAttachmentIDs(draftBPayload)[detachFileID] {
		t.Fatal("解除 draftA 不应影响 draftB 的同一附件关联")
	}
}
