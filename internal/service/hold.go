package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/Abeautiful-crypto/Pet-store/internal/model"
	"github.com/Abeautiful-crypto/Pet-store/internal/store"
)

// ErrNotOwner 表示操作者不是预约所属用户（FR-02 数据隔离）。
var ErrNotOwner = errors.New("无权操作他人预约")

// ExpireHolds 扫描 5 分钟未付款占位并关闭、释放名额（FR-04 / 规则 2）。
// 正确性以存储层 pay_deadline_at 为准；重复扫描因状态已是终态而幂等不重复释放。
func (s *Service) ExpireHolds(ctx context.Context) (int, error) {
	appointments, err := s.store.ListAppointments(ctx)
	if err != nil {
		return 0, err
	}
	now := s.now()
	expired := 0
	for _, appt := range appointments {
		if appt.Status != model.StatusPendingPay || now.Before(appt.PayDeadlineAt) {
			continue
		}
		if err := s.closeAndRelease(ctx, appt, model.EventHoldTimeout); err != nil {
			// 已被并发取消/关闭：CAS 冲突属于幂等命中，不算失败、不再释放名额。
			if isConcurrentClose(err) {
				continue
			}
			return expired, err
		}
		expired++
	}
	return expired, nil
}

// CancelUnpaid 用户主动取消未付款占位，释放名额（FR-04 / 规则 2）。
func (s *Service) CancelUnpaid(ctx context.Context, appointmentID, userID string) error {
	appt, err := s.store.GetAppointment(ctx, appointmentID)
	if err != nil {
		return err
	}
	if appt.UserID != userID {
		return ErrNotOwner
	}
	if appt.Status != model.StatusPendingPay {
		return fmt.Errorf("%w: 仅待支付占位可主动取消，当前 %s", model.ErrIllegalTransition, appt.Status)
	}
	return s.closeAndRelease(ctx, appt, model.EventCancelByUser)
}

// closeAndRelease 是"状态迁移 + 名额释放"的唯一组合写入点。
// 迁移用 version CAS，释放带 slot_released 一次性守卫，杜绝重复释放（规则 4）。
func (s *Service) closeAndRelease(ctx context.Context, appt model.Appointment, event model.Event) error {
	if _, _, err := s.store.Transition(ctx, appt.ID, appt.Status, event, appt.Version); err != nil {
		return err
	}
	if err := s.store.ReleaseSlot(ctx, appt.ID); err != nil && !errors.Is(err, store.ErrAlreadyReleased) {
		return err
	}
	return nil
}

func isConcurrentClose(err error) bool {
	return errors.Is(err, store.ErrVersionConflict) ||
		errors.Is(err, store.ErrAlreadyReleased) ||
		errors.Is(err, model.ErrTerminalStatus) ||
		errors.Is(err, model.ErrIllegalTransition)
}
