package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Abeautiful-crypto/Pet-store/internal/model"
	"github.com/Abeautiful-crypto/Pet-store/internal/store"
)

func TestService_DailySettlementAmountsAndCategories(t *testing.T) {
	ctx := context.Background()
	clock := &mutableClock{now: fixedNow}
	st := store.NewMemoryStore()
	svc := New(Config{
		Store:   st,
		Now:     clock.Now,
		OwnsPet: func(string, string) bool { return true },
	})

	var ids []string
	for i := 1; i <= 4; i++ {
		slot := serviceSlot(fmt.Sprintf("SL%d", i))
		if err := svc.PublishSlot(ctx, slot); err != nil {
			t.Fatal(err)
		}
		appt, err := svc.CreateAppointment(ctx, "U1", fmt.Sprintf("P%d", i), slot.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.ConfirmPayment(ctx, appt.ID, fmt.Sprintf("N%d", i), fmt.Sprintf("T%d", i)); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, appt.ID)
	}

	// A2：提前取消自动受理，全额退款成功。
	if _, err := svc.Cancel(ctx, ids[1], "U1", ""); err != nil {
		t.Fatal(err)
	}
	// A1：正常接待，服务完成，记收入一次。
	if err := svc.Verify(ctx, ids[0], model.StoreID, "FD-1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.StartService(ctx, ids[0], model.StoreID); err != nil {
		t.Fatal(err)
	}
	if err := svc.FinishService(ctx, ids[0], model.StoreID); err != nil {
		t.Fatal(err)
	}
	// A3：临近取消客服批准，退款失败待跟进。
	clock.Advance(3 * time.Hour)
	svc.SetRefundOutcome(false)
	if _, err := svc.Cancel(ctx, ids[2], "U1", "临时有事"); err != nil {
		t.Fatal(err)
	}
	req, _ := svc.ledger.CancelRequestOf(ids[2])
	if _, err := svc.ReviewCancel(ctx, req.ID, "CS-1", true); err != nil {
		t.Fatal(err)
	}
	svc.SetRefundOutcome(true)
	// A4：时段结束未到店，本题约定不自动退款。
	clock.Advance(3 * time.Hour)
	if err := svc.MarkNoShow(ctx, ids[3], "主人未到场"); err != nil {
		t.Fatal(err)
	}

	settlement, err := svc.DailySettlement(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settlement.CollectedFen != 39600 {
		t.Fatalf("已收款应为 39600 分，实际 %d", settlement.CollectedFen)
	}
	if settlement.RefundedFen != 9900 {
		t.Fatalf("已退款应为 9900 分，实际 %d", settlement.RefundedFen)
	}
	if settlement.UnrefundedFen != 29700 {
		t.Fatalf("未退款金额应为 29700 分，实际 %d", settlement.UnrefundedFen)
	}
	if settlement.RevenueFen != 9900 {
		t.Fatalf("已完成服务收入只能是 9900 分，实际 %d", settlement.RevenueFen)
	}
	if len(settlement.Completed) != 1 || settlement.Completed[0] != ids[0] {
		t.Fatalf("已完成记录应只有 A1，实际 %v", settlement.Completed)
	}
	if len(settlement.Cancelled) != 2 {
		t.Fatalf("取消记录应为 A2/A3 两条，实际 %v", settlement.Cancelled)
	}
	if len(settlement.NoShow) != 1 || settlement.NoShow[0] != ids[3] {
		t.Fatalf("未到店记录应只有 A4，实际 %v", settlement.NoShow)
	}
	if len(settlement.RefundPending) != 1 || settlement.RefundPending[0] != ids[2] {
		t.Fatalf("退款待处理应只有 A3，实际 %v", settlement.RefundPending)
	}
	if len(settlement.Active) != 0 {
		t.Fatalf("四条记录均已终态，不应有有效预约，实际 %v", settlement.Active)
	}
}
