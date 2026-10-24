// Package repo 是群聊域唯一包含 SQL 的层：群（group_conversations）、
// 成员（group_members）与消息（group_messages）。
//
// 约定（与 dm repo 一致）：
//   - 所有写库时间由 app 通过 now 参数传入，SQL 中不出现 now()/CURRENT_DATE/interval；
//   - 解散只置 status='dissolved'，退出/移除只置 left_at，撤回只置 status='recalled'，
//     不物理删除；
//   - repo 通过 db.ExecutorFrom 自动加入 ctx 中的事务；
//   - 未读统计基于成员级游标 last_read_message_id，一律带 status='published' 谓词。
//
// users 由 auth 模块拥有，本包不写它的 SQL。
package repo

import (
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
)

// notFound 把 pgx.ErrNoRows 映射为领域 not_found，避免存储层错误泄漏到 app。
func notFound(err error, message string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.NotFound(message)
	}
	return err
}

// rowScanner 抽象 pgx.Row 与 pgx.Rows 的 Scan，便于复用同一列投影。
type rowScanner interface {
	Scan(dest ...any) error
}
