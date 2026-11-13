package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Abeautiful-crypto/Pet-store/internal/model"
)

// ---- F3.8 前台核销三条件准入与服务进度幂等 ----

func TestService_VerifyRequiresThreeConditions(t *testing.T) {
	ctx := context.Background()

	svc1, _, appt1 := confirmedService(t)
	if err := svc1.Verify(ctx, appt1.ID, "OTHER-STORE", "FD-1"); !errors.Is(err, ErrWrongStore) {
		t.Fatalf("非本店员工核销应被拦截，实际: %v", err)
	}

	svc2, _, appt2 := paidService(t)
	if err := svc2.Verify(ctx, appt2.ID, model.StoreID, "FD-1"); !errors.Is(err, model.ErrIllegalTransition) {
		t.Fatalf("未确认预约不得核销，实际: %v", err)
	}

	svc3, _, appt3 := nearCancelService(t)
	if _, err := svc3.Cancel(ctx, appt3.ID, "U1", "临时有事"); err != nil {
		t.Fatal(err)
	}
	if err := svc3.Verify(ctx, appt3.ID, model.StoreID, "FD-1"); !errors.Is(err, ErrPendingCancel) {
		t.Fatalf("存在待处理取消申请时禁止核销，实际: %v", err)
	}
}

func TestService_WrongStoreVerifyDoesNotChangeRecord(t *testing.T) {
	ctx := context.Background()
	svc, _, appt := confirmedService(t)
	if err := svc.Verify(ctx, appt.ID, "OTHER-STORE", "FD-1"); err == nil {
		t.Fatal("非本店操作应被拦截")
	}
	got, _ := svc.store.GetAppointment(ctx, appt.ID)
	if got.Status != model.StatusConfirmed {
		t.Fatalf("被拦截操作不得改动记录，实际 %s", got.Status)
	}
	slot, _ := svc.store.GetSlot(ctx, "SL1")
	if slot.OccupiedCount != 1 {
		t.Fatalf("被拦截操作不得改动名额，occupied_count = %d", slot.OccupiedCount)
	}
	if _, ok := svc.ledger.ServiceRecordOf(appt.ID); ok {
		t.Fatal("被拦截操作不得产生服务记录")
	}
}

func TestService_ServiceProgressIsIdempotent(t *testing.T) {
	ctx := context.Background()
	svc, _, appt := confirmedService(t)
	if err := svc.Verify(ctx, appt.ID, model.StoreID, "FD-1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Verify(ctx, appt.ID, model.StoreID, "FD-1"); err == nil {
		t.Fatal("重复核销必须被拒绝")
	}
	if err := svc.StartService(ctx, appt.ID, model.StoreID); err != nil {
		t.Fatal(err)
	}
	if err := svc.FinishService(ctx, appt.ID, model.StoreID); err != nil {
		t.Fatal(err)
	}
	if err := svc.FinishService(ctx, appt.ID, model.StoreID); err == nil {
		t.Fatal("重复完成必须被拒绝")
	}
	record, ok := svc.ledger.ServiceRecordOf(appt.ID)
	if !ok || record.RevenueFen != model.AmountFen {
		t.Fatalf("收入应只记一次 9900 分，实际 %+v", record)
	}
	if records := svc.ledger.ServiceRecords(); len(records) != 1 {
		t.Fatalf("同一预约只能有一条服务记录，实际 %d", len(records))
	}
	got, _ := svc.store.GetAppointment(ctx, appt.ID)
	if got.Status != model.StatusCompleted {
		t.Fatalf("完成后应为已完成，实际 %s", got.Status)
	}
}

// ---- F3.9 取消 × 核销并发唯一结果与终态门禁 ----

