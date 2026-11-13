package service

import (
	"context"

	"github.com/Abeautiful-crypto/Pet-store/internal/model"
)

// AppointmentView 是用户/前台/客服/店长查看同一条预约主记录的聚合视图。
// 各角色可见字段由 API 层裁剪，但都来自同一份主记录与同一组只增不改的流水。
type AppointmentView struct {
	Appointment   model.Appointment
	Payment       *model.Payment
	Refund        *model.Refund
	CancelRequest *model.CancelRequest
	ServiceRecord *model.ServiceRecord
	Outcome       string
}

// Snapshot 返回预约的四种结局之一及可逐笔反查的单据（FR-16 / SC-05）。
func (s *Service) Snapshot(ctx context.Context, appointmentID string) (AppointmentView, error) {
	appt, err := s.store.GetAppointment(ctx, appointmentID)
	if err != nil {
		return AppointmentView{}, err
	}
	view := AppointmentView{Appointment: appt}
	if payment, ok := s.ledger.PaymentOf(appointmentID); ok {
		view.Payment = &payment
	}
	if refund, ok := s.ledger.RefundOf(appointmentID); ok {
		view.Refund = &refund
	}
	if req, ok := s.ledger.CancelRequestOf(appointmentID); ok {
		view.CancelRequest = &req
	}
	if record, ok := s.ledger.ServiceRecordOf(appointmentID); ok {
		view.ServiceRecord = &record
	}
	view.Outcome = outcomeOf(view)
	return view, nil
}

func outcomeOf(view AppointmentView) string {
	switch view.Appointment.Status {
	case model.StatusCompleted:
		return "已完成服务"
	case model.StatusNoShow:
		return "未到店"
	case model.StatusCancelled:
		if view.Refund != nil && view.Refund.Status == model.RefundFailed {
			return "退款失败待跟进"
		}
		return "已取消退款"
	default:
		return "进行中"
	}
}

// ListSlots 供接口层展示可预约时段与剩余名额。
func (s *Service) ListSlots(ctx context.Context) ([]model.Slot, error) {
	return s.store.ListSlots(ctx)
}
