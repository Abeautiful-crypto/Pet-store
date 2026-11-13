package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Abeautiful-crypto/Pet-store/internal/model"
	"github.com/Abeautiful-crypto/Pet-store/internal/store"
)

// F5.1 覆盖率补测：只补测试，不改实现、不放宽断言。

type failingListStore struct{ *store.MemoryStore }

func (f *failingListStore) ListAppointments(context.Context) ([]model.Appointment, error) {
	return nil, errors.New("db down")
}

type failingGetSlotStore struct{ *store.MemoryStore }

func (f *failingGetSlotStore) GetSlot(context.Context, string) (model.Slot, error) {
	return model.Slot{}, store.ErrSlotNotFound
}

func TestCoverage_DefaultAssemblyAndMissingRecords(t *testing.T) {
	ctx := context.Background()
	svc := New(Config{Store: store.NewMemoryStore()})
	if _, err := svc.CreateAppointment(ctx, "U1", "P1", "SL1"); !errors.Is(err, ErrPetNotOwned) {
		t.Fatalf("默认宠物归属守卫应拒绝: %v", err)
	}
	if _, err := svc.Snapshot(ctx, "NOPE"); !errors.Is(err, store.ErrAppointmentNotFound) {
		t.Fatalf("Snapshot 缺记录: %v", err)
	}
	if slots, err := svc.ListSlots(ctx); err != nil || len(slots) != 0 {
		t.Fatalf("ListSlots: %d %v", len(slots), err)
	}
	svc2 := New(Config{
		Store:   store.NewMemoryStore(),
		Now:     func() time.Time { return fixedNow },
		OwnsPet: func(string, string) bool { return true },
	})
	if _, err := svc2.CreateAppointment(ctx, "U1", "P1", "NOPE"); !errors.Is(err, store.ErrSlotNotFound) {
		t.Fatalf("时段不存在应透传: %v", err)
	}
	if _, err := svc2.CreateAppointment(ctx, "U1", "P1", "SL1"); !errors.Is(err, store.ErrSlotNotFound) {
		t.Fatalf("时段不存在应透传: %v", err)
	}
}

func TestCoverage_SnapshotAllOutcomes(t *testing.T) {
	ctx := context.Background()
	clock := &mutableClock{now: fixedNow}
	svc, _ := newServiceWithClock(t, clock)
	var ids []string
	for i := 1; i <= 4; i++ {
		slotID := fmt.Sprintf("SL%d", i)
		if err := svc.PublishSlot(ctx, serviceSlot(slotID)); err != nil {
			t.Fatal(err)
		}
		appt, err := svc.CreateAppointment(ctx, "U1", fmt.Sprintf("P%d", i), slotID)
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.ConfirmPayment(ctx, appt.ID, fmt.Sprintf("N%d", i), fmt.Sprintf("T%d", i)); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, appt.ID)
	}

	if err := svc.Verify(ctx, ids[0], model.StoreID, "FD1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.StartService(ctx, ids[0], model.StoreID); err != nil {
		t.Fatal(err)
	}
	if err := svc.FinishService(ctx, ids[0], model.StoreID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Cancel(ctx, ids[1], "U1", ""); err != nil {
		t.Fatal(err)
	}
	svc.SetRefundOutcome(false)
	if _, err := svc.Cancel(ctx, ids[2], "U1", ""); err != nil {
		t.Fatal(err)
	}
	svc.SetRefundOutcome(true)
	clock.Advance(6 * time.Hour)
	if err := svc.MarkNoShow(ctx, ids[3], "主人未到场"); err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		ids[0]: "已完成服务", ids[1]: "已取消退款",
		ids[2]: "退款失败待跟进", ids[3]: "未到店",
	}
	for id, expected := range want {
		view, err := svc.Snapshot(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if view.Outcome != expected {
			t.Fatalf("%s outcome = %s, want %s", id, view.Outcome, expected)
		}
		if view.Appointment.AmountFen != model.AmountFen || view.Payment == nil {
			t.Fatalf("主记录与支付流水应可反查: %+v", view)
		}
	}
	slots, err := svc.ListSlots(ctx)
	if err != nil || len(slots) != 4 {
		t.Fatalf("ListSlots = %d, err=%v", len(slots), err)
	}
}

