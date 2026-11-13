package service

import (
	"context"

	"github.com/Abeautiful-crypto/Pet-store/internal/model"
)

// Settlement 是店长日清结果：金额三分类 + 已完成服务收入 + 五类记录（FR-17、FR-18 / 规则 8）。
// 所有金额均为 int64 分，且只能从支付单 / 退款单 / 服务记录逐笔反查。
type Settlement struct {
	CollectedFen  int64
	RefundedFen   int64
	UnrefundedFen int64
	RevenueFen    int64

	Active        []string
	Completed     []string
	Cancelled     []string
	NoShow        []string
	RefundPending []string
}

// DailySettlement 汇总当日记录（SRS 9.3 演示口径）。
// 关键：已完成服务收入只认已完成服务记录的 revenue_fen，绝不把已付款全部计作收入。
func (s *Service) DailySettlement(ctx context.Context) (Settlement, error) {
	var out Settlement
	for _, payment := range s.ledger.Payments() {
		if payment.Status == model.PaymentSucceeded {
			out.CollectedFen += payment.AmountFen
		}
	}
	for _, refund := range s.ledger.Refunds() {
		switch refund.Status {
		case model.RefundSucceeded:
			out.RefundedFen += refund.AmountFen
		case model.RefundPending, model.RefundProcessing, model.RefundFailed:
			out.RefundPending = append(out.RefundPending, refund.AppointmentID)
		}
	}
	out.UnrefundedFen = out.CollectedFen - out.RefundedFen
	for _, record := range s.ledger.ServiceRecords() {
		out.RevenueFen += record.RevenueFen
	}

	appointments, err := s.store.ListAppointments(ctx)
	if err != nil {
		return out, err
	}
	for _, appt := range appointments {
		switch appt.Status {
		case model.StatusCompleted:
			out.Completed = append(out.Completed, appt.ID)
		case model.StatusCancelled:
			out.Cancelled = append(out.Cancelled, appt.ID)
		case model.StatusNoShow:
			out.NoShow = append(out.NoShow, appt.ID)
		default:
			if model.IsActive(appt.Status) {
				out.Active = append(out.Active, appt.ID)
			}
		}
	}
	return out, nil
}
