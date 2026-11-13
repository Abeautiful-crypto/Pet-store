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

// ---- F3.2 5 分钟占位与超时 / 主动取消释放（FR-04 / 规则 2）----

type mutableClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *mutableClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *mutableClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newServiceWithClock(t *testing.T, clock *mutableClock) (*Service, *store.MemoryStore) {
	t.Helper()
	st := store.NewMemoryStore()
	svc := New(Config{
		Store:   st,
		Now:     clock.Now,
		OwnsPet: func(string, string) bool { return true },
	})
	return svc, st
}

func TestService_ExpireHoldReleasesSlotOnce(t *testing.T) {
	ctx := context.Background()
	clock := &mutableClock{now: fixedNow}
	svc, st := newServiceWithClock(t, clock)
	if err := svc.PublishSlot(ctx, serviceSlot("SL1")); err != nil {
		t.Fatal(err)
	}
	appt, err := svc.CreateAppointment(ctx, "U1", "P1", "SL1")
	if err != nil {
		t.Fatal(err)
	}

	clock.Advance(model.HoldDuration - time.Second)
	n, err := svc.ExpireHolds(ctx)
	if err != nil || n != 0 {
		t.Fatalf("未到 5 分钟不得释放: n=%d err=%v", n, err)
	}
	slot, _ := st.GetSlot(ctx, "SL1")
	if slot.OccupiedCount != 1 {
		t.Fatalf("未到期名额应保持占用，occupied_count = %d", slot.OccupiedCount)
	}

	clock.Advance(2 * time.Second)
	n, err = svc.ExpireHolds(ctx)
	if err != nil || n != 1 {
		t.Fatalf("到点应恰好关闭 1 笔: n=%d err=%v", n, err)
	}
	got, _ := st.GetAppointment(ctx, appt.ID)
	if got.Status != model.StatusClosed {
		t.Fatalf("超时后状态应为已关闭，实际 %s", got.Status)
	}
	slot, _ = st.GetSlot(ctx, "SL1")
	if slot.OccupiedCount != 0 {
		t.Fatalf("超时后名额应精确释放一次，occupied_count = %d", slot.OccupiedCount)
	}

	n, err = svc.ExpireHolds(ctx)
	if err != nil || n != 0 {
		t.Fatalf("重复扫描不得重复释放: n=%d err=%v", n, err)
	}
	slot, _ = st.GetSlot(ctx, "SL1")
	if slot.OccupiedCount != 0 {
		t.Fatalf("重复扫描不得二次扣减名额，occupied_count = %d", slot.OccupiedCount)
	}
}

func TestService_ExpiredSlotCanBeRebooked(t *testing.T) {
	ctx := context.Background()
	clock := &mutableClock{now: fixedNow}
	svc, st := newServiceWithClock(t, clock)
	if err := svc.PublishSlot(ctx, serviceSlot("SL1")); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateAppointment(ctx, "U1", "P1", "SL1"); err != nil {
		t.Fatal(err)
	}
	clock.Advance(model.HoldDuration + time.Second)
	if n, err := svc.ExpireHolds(ctx); err != nil || n != 1 {
		t.Fatalf("超时释放失败: n=%d err=%v", n, err)
	}
	if _, err := svc.CreateAppointment(ctx, "U2", "P2", "SL1"); err != nil {
		t.Fatalf("释放后新用户应可预约（SC-02）: %v", err)
	}
	// 原预约已关闭，不再占唯一性；同一宠物可重新预约。
	if _, err := svc.CreateAppointment(ctx, "U1", "P1", "SL1"); err != nil {
		t.Fatalf("已关闭预约不应再占唯一性: %v", err)
	}
	slot, _ := st.GetSlot(ctx, "SL1")
	if slot.OccupiedCount != 2 {
		t.Fatalf("重新预约后名额应为 2，实际 %d", slot.OccupiedCount)
	}
}

func TestService_CancelUnpaidReleasesSlotOnce(t *testing.T) {
	ctx := context.Background()
	clock := &mutableClock{now: fixedNow}
	svc, st := newServiceWithClock(t, clock)
	if err := svc.PublishSlot(ctx, serviceSlot("SL1")); err != nil {
		t.Fatal(err)
	}
	appt, err := svc.CreateAppointment(ctx, "U1", "P1", "SL1")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.CancelUnpaid(ctx, appt.ID, "U1"); err != nil {
		t.Fatalf("主动取消应成功: %v", err)
	}
	got, _ := st.GetAppointment(ctx, appt.ID)
	if got.Status != model.StatusClosed {
		t.Fatalf("主动取消后应为已关闭，实际 %s", got.Status)
	}
	slot, _ := st.GetSlot(ctx, "SL1")
	if slot.OccupiedCount != 0 {
		t.Fatalf("主动取消应释放名额，occupied_count = %d", slot.OccupiedCount)
	}
	if err := svc.CancelUnpaid(ctx, appt.ID, "U1"); err == nil {
		t.Fatal("重复取消必须显式报错，不得静默吞错")
	}
	slot, _ = st.GetSlot(ctx, "SL1")
	if slot.OccupiedCount != 0 {
		t.Fatalf("重复取消不得二次扣减名额，occupied_count = %d", slot.OccupiedCount)
	}
}

func TestService_CancelUnpaidRejectsOtherUser(t *testing.T) {
	ctx := context.Background()
	clock := &mutableClock{now: fixedNow}
	svc, st := newServiceWithClock(t, clock)
	if err := svc.PublishSlot(ctx, serviceSlot("SL1")); err != nil {
		t.Fatal(err)
	}
	appt, err := svc.CreateAppointment(ctx, "U1", "P1", "SL1")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.CancelUnpaid(ctx, appt.ID, "U2"); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("他人不得取消该预约，实际: %v", err)
	}
	got, _ := st.GetAppointment(ctx, appt.ID)
	if got.Status != model.StatusPendingPay {
		t.Fatalf("越权取消不得改动记录，实际状态 %s", got.Status)
	}
	slot, _ := st.GetSlot(ctx, "SL1")
	if slot.OccupiedCount != 1 {
		t.Fatalf("越权取消不得释放名额，occupied_count = %d", slot.OccupiedCount)
	}
}