func TestService_ConcurrentCancelAndVerifyOnlyOneWinner(t *testing.T) {
	ctx := context.Background()
	svc, _, appt := confirmedService(t)

	var wg sync.WaitGroup
	winners := make(chan string, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		if _, err := svc.Cancel(ctx, appt.ID, "U1", ""); err == nil {
			winners <- "cancel"
		}
	}()
	go func() {
		defer wg.Done()
		if err := svc.Verify(ctx, appt.ID, model.StoreID, "FD-1"); err == nil {
			winners <- "verify"
		}
	}()
	wg.Wait()
	close(winners)

	var won []string
	for w := range winners {
		won = append(won, w)
	}
	if len(won) != 1 {
		t.Fatalf("并发只能有一个胜者，实际 %v", won)
	}
	got, _ := svc.store.GetAppointment(ctx, appt.ID)
	slot, _ := svc.store.GetSlot(ctx, "SL1")
	if won[0] == "cancel" {
		if got.Status != model.StatusCancelled || slot.OccupiedCount != 0 {
			t.Fatalf("取消胜出时应为已取消且名额释放，status=%s occupied=%d", got.Status, slot.OccupiedCount)
		}
		if refunds := svc.ledger.Refunds(); len(refunds) != 1 {
			t.Fatalf("取消胜出应生成 1 张退款单，实际 %d", len(refunds))
		}
	} else {
		if got.Status != model.StatusVerified || slot.OccupiedCount != 1 {
			t.Fatalf("核销胜出时应为已核销且名额保持，status=%s occupied=%d", got.Status, slot.OccupiedCount)
		}
		if refunds := svc.ledger.Refunds(); len(refunds) != 0 {
			t.Fatalf("核销胜出不得产生退款单，实际 %d", len(refunds))
		}
	}
	events, _ := svc.store.ListEvents(ctx)
	if len(events) == 0 {
		t.Fatal("并发冲突必须留痕")
	}
}

func TestService_TerminalStatesRejectCancel(t *testing.T) {
	ctx := context.Background()
	stages := []string{"verified", "in_service", "completed"}
	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			svc, _, appt := confirmedService(t)
			if err := svc.Verify(ctx, appt.ID, model.StoreID, "FD-1"); err != nil {
				t.Fatal(err)
			}
			if stage != "verified" {
				if err := svc.StartService(ctx, appt.ID, model.StoreID); err != nil {
					t.Fatal(err)
				}
			}
			if stage == "completed" {
				if err := svc.FinishService(ctx, appt.ID, model.StoreID); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := svc.Cancel(ctx, appt.ID, "U1", ""); !errors.Is(err, model.ErrIllegalTransition) {
				t.Fatalf("终态/接待态不得进入取消退款流程，实际: %v", err)
			}
		})
	}
}

// ---- F3.10 时段结束未到店标记（写原因、不自动退款）----

func TestService_NoShowRequiresSlotEndedAndReason(t *testing.T) {
	ctx := context.Background()
	svc, clock, appt := confirmedService(t)
	if err := svc.MarkNoShow(ctx, appt.ID, "未到店"); !errors.Is(err, ErrSlotNotEnded) {
		t.Fatalf("时段未结束不得标记未到店，实际: %v", err)
	}
	clock.Advance(6 * time.Hour) // end_at = fixedNow + 5h
	if err := svc.MarkNoShow(ctx, appt.ID, ""); !errors.Is(err, ErrReasonRequired) {
		t.Fatalf("未到店原因必填，实际: %v", err)
	}
	if err := svc.MarkNoShow(ctx, appt.ID, "主人未到场"); err != nil {
		t.Fatalf("合法未到店标记应成功: %v", err)
	}
	got, _ := svc.store.GetAppointment(ctx, appt.ID)
	if got.Status != model.StatusNoShow {
		t.Fatalf("标记后应为未到店，实际 %s", got.Status)
	}
	if refunds := svc.ledger.Refunds(); len(refunds) != 0 {
		t.Fatalf("本题约定未到店不自动退款，实际 %d 张退款单", len(refunds))
	}
	record, ok := svc.ledger.ServiceRecordOf(appt.ID)
	if !ok || record.NoShowReason != "主人未到场" {
		t.Fatalf("未到店原因应留痕，实际 %+v", record)
	}
}

func TestService_NoShowRejectsVerifiedAppointment(t *testing.T) {
	ctx := context.Background()
	svc, clock, appt := confirmedService(t)
	if err := svc.Verify(ctx, appt.ID, model.StoreID, "FD-1"); err != nil {
		t.Fatal(err)
	}
	clock.Advance(6 * time.Hour)
	if err := svc.MarkNoShow(ctx, appt.ID, "原因"); !errors.Is(err, ErrAlreadyVerified) {
		t.Fatalf("已有核销记录不得标记未到店，实际: %v", err)
	}
}
