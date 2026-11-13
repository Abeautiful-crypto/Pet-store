package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Abeautiful-crypto/Pet-store/internal/model"
)

// 零外部依赖的 SQL driver：记录事务内的 SQL 与 RowsAffected，验证 SQL 层契约（T4）。
var fakeDriverSeq atomic.Int64

type fakeDriver struct{ conn *fakeConn }

func (d *fakeDriver) Open(string) (driver.Conn, error) { return d.conn, nil }

type fakeResult struct{ rows int64 }

func (r fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (r fakeResult) RowsAffected() (int64, error) { return r.rows, nil }

type fakeRows struct{}

func (r *fakeRows) Columns() []string         { return []string{"id"} }
func (r *fakeRows) Close() error              { return nil }
func (r *fakeRows) Next([]driver.Value) error { return io.EOF }

type fakeTx struct{ conn *fakeConn }

func (tx *fakeTx) Commit() error {
	tx.conn.mu.Lock()
	defer tx.conn.mu.Unlock()
	tx.conn.committed = true
	return nil
}

func (tx *fakeTx) Rollback() error {
	tx.conn.mu.Lock()
	defer tx.conn.mu.Unlock()
	tx.conn.rolledBack = true
	return nil
}

type fakeConn struct {
	mu         sync.Mutex
	queries    []string
	results    map[string]int64 // SQL 片段 -> RowsAffected
	failSQL    map[string]error // SQL 片段 -> 执行错误
	committed  bool
	rolledBack bool
}

func (c *fakeConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("fake driver 不使用 Prepare")
}
func (c *fakeConn) Close() error { return nil }
func (c *fakeConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}
func (c *fakeConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return &fakeTx{conn: c}, nil
}
func (c *fakeConn) Ping(context.Context) error { return nil }

func (c *fakeConn) resultFor(query string) (driver.Result, error) {
	for fragment, err := range c.failSQL {
		if strings.Contains(query, fragment) {
			return nil, err
		}
	}
	for fragment, rows := range c.results {
		if strings.Contains(query, fragment) {
			return fakeResult{rows: rows}, nil
		}
	}
	return fakeResult{rows: 1}, nil
}

func (c *fakeConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queries = append(c.queries, query)
	return c.resultFor(query)
}

func (c *fakeConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queries = append(c.queries, query)
	for fragment, err := range c.failSQL {
		if strings.Contains(query, fragment) {
			return nil, err
		}
	}
	return &fakeRows{}, nil
}

func (c *fakeConn) has(query string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, q := range c.queries {
		if strings.Contains(q, query) {
			return true
		}
	}
	return false
}

func newFakeDB(t *testing.T, conn *fakeConn) *sql.DB {
	t.Helper()
	name := fmt.Sprintf("fake_store_%d", fakeDriverSeq.Add(1))
	sql.Register(name, &fakeDriver{conn: conn})
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func mysqlTestAppointment() *model.Appointment {
	return model.NewAppointment("A1", model.StoreID, "SL1", "U1", "P1",
		time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC))
}

func TestMySQL_OccupyUsesConditionalUpdate(t *testing.T) {
	if !strings.Contains(SQLOccupySlot, "occupied_count < capacity") {
		t.Fatal("名额占用必须是原子条件更新 WHERE occupied_count < capacity")
	}
	if !strings.Contains(SQLOccupySlot, "occupied_count = occupied_count + 1") {
		t.Fatal("名额占用必须自增 occupied_count")
	}
	if !strings.Contains(SQLReleaseSlot, "slot_released = 0") {
		t.Fatal("名额释放必须带一次性守卫 slot_released = 0")
	}
}

func TestMySQL_CreateAppointmentCommitsTransaction(t *testing.T) {
	conn := &fakeConn{}
	s := NewMySQLStore(newFakeDB(t, conn))
	if err := s.CreateAppointment(context.Background(), mysqlTestAppointment()); err != nil {
		t.Fatalf("合法创建应成功: %v", err)
	}
	for _, q := range []string{SQLOccupySlot, SQLInsertAppointment, SQLInsertEvent} {
		if !conn.has(q) {
			t.Fatalf("事务内缺少 SQL: %s", q)
		}
	}
	conn.mu.Lock()
	defer conn.mu.Unlock()
	if !conn.committed || conn.rolledBack {
		t.Fatalf("成功事务必须 Commit 且不 Rollback: committed=%v rolledBack=%v", conn.committed, conn.rolledBack)
	}
}

func TestMySQL_FullSlotRollsBack(t *testing.T) {
	conn := &fakeConn{results: map[string]int64{SQLOccupySlot: 0}}
	s := NewMySQLStore(newFakeDB(t, conn))
	if err := s.CreateAppointment(context.Background(), mysqlTestAppointment()); !errors.Is(err, ErrSlotFull) {
		t.Fatalf("RowsAffected=0 应报 ErrSlotFull，实际: %v", err)
	}
	inserted := conn.has(SQLInsertAppointment)
	conn.mu.Lock()
	defer conn.mu.Unlock()
	if !conn.rolledBack || conn.committed {
		t.Fatalf("名额不足必须整体回滚: committed=%v rolledBack=%v", conn.committed, conn.rolledBack)
	}
	if inserted {
		t.Fatal("占位失败后不得继续写预约记录")
	}
}

func TestMySQL_DuplicateActiveAppointmentRollsBack(t *testing.T) {
	conn := &fakeConn{failSQL: map[string]error{SQLInsertAppointment: ErrDuplicateActive}}
	s := NewMySQLStore(newFakeDB(t, conn))
	if err := s.CreateAppointment(context.Background(), mysqlTestAppointment()); !errors.Is(err, ErrDuplicateActive) {
		t.Fatalf("唯一索引冲突应报 ErrDuplicateActive，实际: %v", err)
	}
	conn.mu.Lock()
	defer conn.mu.Unlock()
	if !conn.rolledBack || conn.committed {
		t.Fatal("唯一索引冲突必须整体回滚，名额不得被占用")
	}
}

func TestMySQL_EventFailureRollsBackWholeTransaction(t *testing.T) {
	conn := &fakeConn{failSQL: map[string]error{SQLInsertEvent: errors.New("event log down")}}
	s := NewMySQLStore(newFakeDB(t, conn))
	if err := s.CreateAppointment(context.Background(), mysqlTestAppointment()); err == nil {
		t.Fatal("事件流水写入失败应显式报错")
	}
	conn.mu.Lock()
	defer conn.mu.Unlock()
	if !conn.rolledBack || conn.committed {
		t.Fatal("跨表事务中任一失败必须整体回滚，不得提交")
	}
}

func TestMySQL_TransitionUsesVersionCAS(t *testing.T) {
	if !strings.Contains(SQLTransition, "version = version + 1") || !strings.Contains(SQLTransition, "version = ?") {
		t.Fatal("状态迁移必须带乐观锁 version 条件更新")
	}
	conn := &fakeConn{results: map[string]int64{SQLTransition: 0}}
	s := NewMySQLStore(newFakeDB(t, conn))
	if _, _, err := s.Transition(context.Background(), "A1", model.StatusPendingPay, model.EventPaySuccess, 1); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("RowsAffected=0 应报 ErrVersionConflict，实际: %v", err)
	}
}

