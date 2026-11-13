package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Abeautiful-crypto/Pet-store/internal/model"
)

func TestService_RefundFailureThenRetrySucceeds(t *testing.T) {
	ctx := context.Background()
	svc, _, appt := confirmedService(t)
	svc.SetRefundOutcome(false)

	refund, err := svc.Cancel(ctx, appt.ID, "U1", "")
	if err != nil {
		t.Fatal(err)
	}
	if refund.Status != model.RefundFailed || refund.LastError == "" {
		t.Fatalf("渠道失败应留下失败待跟进状态与原因，实际 %+v", refund)
	}
	got, _ := svc.store.GetAppointment(ctx, appt.ID)
	if got.Status != model.StatusCancelled {
		t.Fatalf("退款失败不得改变已取消终态，实际 %s", got.Status)
	}
	slot, _ := svc.store.GetSlot(ctx, "SL1")
	if slot.OccupiedCount != 0 {
		t.Fatalf("退款失败不得恢复名额，occupied_count = %d", slot.OccupiedCount)
	}

	svc.SetRefundOutcome(true)
	retried, err := svc.RetryRefund(ctx, appt.ID)
	if err != nil {
		t.Fatalf("客服重试应成功: %v", err)
	}
	if retried.Status != model.RefundSucceeded || retried.RetryCount != 1 {
		t.Fatalf("重试后应为成功且重试次数 1，实际 %+v", retried)
	}
	if events := svc.ledger.RefundEvents(); len(events) != 2 {
		t.Fatalf("失败→重试→成功的状态链应可查，实际 %d 条", len(events))
	}
	got2, _ := svc.store.GetAppointment(ctx, appt.ID)
	if got2.Status != model.StatusCancelled {
		t.Fatalf("重试全程预约不得复活，实际 %s", got2.Status)
	}
	slot2, _ := svc.store.GetSlot(ctx, "SL1")
	if slot2.OccupiedCount != 0 {
		t.Fatalf("重试全程名额不得恢复，occupied_count = %d", slot2.OccupiedCount)
	}
}

func TestService_RefundFailureDoesNotReopenAppointment(t *testing.T) {
	ctx := context.Background()
	svc, _, appt := confirmedService(t)
	svc.SetRefundOutcome(false)
	if _, err := svc.Cancel(ctx, appt.ID, "U1", ""); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := svc.Cancel(ctx, appt.ID, "U1", "再次取消"); err == nil {
			t.Fatal("退款失败期间重复取消必须显式报错")
		}
		if _, err := svc.RetryRefund(ctx, appt.ID); err != nil {
			t.Fatalf("失败状态下允许重试: %v", err)
		}
	}
	got, _ := svc.store.GetAppointment(ctx, appt.ID)
	if got.Status != model.StatusCancelled {
		t.Fatalf("已取消终态不得复活，实际 %s", got.Status)
	}
	slot, _ := svc.store.GetSlot(ctx, "SL1")
	if slot.OccupiedCount != 0 {
		t.Fatalf("名额不得恢复，occupied_count = %d", slot.OccupiedCount)
	}
}

func TestService_RetryOnlyFailedRefund(t *testing.T) {
	ctx := context.Background()
	svc, _, appt := confirmedService(t)
	if _, err := svc.Cancel(ctx, appt.ID, "U1", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RetryRefund(ctx, appt.ID); !errors.Is(err, ErrRefundNotRetryable) {
		t.Fatalf("已成功的退款单不得重复重试，实际: %v", err)
	}
}
