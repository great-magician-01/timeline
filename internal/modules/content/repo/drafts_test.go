package repo

import (
	"context"
	"strings"
	"testing"
)

// TestActiveDraftAttachmentIDsSQL 锁定 media 清理流程依赖的查询语义：
// 只统计生效草稿关联（is_active）、按附件 ID 集合查询并返回去重结果。
func TestActiveDraftAttachmentIDsSQL(t *testing.T) {
	for _, want := range []string{
		"post_draft_attachments",
		"is_active",
		"attachment_id = ANY($1)",
		"SELECT DISTINCT attachment_id",
		"ORDER BY attachment_id",
	} {
		if !strings.Contains(sqlActiveDraftAttachmentIDs, want) {
			t.Errorf("查询缺少片段 %q:\n%s", want, sqlActiveDraftAttachmentIDs)
		}
	}
	if strings.Contains(sqlActiveDraftAttachmentIDs, "DELETE") {
		t.Fatalf("查询不得包含物理删除:\n%s", sqlActiveDraftAttachmentIDs)
	}
}

// TestActiveDraftAttachmentIDsEmptyInput 校验空入参直接返回空结果、不访问数据库。
func TestActiveDraftAttachmentIDsEmptyInput(t *testing.T) {
	r := New(nil)
	ids, err := r.ActiveDraftAttachmentIDs(context.Background(), nil)
	if err != nil || len(ids) != 0 {
		t.Fatalf("空入参应返回空结果: %v, %v", ids, err)
	}
}
