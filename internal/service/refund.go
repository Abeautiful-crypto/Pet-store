package service

import (
	"context"
	"errors"

	"github.com/Abeautiful-crypto/Pet-store/internal/model"
)

var (
	ErrRefundNotFound     = errors.New("退款单不存在")
	ErrRefundNotRetryable = errors.New("仅失败待跟进的退款单可重试")
)

// RetryRefund 客服跟进退款失败并重试（FR-10 / 规则 4）。
// 退款状态独立于预约状态：重试全程不触碰预约主记录、不恢复名额。
func (s *Service) RetryRefund(_ context.Context, appointmentID string) (*model.Refund, error) {
	refund, ok := s.ledger.RefundOf(appointmentID)
	if !ok {
		return nil, ErrRefundNotFound
	}
	if refund.Status != model.RefundFailed {
		return nil, ErrRefundNotRetryable
	}
	refund.RetryCount++
	now := s.now()
	if s.refundOK.Load() {
		refund.Status = model.RefundSucceeded
		refund.LastError = ""
		refund.FinishedAt = &now
	} else {
		refund.Status = model.RefundFailed
		refund.LastError = "模拟渠道退款失败"
		refund.FinishedAt = nil
	}
	s.ledger.SaveRefund(refund)
	s.ledger.AppendRefundEvent(refund)
	return &refund, nil
}
