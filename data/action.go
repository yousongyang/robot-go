package atsf4g_go_robot_user

import (
	base "github.com/atframework/robot-go/base"
)

type TaskActionUser struct {
	base.TaskActionBase
	User User
	Fn   func(*TaskActionUser) error
}

type TaskActionUserNoneLock struct {
	base.TaskActionBase
	User User
	Fn   func(*TaskActionUserNoneLock) error
}

func init() {
	var _ base.TaskActionImpl = &TaskActionUser{}
	var _ base.TaskActionImpl = &TaskActionUserNoneLock{}
}

func (t *TaskActionUser) IsTakenActionGuard() bool {
	return t.User.IsTakenActionGuard(t)
}

func (t *TaskActionUser) BeforeYield() error {
	return t.User.ReleaseActionGuard(t)
}

func (t *TaskActionUser) AfterYield() error {
	return t.User.TakeActionGuard(t)
}

func (t *TaskActionUser) HookRun() error {
	err := t.User.TakeActionGuard(t)
	if err != nil {
		return err
	}
	err = t.Fn(t)
	if err != nil {
		t.User.ReleaseActionGuard(t)
		return err
	}
	return t.User.ReleaseActionGuard(t)
}

func (t *TaskActionUser) Log(format string, a ...any) {
	t.User.Log(format, a...)
}

func (t *TaskActionUserNoneLock) HookRun() error {
	return t.Fn(t)
}

func (t *TaskActionUserNoneLock) Log(format string, a ...any) {
	t.User.Log(format, a...)
}