func TestMySQL_MigrationDDLEnforcesUniqueness(t *testing.T) {
	body, err := os.ReadFile("../../migrations/001_init.up.sql")
	if err != nil {
		t.Fatalf("缺少版本化迁移: %v", err)
	}
	ddl := strings.ToLower(string(body))
	for _, want := range []string{"bigint", "unique", "pet_id", "slot_id", "active", "occupied_count", "slot_released"} {
		if !strings.Contains(ddl, want) {
			t.Fatalf("DDL 缺少关键约束/字段: %s", want)
		}
	}
}

func TestMySQL_RealIntegrationSkippedWithoutDSN(t *testing.T) {
	if os.Getenv("PETSTORE_TEST_DSN") == "" {
		t.Skip("未设置 PETSTORE_TEST_DSN，跳过真实 MySQL 集成测试（unit-test.md T4）")
	}
	t.Log("真实 DSN 已配置：名额条件更新与唯一索引需在引入 MySQL 驱动后联调")
}

// ---- F2.2 MySQL 事务提交/回滚与缓存失效 ----

func TestMySQL_Cache_CommitInvalidatesSlot(t *testing.T) {
	conn := &fakeConn{}
	s := NewMySQLStore(newFakeDB(t, conn))
	inv := &recordingInvalidator{}
	s.SetInvalidator(inv)
	if err := s.CreateAppointment(context.Background(), mysqlTestAppointment()); err != nil {
		t.Fatal(err)
	}
	slots, all := inv.snapshot()
	if len(slots) != 1 || slots[0] != "SL1" {
		t.Fatalf("事务提交后应失效变化的 slot:SL1，实际 %v", slots)
	}
	if all != 0 {
		t.Fatal("单次提交不应失效全部缓存")
	}
}

func TestMySQL_Cache_RollbackDoesNotInvalidate(t *testing.T) {
	conn := &fakeConn{results: map[string]int64{SQLOccupySlot: 0}}
	s := NewMySQLStore(newFakeDB(t, conn))
	inv := &recordingInvalidator{}
	s.SetInvalidator(inv)
	if err := s.CreateAppointment(context.Background(), mysqlTestAppointment()); !errors.Is(err, ErrSlotFull) {
		t.Fatalf("名额不足应回滚: %v", err)
	}
	slots, all := inv.snapshot()
	if len(slots) != 0 || all != 0 {
		t.Fatalf("事务回滚不得误失效缓存: slots=%v all=%d", slots, all)
	}
}

func TestMySQL_Cache_ResetInvalidatesAll(t *testing.T) {
	conn := &fakeConn{}
	s := NewMySQLStore(newFakeDB(t, conn))
	inv := &recordingInvalidator{}
	s.SetInvalidator(inv)
	if err := s.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, all := inv.snapshot()
	if all != 1 {
		t.Fatalf("Reset 必须失效全部缓存，实际 all=%d", all)
	}
}
