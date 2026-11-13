package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Abeautiful-crypto/Pet-store/internal/model"
	"github.com/Abeautiful-crypto/Pet-store/internal/store"
)

var (
	ErrWrongStore            = errors.New("非本店员工不得操作")
	ErrPendingCancel         = errors.New("存在待处理取消申请，禁止核销")
	ErrAlreadyVerified       = errors.New("已有核销记录，不得标记未到店")
	ErrSlotNotEnded          = errors.New("时段尚未结束，不得标记未到店")
	ErrServiceRecordNotFound = errors.New("服务履约记录不存在")
)

// logConflict 把并发/越权被拒的冲突写入只增不改的事件日志（SRS 13 章 A8）。
func (s *Service) logConflict(ctx context.Context, appointmentID, action string) {
	_ = s.store.AppendEvent(ctx, model.EventLog{
		ID:            s.newID("EVT"),
		AppointmentID: appointmentID,
		Action:        "concurrent_conflict",
		Detail:        action + " CAS 冲突，输方已拒绝并留痕",
		CreatedAt:     s.now(),
	})
}

// Verify 前台核销三条件准入：本店 + 已确认 + 无待处理取消申请（FR-11 / 规则 5）。
// 任一不满足即拒绝且不改动任何记录（FR-19 / SC-04）。
func (s *Service) Verify(ctx context.Context, appointmentID, operatorStoreID, operator string) error {
	appt, err := s.store.GetAppointment(ctx, appointmentID)
	if err != nil {
		return err
	}
	if operatorStoreID != model.StoreID || appt.StoreID != operatorStoreID {
		s.logConflict(ctx, appointmentID, "wrong_store_verify")
		return ErrWrongStore
	}
	if appt.Status == model.StatusCancelReview {
		s.logConflict(ctx, appointmentID, "pending_cancel_verify")
		return ErrPendingCancel
	}
	if req, ok := s.ledger.CancelRequestOf(appointmentID); ok && req.Status == model.CancelRequestPending {
		s.logConflict(ctx, appointmentID, "pending_cancel_verify")
		return ErrPendingCancel
	}
	if appt.Status != model.StatusConfirmed {
		s.logConflict(ctx, appointmentID, "verify")
		return fmt.Errorf("%w: 核销需已确认，当前 %s", model.ErrIllegalTransition, appt.Status)
	}

	if _, _, err := s.store.Transition(ctx, appointmentID, model.StatusConfirmed,
		model.EventVerify, appt.Version); err != nil {
		if errors.Is(err, store.ErrVersionConflict) {
			s.logConflict(ctx, appointmentID, "verify")
		}
		return err
	}
	now := s.now()
	s.ledger.SaveServiceRecord(model.ServiceRecord{
		ID:            s.newID("SR"),
		AppointmentID: appointmentID,
		VerifyBy:      operator,
		VerifyAt:      &now,
	})
	return nil
}

// StartService 登记服务开始（FR-12 / 规则 5），进度只写一次。
func (s *Service) StartService(ctx context.Context, appointmentID, operatorStoreID string) error {
	if operatorStoreID != model.StoreID {
		return ErrWrongStore
	}
	appt, err := s.store.GetAppointment(ctx, appointmentID)
	if err != nil {
		return err
	}
	if appt.Status != model.StatusVerified {
		return fmt.Errorf("%w: 登记开始需已核销，当前 %s", model.ErrIllegalTransition, appt.Status)
	}
	if _, _, err := s.store.Transition(ctx, appointmentID, model.StatusVerified,
		model.EventStartService, appt.Version); err != nil {
		return err
	}
	record, ok := s.ledger.ServiceRecordOf(appointmentID)
	if !ok {
		return ErrServiceRecordNotFound
	}
	now := s.now()
	record.StartedAt = &now
	s.ledger.SaveServiceRecord(record)
	return nil
}

// FinishService 登记服务完成并记收入一次（FR-12 / 规则 8）；重复完成被终态门禁拒绝。
func (s *Service) FinishService(ctx context.Context, appointmentID, operatorStoreID string) error {
	if operatorStoreID != model.StoreID {
		return ErrWrongStore
	}
	appt, err := s.store.GetAppointment(ctx, appointmentID)
	if err != nil {
		return err
	}
	if appt.Status != model.StatusInService {
		return fmt.Errorf("%w: 登记完成需服务中，当前 %s", model.ErrIllegalTransition, appt.Status)
	}
	if _, _, err := s.store.Transition(ctx, appointmentID, model.StatusInService,
		model.EventFinishService, appt.Version); err != nil {
		return err
	}
	record, ok := s.ledger.ServiceRecordOf(appointmentID)
	if !ok {
		return ErrServiceRecordNotFound
	}
	now := s.now()
	record.FinishedAt = &now
	record.RevenueFen = model.ServiceRevenueFen
	s.ledger.SaveServiceRecord(record)
	return nil
}

// MarkNoShow 时段结束后由店长标记未到店并写明原因（FR-15 / 规则 7）。
// 本题约定不自动退款、不实现争议裁决；已有核销记录或时段未结束均拒绝。
func (s *Service) MarkNoShow(ctx context.Context, appointmentID, reason string) error {
	appt, err := s.store.GetAppointment(ctx, appointmentID)
	if err != nil {
		return err
	}
	slot, err := s.store.GetSlot(ctx, appt.SlotID)
	if err != nil {
		return err
	}
	if s.now().Before(slot.EndAt) {
		return ErrSlotNotEnded
	}
	if record, ok := s.ledger.ServiceRecordOf(appointmentID); ok && record.VerifyAt != nil {
		return ErrAlreadyVerified
	}
	if strings.TrimSpace(reason) == "" {
		return ErrReasonRequired
	}
	if appt.Status != model.StatusConfirmed {
		return fmt.Errorf("%w: 仅已确认且未核销预约可标记未到店，当前 %s",
			model.ErrIllegalTransition, appt.Status)
	}
	if _, _, err := s.store.Transition(ctx, appointmentID, model.StatusConfirmed,
		model.EventMarkNoShow, appt.Version); err != nil {
		return err
	}
	if err := s.store.ReleaseSlot(ctx, appointmentID); err != nil && !errors.Is(err, store.ErrAlreadyReleased) {
		return err
	}
	s.ledger.SaveServiceRecord(model.ServiceRecord{
		ID:            s.newID("SR"),
		AppointmentID: appointmentID,
		NoShowReason:  reason,
	})
	return nil
}
