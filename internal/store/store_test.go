package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Abeautiful-crypto/Pet-store/internal/model"
)

func testSlot(id string) model.Slot {
	start := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	return model.Slot{
		ID:            id,
		StoreID:       model.StoreID,
		ServiceItemID: model.ServiceItemID,
		StartAt:       start,
		EndAt:         start.Add(time.Hour),
		Capacity:      model.SlotCapacity,
	}
}

func testAppointment(id, petID string) *model.Appointment {
	return model.NewAppointment(id, model.StoreID, "SL1", "U1", petID,
		time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC))
}

func newTestStore(t *testing.T) *MemoryStore {
	t.Helper()
	ctx := context.Background()
	s := NewMemoryStore()
	if err := s.CreateSlot(ctx, testSlot("SL1")); err != nil {
		t.Fatalf("创建时段失败: %v", err)
	}
	return s
}

func TestStore_ConcurrentLastSlotOnlyOneSucceeds(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	for i := 0; i < 2; i++ {
		if err := s.CreateAppointment(ctx, testAppointment(fmt.Sprintf("A%d", i), fmt.Sprintf("P%d", i))); err != nil {
			t.Fatalf("预占名额失败: %v", err)
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
			err := s.CreateAppointment(ctx, testAppointment(fmt.Sprintf("R%d", i), fmt.Sprintf("RP%d", i)))
			if err == nil {
				mu.Lock()
				success++
				mu.Unlock()
			} else if !errors.Is(err, ErrSlotFull) {
				t.Errorf("并发抢名额应只报名额不足，实际: %v", err)
			}
		}(i)
	}
	wg.Wait()

	if success != 1 {
		t.Fatalf("最后 1 个名额应恰好 1 人成功，实际 %d 人", success)
	}
	slot, err := s.GetSlot(ctx, "SL1")
	if err != nil {
		t.Fatal(err)
	}
	if slot.OccupiedCount != model.SlotCapacity {
		t.Fatalf("occupied_count = %d，不得超过容量 %d", slot.OccupiedCount, model.SlotCapacity)
	}
}

func TestStore_DuplicateActiveAppointmentRejected(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if err := s.CreateAppointment(ctx, testAppointment("A1", "P1")); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateAppointment(ctx, testAppointment("A2", "P1")); !errors.Is(err, ErrDuplicateActive) {
		t.Fatalf("同一宠物同一时段重复预约应报 ErrDuplicateActive，实际: %v", err)
	}
	slot, _ := s.GetSlot(ctx, "SL1")
	if slot.OccupiedCount != 1 {
		t.Fatalf("重复预约不得重复占名额，occupied_count = %d", slot.OccupiedCount)
	}
}

func TestStore_TransitionCASOnlyOneWinner(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if err := s.CreateAppointment(ctx, testAppointment("A1", "P1")); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	won := make(chan struct{}, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := s.Transition(ctx, "A1", model.StatusPendingPay, model.EventPaySuccess, 1)
			if err == nil {
				won <- struct{}{}
			} else if !errors.Is(err, ErrVersionConflict) {
				t.Errorf("CAS 失败应报 ErrVersionConflict，实际: %v", err)
			}
		}()
	}
	wg.Wait()
	if len(won) != 1 {
		t.Fatalf("同一 version 并发迁移应只有 1 个胜者，实际 %d", len(won))
	}
	appt, _ := s.GetAppointment(ctx, "A1")
	if appt.Status != model.StatusConfirmed || appt.Version != 2 {
		t.Fatalf("迁移后状态/版本错误: status=%s version=%d", appt.Status, appt.Version)
	}
}

func TestStore_ReleaseSlotExactlyOnce(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if err := s.CreateAppointment(ctx, testAppointment("A1", "P1")); err != nil {
		t.Fatal(err)
	}
	if err := s.ReleaseSlot(ctx, "A1"); err != nil {
		t.Fatalf("首次释放应成功: %v", err)
	}
	slot, _ := s.GetSlot(ctx, "SL1")
	if slot.OccupiedCount != 0 {
		t.Fatalf("释放后名额应为 0，实际 %d", slot.OccupiedCount)
	}
	if err := s.ReleaseSlot(ctx, "A1"); !errors.Is(err, ErrAlreadyReleased) {
		t.Fatalf("重复释放应报 ErrAlreadyReleased，实际: %v", err)
	}
	slot, _ = s.GetSlot(ctx, "SL1")
	if slot.OccupiedCount != 0 {
		t.Fatalf("重复释放不得二次扣减名额，occupied_count = %d", slot.OccupiedCount)
	}
}

