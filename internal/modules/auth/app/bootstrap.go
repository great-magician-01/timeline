package app

import (
	"context"

	"github.com/great-magician-01/timeline/internal/platform/apperr"
)

// BootstrapStatus 是初始化状态快照：人类用户数与超级管理员数。
type BootstrapStatus struct {
	HumanUsers  int64
	SuperAdmins int64
}

// BootstrapRequired 表示系统尚无人类用户（未初始化）：首个注册者将成为超级管理员。
func (s *Service) BootstrapRequired(ctx context.Context) (bool, error) {
	if s.users == nil {
		return false, apperr.Internal("auth 账号仓储未初始化")
	}
	humanUsers, err := s.users.CountHumanUsers(ctx)
	if err != nil {
		return false, internal(err, "读取初始化状态失败")
	}
	return humanUsers == 0, nil
}

// BootstrapStatus 返回人类用户数与超级管理员数，供启动日志判断初始化状态。
func (s *Service) BootstrapStatus(ctx context.Context) (BootstrapStatus, error) {
	if s.users == nil {
		return BootstrapStatus{}, apperr.Internal("auth 账号仓储未初始化")
	}
	humanUsers, err := s.users.CountHumanUsers(ctx)
	if err != nil {
		return BootstrapStatus{}, internal(err, "统计人类用户失败")
	}
	superAdmins, err := s.users.CountSuperAdmins(ctx)
	if err != nil {
		return BootstrapStatus{}, internal(err, "统计超级管理员失败")
	}
	return BootstrapStatus{HumanUsers: humanUsers, SuperAdmins: superAdmins}, nil
}
