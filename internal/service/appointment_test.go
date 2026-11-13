package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Abeautiful-crypto/Pet-store/internal/model"
	"github.com/Abeautiful-crypto/Pet-store/internal/store"
)

var fixedNow = time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)

func serviceSlot(id string) model.Slot {
	start := fixedNow.Add(4 * time.Hour)
	return model.Slot{
		ID:            id,
		StoreID:       model.StoreID,
		ServiceItemID: model.ServiceItemID,
		StartAt:       start,
		EndAt:         start.Add(time.Hour),
		Capacity:      model.SlotCapacity,
	}
}

func newServiceWithOwners(t *testing.T, now time.Time, owns func(userID, petID string) bool) (*Service, *store.MemoryStore) {
	t.Helper()
	st := store.NewMemoryStore()
	svc := New(Config{
		Store:   st,
		Now:     func() time.Time { return now },
		OwnsPet: owns,
	})
	return svc, st
}

func newService(t *testing.T) (*Service, *store.MemoryStore) {
	return newServiceWithOwners(t, fixedNow, func(string, string) bool { return true })
}

func TestService_PublishSlotRejectsInvalidRange(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	bad := serviceSlot("SL1")
	bad.EndAt = bad.StartAt
	if err := svc.PublishSlot(ctx, bad); err == nil {
		t.Fatal("end_at 不晚于 start_at 的时段必须被拒绝（FR-01）")
	}
	if err := svc.PublishSlot(ctx, serviceSlot("SL1")); err != nil {
		t.Fatalf("合法时段应发布成功: %v", err)
	}
}

func TestService_CreateAppointmentRequiresOwnPet(t *testing.T) {
	ctx := context.Background()
	svc, _ := newServiceWithOwners(t, fixedNow,
		func(userID, petID string) bool { return userID == "U1" && petID == "P1" })
	if err := svc.PublishSlot(ctx, serviceSlot("SL1")); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateAppointment(ctx, "U1", "P2", "SL1"); !errors.Is(err, ErrPetNotOwned) {
		t.Fatalf("不得为他人宠物预约（FR-02），实际: %v", err)
	}
	if _, err := svc.CreateAppointment(ctx, "U1", "P1", "SL1"); err != nil {
		t.Fatalf("自己的宠物应可预约: %v", err)
	}
}

func TestService_CreateAppointmentRejectsStartedSlot(t *testing.T) {
	ctx := context.Background()
	slot := serviceSlot("SL1")
	svc, _ := newServiceWithOwners(t, slot.StartAt, func(string, string) bool { return true })
	if err := svc.PublishSlot(ctx, slot); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateAppointment(ctx, "U1", "P1", "SL1"); !errors.Is(err, ErrSlotStarted) {
		t.Fatalf("当前时间 >= start_at 时不可预约（FR-03），实际: %v", err)
	}
}

func TestService_CreateAppointmentRejectsFullSlot(t *testing.T) {
	ctx := context.Background()
	svc, st := newService(t)
	if err := svc.PublishSlot(ctx, serviceSlot("SL1")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < model.SlotCapacity; i++ {
		if _, err := svc.CreateAppointment(ctx, "U1", fmt.Sprintf("P%d", i), "SL1"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.CreateAppointment(ctx, "U1", "P9", "SL1"); !errors.Is(err, store.ErrSlotFull) {
		t.Fatalf("第 4 个预约应报名额不足，实际: %v", err)
	}
	slot, _ := st.GetSlot(ctx, "SL1")
	if slot.OccupiedCount != model.SlotCapacity {
		t.Fatalf("occupied_count 不得超过容量，实际 %d", slot.OccupiedCount)
	}
}

func TestService_ConcurrentLastSlotExactlyOneSucceeds(t *testing.T) {
	ctx := context.Background()
	svc, st := newService(t)
	if err := svc.PublishSlot(ctx, serviceSlot("SL1")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := svc.CreateAppointment(ctx, "U1", fmt.Sprintf("P%d", i), "SL1"); err != nil {
			t.Fatal(err)
		}
	}

	const racers = 20
	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := svc.CreateAppointment(ctx, "U1", fmt.Sprintf("RP%d", i), "SL1")
			if err == nil {
				mu.Lock()
				success++
				mu.Unlock()
			} else if !errors.Is(err, store.ErrSlotFull) {
				t.Errorf("并发抢名额只应报名额不足，实际: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if success != 1 {
		t.Fatalf("最后 1 个名额应恰好 1 人成功（SC-02），实际 %d", success)
	}
	slot, _ := st.GetSlot(ctx, "SL1")
	if slot.OccupiedCount != model.SlotCapacity {
		t.Fatalf("并发后不得超卖，occupied_count = %d", slot.OccupiedCount)
	}
}

func TestService_CreateAppointmentSetsHoldDeadline(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	if err := svc.PublishSlot(ctx, serviceSlot("SL1")); err != nil {
		t.Fatal(err)
	}
	appt, err := svc.CreateAppointment(ctx, "U1", "P1", "SL1")
	if err != nil {
		t.Fatal(err)
	}
	if appt.Status != model.StatusPendingPay {
		t.Fatalf("新建预约应为待支付占位，实际 %s", appt.Status)
	}
	if appt.AmountFen != model.AmountFen {
		t.Fatalf("金额必须为 9900 分，实际 %d", appt.AmountFen)
	}
	if want := fixedNow.Add(model.HoldDuration); !appt.PayDeadlineAt.Equal(want) {
		t.Fatalf("5 分钟占位截止时间 = %s, want %s", appt.PayDeadlineAt, want)
	}
}
