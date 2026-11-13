package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Abeautiful-crypto/Pet-store/internal/model"
	"github.com/Abeautiful-crypto/Pet-store/internal/store"
)

var (
	// ErrReasonRequired 表示临近取消未提交原因（FR-08）。
	ErrReasonRequired = errors.New("临近取消必须提交原因")
	// ErrCancelRequestNotFound 表示审核的取消申请不存在。
	ErrCancelRequestNotFound = errors.New("取消申请不存在")
	// ErrCancelAlreadyReviewed 表示重复审核，避免名额二次释放（规则 4）。
	ErrCancelAlreadyReviewed = errors.New("取消申请已处理")
)

// Cancel 发起取消：距 start_at >= 2h 自动受理全额退款；<2h 须填原因转客服审核。
// 分界以服务端时钟计算，等于 2h 走自动分支（SRS 13 章 A2 / FR-07、FR-08 / 规则 3）。
func (s *Service) Cancel(ctx context.Context, appointmentID, userID, reason string) (*model.Refund, error) {
	appt, err := s.store.GetAppointment(ctx, appointmentID)
	if err != nil {
		return nil, err
	}
	if appt.UserID != userID {
		return nil, ErrNotOwner
	}
	// 终态门禁：已核销/服务中/已完成/已取消均不得再走取消退款流程（规则 6 / FR-14）。
	if appt.Status != model.StatusConfirmed {
		return nil, fmt.Errorf("%w: 仅已确认预约可发起取消退款，当前 %s",
			model.ErrIllegalTransition, appt.Status)
	}
	slot, err := s.store.GetSlot(ctx, appt.SlotID)
	if err != nil {
		return nil, err
	}
	lead := slot.StartAt.Sub(s.now())
	if lead >= model.CancelLeadTime {
		return s.autoCancel(ctx, appt, reason, lead)
	}
	if strings.TrimSpace(reason) == "" {
		return nil, ErrReasonRequired
	}
	return nil, s.applyNearCancel(ctx, appt, reason, lead)
}

// autoCancel 自动受理：状态 CAS 到已取消 + 名额释放一次 + 全额退款单（FR-07）。
func (s *Service) autoCancel(ctx context.Context, appt model.Appointment, reason string, lead time.Duration) (*model.Refund, error) {
	if _, _, err := s.store.Transition(ctx, appt.ID, model.StatusConfirmed,
		model.EventCancelEarly, appt.Version); err != nil {
		return nil, err
	}
	if err := s.store.ReleaseSlot(ctx, appt.ID); err != nil && !errors.Is(err, store.ErrAlreadyReleased) {
		return nil, err
	}
	now := s.now()
	s.ledger.SaveCancelRequest(model.CancelRequest{
		ID:            s.newID("CR"),
		AppointmentID: appt.ID,
		Reason:        reason,
		ApplyAt:       now,
		HoursToStart:  lead.Hours(),
		Type:          "自动受理",
		Status:        model.CancelRequestApproved,
		Reviewer:      "system",
		ReviewedAt:    &now,
	})
	refund := s.newRefund(appt.ID, now)
	s.ledger.SaveRefund(refund)
	return &refund, nil
}

// applyNearCancel 转人工审核：名额保持、禁止核销（FR-08 / 规则 3）。
func (s *Service) applyNearCancel(ctx context.Context, appt model.Appointment, reason string, lead time.Duration) error {
	now := s.now()
	req := model.CancelRequest{
		ID:            s.newID("CR"),
		AppointmentID: appt.ID,
		Reason:        reason,
		ApplyAt:       now,
		HoursToStart:  lead.Hours(),
		Type:          "人工审核",
		Status:        model.CancelRequestPending,
	}
	if _, _, err := s.store.Transition(ctx, appt.ID, model.StatusConfirmed,
		model.EventCancelApplyNear, appt.Version); err != nil {
		return err
	}
	s.ledger.SaveCancelRequest(req)
	return nil
}

// ReviewCancel 客服批准或拒绝临近取消申请（FR-08 / SC-03）。
// 批准才释放名额并退款；拒绝回到已确认且名额保持，用户可见不同结果。
func (s *Service) ReviewCancel(ctx context.Context, requestID, reviewer string, approve bool) (*model.Refund, error) {
	req, ok := s.ledger.CancelRequestByID(requestID)
	if !ok {
		return nil, ErrCancelRequestNotFound
	}
	if req.Status != model.CancelRequestPending {
		return nil, ErrCancelAlreadyReviewed
	}
	appt, err := s.store.GetAppointment(ctx, req.AppointmentID)
	if err != nil {
		return nil, err
	}
	now := s.now()
	req.Reviewer = reviewer
	req.ReviewedAt = &now

	if !approve {
		req.Status = model.CancelRequestRejected
		if _, _, err := s.store.Transition(ctx, appt.ID, model.StatusCancelReview,
			model.EventCancelReject, appt.Version); err != nil {
			return nil, err
		}
		s.ledger.SaveCancelRequest(req)
		return nil, nil
	}

	req.Status = model.CancelRequestApproved
	if _, _, err := s.store.Transition(ctx, appt.ID, model.StatusCancelReview,
		model.EventCancelApprove, appt.Version); err != nil {
		return nil, err
	}
	if err := s.store.ReleaseSlot(ctx, appt.ID); err != nil && !errors.Is(err, store.ErrAlreadyReleased) {
		return nil, err
	}
	refund := s.newRefund(appt.ID, now)
	s.ledger.SaveRefund(refund)
	s.ledger.SaveCancelRequest(req)
	return &refund, nil
}

// newRefund 生成 9900 分全额退款单；状态独立于预约主记录（规则 4 / FR-10）。
func (s *Service) newRefund(appointmentID string, now time.Time) model.Refund {
	paymentID := ""
	if payment, ok := s.ledger.PaymentOf(appointmentID); ok {
		paymentID = payment.ID
	}
	return model.Refund{
		ID:            s.newID("RFD"),
		AppointmentID: appointmentID,
		PaymentID:     paymentID,
		AmountFen:     model.AmountFen,
		Status:        model.RefundSucceeded,
		CreatedAt:     now,
		FinishedAt:    &now,
	}
}
