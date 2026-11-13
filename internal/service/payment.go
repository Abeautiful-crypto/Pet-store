package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Abeautiful-crypto/Pet-store/internal/model"
	"github.com/Abeautiful-crypto/Pet-store/internal/store"
)

// payDedupTTL 是支付通知去重键在缓存（Redis/Memory）中的保留时长；
// 远长于单次支付链路，覆盖并发重复回调与短时重试（FR-05 / 规则 2）。
const payDedupTTL = 24 * time.Hour

// ConfirmPayment 处理模拟渠道的支付成功通知（FR-05、FR-06 / 规则 2）。
// 幂等键为 notify_key：重复通知只记一条流水，不重复确认、不重复记收入。
// 缓存去重（SETNX+TTL）是第一道闸，MySQL 唯一索引是最终兜底。
func (s *Service) ConfirmPayment(ctx context.Context, appointmentID, notifyKey, channelTradeNo string) error {
	if s.deduper != nil {
		// 重复通知：缓存已占用该 notify_key，直接幂等命中（不重复占名额、不重复记收入）。
		if acquired, err := s.deduper.Dedup(ctx, notifyKey, payDedupTTL); err == nil && !acquired {
			return nil
		}
	}
	if _, exists := s.ledger.PaymentByNotify(notifyKey); exists {
		return nil
	}
	appt, err := s.store.GetAppointment(ctx, appointmentID)
	if err != nil {
		return err
	}
	now := s.now()

	// 已关闭后的迟到回调：登记异常 + 退款，保持已关闭，不恢复预约、不恢复名额。
	if appt.Status == model.StatusClosed {
		return s.registerLateCallback(appointmentID, notifyKey, channelTradeNo, now)
	}
	if appt.Status != model.StatusPendingPay {
		return fmt.Errorf("%w: 支付通知到达时预约状态为 %s", model.ErrIllegalTransition, appt.Status)
	}

	// 规则 2：状态确认走 version CAS，只有一个写入点。
	_, _, err = s.store.Transition(ctx, appointmentID, model.StatusPendingPay,
		model.EventPaySuccess, appt.Version)
	if err != nil {
		// 并发重复通知：输方若发现该 notify_key 已被确认，视为幂等命中。
		if errors.Is(err, store.ErrVersionConflict) {
			if _, confirmed := s.ledger.PaymentByNotify(notifyKey); confirmed {
				return nil
			}
		}
		return err
	}

	s.ledger.SavePayment(model.Payment{
		ID:             s.newID("PAY"),
		AppointmentID:  appointmentID,
		AmountFen:      model.AmountFen,
		ChannelTradeNo: channelTradeNo,
		NotifyKey:      notifyKey,
		Status:         model.PaymentSucceeded,
		PaidAt:         &now,
		CallbackAt:     &now,
	})
	return nil
}

func (s *Service) registerLateCallback(appointmentID, notifyKey, channelTradeNo string, now time.Time) error {
	paymentID := s.newID("PAY")
	saved := s.ledger.SavePayment(model.Payment{
		ID:             paymentID,
		AppointmentID:  appointmentID,
		AmountFen:      model.AmountFen,
		ChannelTradeNo: channelTradeNo,
		NotifyKey:      notifyKey,
		Status:         model.PaymentExceptionWait,
		CallbackAt:     &now,
	})
	if !saved {
		return nil
	}
	s.ledger.SaveRefund(model.Refund{
		ID:            s.newID("RFD"),
		AppointmentID: appointmentID,
		PaymentID:     paymentID,
		AmountFen:     model.AmountFen,
		Status:        model.RefundPending,
		CreatedAt:     now,
	})
	s.ledger.AppendException(model.EventLog{
		ID:            s.newID("EVT"),
		AppointmentID: appointmentID,
		Action:        string(model.EventLatePayCallback),
		FromStatus:    model.StatusClosed,
		ToStatus:      model.StatusClosed,
		Detail:        "关闭后迟到的支付成功回调：登记异常并退款，不恢复预约与名额",
		CreatedAt:     now,
	})
	return nil
}
