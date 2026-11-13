package store

import (
	"context"
	"errors"
	"testing"

	"github.com/Abeautiful-crypto/Pet-store/internal/model"
)

// F5.1 覆盖率补测：只补测试，不改实现、不放宽断言。

func TestCoverage_MemoryStoreQueryAndErrorPaths(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	slot := testSlot("SL1")
	if err := s.CreateSlot(ctx, slot); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSlot(ctx, slot); err == nil {
		t.Fatal("重复时段应报错")
	}
	if err := s.CreateSlot(ctx, model.Slot{ID: "BAD", StoreID: "OTHER"}); err == nil {
		t.Fatal("非法时段应被校验拒绝")
	}

	slots, err := s.ListSlots(ctx)
	if err != nil || len(slots) != 1 {
		t.Fatalf("ListSlots = %d, err=%v", len(slots), err)
	}
	if err := s.CreateAppointment(ctx, testAppointment("A1", "P1")); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListAppointments(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListAppointments = %d, err=%v", len(list), err)
	}
	active, err := s.HasActiveAppointment(ctx, "P1", "SL1")
	if err != nil || !active {
		t.Fatalf("有效预约应存在: active=%v err=%v", active, err)
	}
	active, err = s.HasActiveAppointment(ctx, "PX", "SL1")
	if err != nil || active {
		t.Fatalf("无效应为 false: active=%v err=%v", active, err)
	}
	if err := s.AppendEvent(ctx, model.EventLog{ID: "E1"}); err != nil {
		t.Fatal(err)
	}
	events, err := s.ListEvents(ctx)
	if err != nil || len(events) != 1 {
		t.Fatalf("ListEvents = %d, err=%v", len(events), err)
	}

	if _, err := s.GetSlot(ctx, "NOPE"); !errors.Is(err, ErrSlotNotFound) {
		t.Fatalf("GetSlot 缺时段: %v", err)
	}
	if _, err := s.GetAppointment(ctx, "NOPE"); !errors.Is(err, ErrAppointmentNotFound) {
		t.Fatalf("GetAppointment 缺记录: %v", err)
	}
	if _, _, err := s.Transition(ctx, "NOPE", model.StatusPendingPay, model.EventPaySuccess, 1); !errors.Is(err, ErrAppointmentNotFound) {
		t.Fatalf("Transition 缺记录: %v", err)
	}
	if _, _, err := s.Transition(ctx, "A1", model.StatusPendingPay, model.EventVerify, 1); !errors.Is(err, model.ErrIllegalTransition) {
		t.Fatalf("非法迁移: %v", err)
	}
	if err := s.ReleaseSlot(ctx, "NOPE"); !errors.Is(err, ErrAppointmentNotFound) {
		t.Fatalf("ReleaseSlot 缺记录: %v", err)
	}
	if err := s.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	list, _ = s.ListAppointments(ctx)
	if len(list) != 0 {
		t.Fatalf("Reset 后应清空, got %d", len(list))
	}
}

