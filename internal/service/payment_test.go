package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Abeautiful-crypto/Pet-store/internal/model"
)

func paidService(t *testing.T) (*Service, *mutableClock, *model.Appointment) {
	t.Helper()
	ctx := context.Background()
	clock := &mutableClock{now: fixedNow}
	svc, _ := newServiceWithClock(t, clock)
	if err := svc.PublishSlot(ctx, serviceSlot("SL1")); err != nil {
		t.Fatal(err)
	}
	appt, err := svc.CreateAppointment(ctx, "U1", "P1", "SL1")
	if err != nil {
		t.Fatal(err)
	}
	return svc, clock, appt
}

func TestService_ConfirmPaymentIsIdempotent(t *testing.T) {
	ctx := context.Background()
	svc, _, appt := paidService(t)

	if err := svc.ConfirmPayment(ctx, appt.ID, "NOTIFY-1", "TRADE-1"); err != nil {
		t.Fatalf("首次支付通知应成功: %v", err)
	}
	got, _ := svc.store.GetAppointment(ctx, appt.ID)
	if got.Status != model.StatusConfirmed {
		t.Fatalf("有效付款后应为已确认，实际 %s", got.Status)
	}
	if err := svc.ConfirmPayment(ctx, appt.ID, "NOTIFY-1", "TRADE-1"); err != nil {
		t.Fatalf("重复通知应幂等命中而非报错: %v", err)
	}
	got2, _ := svc.store.GetAppointment(ctx, appt.ID)
	if got2.Status != model.StatusConfirmed || got2.Version != got.Version {
		t.Fatalf("重复通知不得重复确认: status=%s version=%d -> %d",
			got2.Status, got.Version, got2.Version)
	}
	if payments := svc.ledger.Payments(); len(payments) != 1 {
		t.Fatalf("同一 notify_key 只应有 1 条支付流水，实际 %d", len(payments))
	}
}

func TestService_ConcurrentDuplicateNotifyOnlyOneConfirm(t *testing.T) {
	ctx := context.Background()
	svc, _, appt := paidService(t)

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = svc.ConfirmPayment(ctx, appt.ID, "NOTIFY-1", "TRADE-1")
		}()
	}
	wg.Wait()

	if payments := svc.ledger.Payments(); len(payments) != 1 {
		t.Fatalf("并发重复通知只应确认一次，实际 %d 条流水", len(payments))
	}
	got, _ := svc.store.GetAppointment(ctx, appt.ID)
	if got.Status != model.StatusConfirmed || got.Version != 2 {
		t.Fatalf("状态与版本应只推进一次: status=%s version=%d", got.Status, got.Version)
	}
}

func TestService_LatePayCallbackRegistersExceptionAndRefund(t *testing.T) {
	ctx := context.Background()
	svc, clock, appt := paidService(t)

	clock.Advance(model.HoldDuration + time.Second)
	if n, err := svc.ExpireHolds(ctx); err != nil || n != 1 {
		t.Fatalf("先让占位超时关闭: n=%d err=%v", n, err)
	}
	if err := svc.ConfirmPayment(ctx, appt.ID, "NOTIFY-LATE", "TRADE-LATE"); err != nil {
		t.Fatalf("迟到回调应被受理并登记，不应报错: %v", err)
	}

	got, _ := svc.store.GetAppointment(ctx, appt.ID)
	if got.Status != model.StatusClosed {
		t.Fatalf("迟到回调不得复活预约，实际状态 %s", got.Status)
	}
	if exceptions := svc.ledger.Exceptions(); len(exceptions) != 1 {
		t.Fatalf("迟到支付必须登记异常，实际 %d 条", len(exceptions))
	}
	refund, ok := svc.ledger.RefundOf(appt.ID)
	if !ok {
		t.Fatal("迟到支付必须生成退款单")
	}
	if refund.AmountFen != model.AmountFen {
		t.Fatalf("退款金额应为 9900 分，实际 %d", refund.AmountFen)
	}
	slot, _ := svc.store.GetSlot(ctx, "SL1")
	if slot.OccupiedCount != 0 {
		t.Fatalf("迟到回调不得恢复名额，occupied_count = %d", slot.OccupiedCount)
	}
}

func TestService_LatePayCallbackIsIdempotentToo(t *testing.T) {
	ctx := context.Background()
	svc, clock, appt := paidService(t)
	clock.Advance(model.HoldDuration + time.Second)
	if _, err := svc.ExpireHolds(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := svc.ConfirmPayment(ctx, appt.ID, "NOTIFY-LATE", "TRADE-LATE"); err != nil {
			t.Fatal(err)
		}
	}
	if exceptions := svc.ledger.Exceptions(); len(exceptions) != 1 {
		t.Fatalf("重复迟到回调不得重复登记异常，实际 %d 条", len(exceptions))
	}
	if refunds := svc.ledger.Refunds(); len(refunds) != 1 {
		t.Fatalf("重复迟到回调不得重复生成退款单，实际 %d 条", len(refunds))
	}
}