func TestCoverage_ServiceErrorBranches(t *testing.T) {
	ctx := context.Background()
	svc, _, appt := confirmedService(t)

	if _, err := svc.ReviewCancel(ctx, "NOPE", "CS1", true); !errors.Is(err, ErrCancelRequestNotFound) {
		t.Fatalf("申请不存在: %v", err)
	}
	if _, err := svc.RetryRefund(ctx, appt.ID); !errors.Is(err, ErrRefundNotFound) {
		t.Fatalf("退款单不存在: %v", err)
	}
	if _, err := svc.Cancel(ctx, "NOPE", "U1", ""); !errors.Is(err, store.ErrAppointmentNotFound) {
		t.Fatalf("取消缺记录: %v", err)
	}
	if err := svc.CancelUnpaid(ctx, "NOPE", "U1"); !errors.Is(err, store.ErrAppointmentNotFound) {
		t.Fatalf("主动取消缺记录: %v", err)
	}
	if err := svc.Verify(ctx, "NOPE", model.StoreID, "FD1"); !errors.Is(err, store.ErrAppointmentNotFound) {
		t.Fatalf("核销缺记录: %v", err)
	}
	if err := svc.MarkNoShow(ctx, "NOPE", "x"); !errors.Is(err, store.ErrAppointmentNotFound) {
		t.Fatalf("未到店缺记录: %v", err)
	}
	if err := svc.ConfirmPayment(ctx, "NOPE", "N", "T"); !errors.Is(err, store.ErrAppointmentNotFound) {
		t.Fatalf("支付缺记录: %v", err)
	}
	if err := svc.StartService(ctx, appt.ID, "OTHER-STORE"); !errors.Is(err, ErrWrongStore) {
		t.Fatalf("非本店登记开始: %v", err)
	}
	if err := svc.FinishService(ctx, appt.ID, "OTHER-STORE"); !errors.Is(err, ErrWrongStore) {
		t.Fatalf("非本店登记完成: %v", err)
	}
	if err := svc.Verify(ctx, appt.ID, "OTHER-STORE", "FD1"); !errors.Is(err, ErrWrongStore) {
		t.Fatalf("非本店核销: %v", err)
	}
	for _, err := range []error{store.ErrVersionConflict, store.ErrAlreadyReleased,
		model.ErrTerminalStatus, model.ErrIllegalTransition} {
		if !isConcurrentClose(err) {
			t.Fatalf("应识别为并发冲突: %v", err)
		}
	}
	if isConcurrentClose(errors.New("other")) {
		t.Fatal("普通错误不应视为并发冲突")
	}
}

func TestCoverage_ServiceRecordMissing(t *testing.T) {
	ctx := context.Background()
	svc1, _, appt1 := confirmedService(t)
	if err := svc1.Verify(ctx, appt1.ID, model.StoreID, "FD1"); err != nil {
		t.Fatal(err)
	}
	svc1.ledger.Reset()
	if err := svc1.StartService(ctx, appt1.ID, model.StoreID); !errors.Is(err, ErrServiceRecordNotFound) {
		t.Fatalf("缺少服务记录应显式报错: %v", err)
	}

	svc2, _, appt2 := confirmedService(t)
	if err := svc2.Verify(ctx, appt2.ID, model.StoreID, "FD1"); err != nil {
		t.Fatal(err)
	}
	if err := svc2.StartService(ctx, appt2.ID, model.StoreID); err != nil {
		t.Fatal(err)
	}
	svc2.ledger.Reset()
	if err := svc2.FinishService(ctx, appt2.ID, model.StoreID); !errors.Is(err, ErrServiceRecordNotFound) {
		t.Fatalf("缺少服务记录应显式报错: %v", err)
	}
}

func TestCoverage_StoreFailurePropagation(t *testing.T) {
	ctx := context.Background()
	listStore := &failingListStore{MemoryStore: store.NewMemoryStore()}
	listSvc := New(Config{Store: listStore, Now: func() time.Time { return fixedNow }})
	if _, err := listSvc.ExpireHolds(ctx); err == nil {
		t.Fatal("存储错误必须显式返回，不得静默吞错")
	}

	base := store.NewMemoryStore()
	svc := New(Config{
		Store:   base,
		Now:     func() time.Time { return fixedNow },
		OwnsPet: func(string, string) bool { return true },
	})
	if err := svc.PublishSlot(ctx, serviceSlot("SL1")); err != nil {
		t.Fatal(err)
	}
	appt, err := svc.CreateAppointment(ctx, "U1", "P1", "SL1")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ConfirmPayment(ctx, appt.ID, "N1", "T1"); err != nil {
		t.Fatal(err)
	}
	svc.store = &failingGetSlotStore{MemoryStore: base}
	if _, err := svc.Cancel(ctx, appt.ID, "U1", ""); !errors.Is(err, store.ErrSlotNotFound) {
		t.Fatalf("取消时存储错误应透传: %v", err)
	}
	if err := svc.MarkNoShow(ctx, appt.ID, "x"); !errors.Is(err, store.ErrSlotNotFound) {
		t.Fatalf("未到店时存储错误应透传: %v", err)
	}
}
