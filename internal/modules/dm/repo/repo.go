// Package repo 是私信域唯一包含 SQL 的层：会话（dm_conversations）与消息（dm_messages）。
//
// 约定：
//   - 所有写库时间由 app 通过 now 参数传入，SQL 中不出现 now()/CURRENT_DATE/interval；
//   - 删除会话视图只置单侧 hidden_at，撤回只置 status='recalled'，不物理删除；
//   - repo 通过 db.ExecutorFrom 自动加入 ctx 中的事务；
//   - 未读统计一律带 status='published' 谓词，命中 recipient_id + read_at IS NULL 部分索引。
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