func TestStore_FailedCreateDoesNotChangeState(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	// 名额未满时验证唯一性冲突：失败事务不得占名额、不得写记录。
	if err := s.CreateAppointment(ctx, testAppointment("A0", "P0")); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateAppointment(ctx, testAppointment("A5", "P0")); !errors.Is(err, ErrDuplicateActive) {
		t.Fatalf("唯一性冲突应报 ErrDuplicateActive，实际: %v", err)
	}
	slot, _ := s.GetSlot(ctx, "SL1")
	if slot.OccupiedCount != 1 {
		t.Fatalf("唯一性冲突不得占名额，occupied_count = %d", slot.OccupiedCount)
	}
	for i := 1; i < model.SlotCapacity; i++ {
		if err := s.CreateAppointment(ctx, testAppointment(fmt.Sprintf("A%d", i), fmt.Sprintf("P%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.CreateAppointment(ctx, testAppointment("A4", "P4")); !errors.Is(err, ErrSlotFull) {
		t.Fatalf("第 4 个预约应报 ErrSlotFull，实际: %v", err)
	}
	missing := testAppointment("A6", "P6")
	missing.SlotID = "NOT-EXIST"
	if err := s.CreateAppointment(ctx, missing); !errors.Is(err, ErrSlotNotFound) {
		t.Fatalf("时段不存在应报 ErrSlotNotFound，实际: %v", err)
	}
	slot, _ = s.GetSlot(ctx, "SL1")
	if slot.OccupiedCount != model.SlotCapacity {
		t.Fatalf("失败事务不得污染名额，occupied_count = %d", slot.OccupiedCount)
	}
	if _, err := s.GetAppointment(ctx, "A4"); !errors.Is(err, ErrAppointmentNotFound) {
		t.Fatalf("失败事务不得写入预约记录，实际: %v", err)
	}
}

func TestStore_AppendEventAndReset(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if err := s.CreateAppointment(ctx, testAppointment("A1", "P1")); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendEvent(ctx, model.EventLog{ID: "E1", AppointmentID: "A1", Action: "create"}); err != nil {
		t.Fatal(err)
	}
	logs, err := s.ListEvents(ctx)
	if err != nil || len(logs) != 1 {
		t.Fatalf("事件流水应可查，logs=%d err=%v", len(logs), err)
	}
	if err := s.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSlot(ctx, "SL1"); !errors.Is(err, ErrSlotNotFound) {
		t.Fatalf("Reset 后时段应清空，实际: %v", err)
	}
	logs, _ = s.ListEvents(ctx)
	if len(logs) != 0 {
		t.Fatalf("Reset 后事件流水应清空，实际 %d 条", len(logs))
	}
}

// ---- F2.2 缓存写失效（tech-stack.md §2.2：写提交后失效，回滚不误失效）----

type recordingInvalidator struct {
	mu    sync.Mutex
	slots []string
	all   int
}

func (r *recordingInvalidator) InvalidateSlot(_ context.Context, slotID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.slots = append(r.slots, slotID)
	return nil
}

func (r *recordingInvalidator) InvalidateAll(_ context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.all++
	return nil
}

func (r *recordingInvalidator) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.slots = nil
	r.all = 0
}

func (r *recordingInvalidator) snapshot() ([]string, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.slots...), r.all
}

func TestStore_Cache_WriteCommitInvalidatesSlot(t *testing.T) {
	ctx := context.Background()
	inv := &recordingInvalidator{}
	s := newTestStore(t)
	s.SetInvalidator(inv)
	inv.reset()

	if err := s.CreateAppointment(ctx, testAppointment("A1", "P1")); err != nil {
		t.Fatal(err)
	}
	slots, all := inv.snapshot()
	if len(slots) != 1 || slots[0] != "SL1" {
		t.Fatalf("创建预约提交后应失效 slot:SL1，实际 %v", slots)
	}
	if all != 0 {
		t.Fatal("单次写提交不应失效全部缓存")
	}

	inv.reset()
	if err := s.ReleaseSlot(ctx, "A1"); err != nil {
		t.Fatal(err)
	}
	slots, _ = inv.snapshot()
	if len(slots) != 1 || slots[0] != "SL1" {
		t.Fatalf("名额释放提交后应失效 slot:SL1，实际 %v", slots)
	}
}

func TestStore_Cache_RollbackDoesNotInvalidate(t *testing.T) {
	ctx := context.Background()
	inv := &recordingInvalidator{}
	s := newTestStore(t)
	s.SetInvalidator(inv)
	if err := s.CreateAppointment(ctx, testAppointment("A1", "P1")); err != nil {
		t.Fatal(err)
	}
	inv.reset()

	if err := s.CreateAppointment(ctx, testAppointment("A2", "P1")); !errors.Is(err, ErrDuplicateActive) {
		t.Fatalf("重复预约应失败: %v", err)
	}
	missing := testAppointment("A3", "P3")
	missing.SlotID = "NOPE"
	if err := s.CreateAppointment(ctx, missing); !errors.Is(err, ErrSlotNotFound) {
		t.Fatalf("时段不存在应失败: %v", err)
	}
	slots, all := inv.snapshot()
	if len(slots) != 0 || all != 0 {
		t.Fatalf("回滚/失败的写不得失效缓存: slots=%v all=%d", slots, all)
	}
}

func TestStore_Cache_ResetInvalidatesAll(t *testing.T) {
	ctx := context.Background()
	inv := &recordingInvalidator{}
	s := newTestStore(t)
	s.SetInvalidator(inv)
	if err := s.CreateAppointment(ctx, testAppointment("A1", "P1")); err != nil {
		t.Fatal(err)
	}
	inv.reset()
	if err := s.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	slots, all := inv.snapshot()
	if all != 1 {
		t.Fatalf("Reset 必须失效全部缓存，实际 all=%d", all)
	}
	if len(slots) != 0 {
		t.Fatalf("Reset 只需失效全部，不应逐个失效: %v", slots)
	}
}
