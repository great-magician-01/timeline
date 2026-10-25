package repo

import (
	"strings"
	"testing"

	authrepo "github.com/great-magician-01/timeline/internal/modules/auth/repo"
)

// TestUserColumnsMirrorAuth 守护管理端镜像的 users 投影与 auth 权威定义一致。
// 模块间非测试代码不得互相 import（架构守卫的模块互斥规则），镜像清单无法直接
// 复用权威定义，列集或顺序漂移会导致 model.User 的按位 Scan 静默错位
// （banner_url 曾实际漂移过一次），因此在测试期比对两份清单：
// auth 调整 users 投影时，本测试失败提醒同步镜像。
func TestUserColumnsMirrorAuth(t *testing.T) {
	ours := strings.Join(strings.Fields(userColumns), " ")
	theirs := strings.Join(strings.Fields(authrepo.UserColumns), " ")
	if ours != theirs {
		t.Fatalf("管理端 users 投影与 auth 权威定义不一致：\nadmin:  %s\nauth:   %s", userColumns, authrepo.UserColumns)
	}
}