func TestCoverage_MemoryStoreTransitionTerminal(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	if err := s.CreateSlot(ctx, testSlot("SL1")); err != nil {
		t.Fatal(err)
	}
	appt := testAppointment("A1", "P1")
	if err := s.CreateAppointment(ctx, appt); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Transition(ctx, "A1", model.StatusPendingPay, model.EventHoldTimeout, 1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Transition(ctx, "A1", model.StatusClosed, model.EventPaySuccess, 2); !errors.Is(err, model.ErrTerminalStatus) {
		t.Fatalf("终态拒绝: %v", err)
	}
	if err := s.CreateAppointment(ctx, nil); err == nil {
		t.Fatal("nil 预约应被拒绝")
	}
	bad := testAppointment("A2", "P2")
	bad.AmountFen = 1
	if err := s.CreateAppointment(ctx, bad); err == nil {
		t.Fatal("非法金额应被拒绝")
	}
}

func TestCoverage_MySQLQueryAndErrorPaths(t *testing.T) {
	ctx := context.Background()
	conn := &fakeConn{}
	s := NewMySQLStore(newFakeDB(t, conn))

	if err := s.CreateSlot(ctx, model.Slot{ID: "BAD", StoreID: "OTHER"}); err == nil {
		t.Fatal("非法时段应被拒绝")
	}
	if err := s.CreateSlot(ctx, testSlot("SL1")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSlot(ctx, "SL1"); !errors.Is(err, ErrSlotNotFound) {
		t.Fatalf("空结果应映射为 ErrSlotNotFound: %v", err)
	}
	if _, err := s.GetAppointment(ctx, "A1"); !errors.Is(err, ErrAppointmentNotFound) {
		t.Fatalf("空结果应映射为 ErrAppointmentNotFound: %v", err)
	}
	if _, err := s.HasActiveAppointment(ctx, "P1", "SL1"); err == nil {
		t.Fatal("空结果应显式报错，不得静默吞错")
	}
	if list, err := s.ListAppointments(ctx); err != nil || len(list) != 0 {
		t.Fatalf("ListAppointments: %d %v", len(list), err)
	}
	if list, err := s.ListSlots(ctx); err != nil || len(list) != 0 {
		t.Fatalf("ListSlots: %d %v", len(list), err)
	}
	if events, err := s.ListEvents(ctx); err != nil || len(events) != 0 {
		t.Fatalf("ListEvents: %d %v", len(events), err)
	}
	if err := s.AppendEvent(ctx, model.EventLog{ID: "E1"}); err != nil {
		t.Fatal(err)
	}
	if to, version, err := s.Transition(ctx, "A1", model.StatusPendingPay, model.EventPaySuccess, 1); err != nil || to != model.StatusConfirmed || version != 2 {
		t.Fatalf("Transition 成功路径: %s %d %v", to, version, err)
	}
	if _, _, err := s.Transition(ctx, "A1", model.StatusPendingPay, model.EventVerify, 1); !errors.Is(err, model.ErrIllegalTransition) {
		t.Fatalf("非法迁移: %v", err)
	}
	if err := s.ReleaseSlot(ctx, "A1"); err != nil {
		t.Fatal(err)
	}
	conn.results = map[string]int64{SQLReleaseSlot: 0}
	if err := s.ReleaseSlot(ctx, "A1"); !errors.Is(err, ErrAlreadyReleased) {
		t.Fatalf("重复释放: %v", err)
	}
	if err := s.CreateAppointment(ctx, nil); err == nil {
		t.Fatal("nil 预约应被拒绝")
	}
	bad := mysqlTestAppointment()
	bad.AmountFen = 1
	if err := s.CreateAppointment(ctx, bad); err == nil {
		t.Fatal("非法金额应被拒绝")
	}
}

func TestCoverage_MySQLExecFailures(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		sql    string
		invoke func(*MySQLStore) error
	}{
		{"CreateSlot", SQLInsertSlot, func(s *MySQLStore) error { return s.CreateSlot(ctx, testSlot("SL1")) }},
		{"CreateAppointment", SQLOccupySlot, func(s *MySQLStore) error { return s.CreateAppointment(ctx, mysqlTestAppointment()) }},
		{"Transition", SQLTransition, func(s *MySQLStore) error {
			_, _, err := s.Transition(ctx, "A1", model.StatusPendingPay, model.EventPaySuccess, 1)
			return err
		}},
		{"ReleaseSlot", SQLReleaseSlot, func(s *MySQLStore) error { return s.ReleaseSlot(ctx, "A1") }},
		{"AppendEvent", SQLInsertEvent, func(s *MySQLStore) error { return s.AppendEvent(ctx, model.EventLog{ID: "E1"}) }},
		{"Reset", SQLResetEvents, func(s *MySQLStore) error { return s.Reset(ctx) }},
		{"ListSlots", SQLSelectSlots, func(s *MySQLStore) error { _, err := s.ListSlots(ctx); return err }},
		{"ListAppointments", SQLSelectAppointments, func(s *MySQLStore) error { _, err := s.ListAppointments(ctx); return err }},
		{"ListEvents", SQLSelectEvents, func(s *MySQLStore) error { _, err := s.ListEvents(ctx); return err }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn := &fakeConn{failSQL: map[string]error{tc.sql: errors.New("db down")}}
			s := NewMySQLStore(newFakeDB(t, conn))
			if err := tc.invoke(s); err == nil {
				t.Fatal("数据库错误必须显式返回，不得静默吞错")
			}
		})
	}
}
