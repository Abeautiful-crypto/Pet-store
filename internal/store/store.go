// Package store 是所有共享状态的唯一访问点，名额与预约状态的真相源。
// 约束锚点：.agent/backend/constraints.md §4、tech-stack.md §1；规则 1/6 与 SRS 5.4。
package store

import (
	"context"
	"errors"

	"github.com/Abeautiful-crypto/Pet-store/internal/model"
)

var (
	// ErrSlotFull 表示名额不足（原子条件更新影响行数为 0）。
	ErrSlotFull = errors.New("名额不足")
	// ErrSlotNotFound 表示时段不存在。
	ErrSlotNotFound = errors.New("时段不存在")
	// ErrAppointmentNotFound 表示预约主记录不存在。
	ErrAppointmentNotFound = errors.New("预约不存在")
	// ErrDuplicateActive 表示同一宠物同一时段已有有效预约（规则 1）。
	ErrDuplicateActive = errors.New("同一宠物同一时段已有有效预约")
	// ErrVersionConflict 表示乐观锁 CAS 失败，并发操作只有一个胜者（规则 6）。
	ErrVersionConflict = errors.New("并发冲突：version 不匹配")
	// ErrAlreadyReleased 表示名额已释放，重复释放被拒绝（规则 4）。
	ErrAlreadyReleased = errors.New("名额已释放")
)

// SlotCacheInvalidator 是存储层写提交后的名额读缓存失效钩子。
// Cache-Aside 写失效：先更新真相源（事务提交成功），再失效变化的 slot:{id}（tech-stack.md §2.2）。
type SlotCacheInvalidator interface {
	InvalidateSlot(ctx context.Context, slotID string) error
	InvalidateAll(ctx context.Context) error
}

// StateStore 定义共享状态的唯一访问接口。
// 所有角色读写同一条 appointment 记录；状态变更只有 Transition 一个写入点。
type StateStore interface {
	CreateSlot(ctx context.Context, slot model.Slot) error
	GetSlot(ctx context.Context, slotID string) (model.Slot, error)

	// CreateAppointment 在单个事务内完成：原子占名额 + 写预约主记录 + 写事件流水。
	// 任一失败整体回滚，名额与预约记录都不留半成品（规则 1 / SRS 8 章）。
	CreateAppointment(ctx context.Context, appt *model.Appointment) error
	GetAppointment(ctx context.Context, appointmentID string) (model.Appointment, error)
	HasActiveAppointment(ctx context.Context, petID, slotID string) (bool, error)

	// Transition 以 from + version 做 CAS，返回目标状态与新版本号。
	Transition(ctx context.Context, appointmentID string, from model.Status, event model.Event, version int64) (model.Status, int64, error)

	// ReleaseSlot 释放名额，带一次性守卫，重复调用返回 ErrAlreadyReleased。
	ReleaseSlot(ctx context.Context, appointmentID string) error

	AppendEvent(ctx context.Context, log model.EventLog) error
	ListEvents(ctx context.Context) ([]model.EventLog, error)
	Reset(ctx context.Context) error
}
