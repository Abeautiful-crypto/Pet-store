package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Abeautiful-crypto/Pet-store/internal/model"
	_ "github.com/go-sql-driver/mysql"
)

// 真实 MySQL 集成测试：仅在设置 PETSTORE_TEST_DSN 时运行（unit-test.md T4）。
func realMySQL(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("PETSTORE_TEST_DSN")
	if dsn == "" {
		t.Skip("未设置 PETSTORE_TEST_DSN，跳过真实 MySQL 集成测试")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Ping(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	body, err := os.ReadFile(filepath.Join("..", "..", "migrations", "001_init.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	var scriptLines []string
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "--") {
			continue
		}
		scriptLines = append(scriptLines, line)
	}
	for _, statement := range strings.Split(strings.Join(scriptLines, "\n"), ";") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("建表失败: %v", err)
		}
	}
	for _, table := range []string{"event_logs", "service_records", "refunds",
		"cancel_requests", "payments", "appointments", "slots"} {
		if _, err := db.Exec("DELETE FROM " + table); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestMySQL_RealAtomicOccupyAndUniqueActive(t *testing.T) {
	db := realMySQL(t)
	s := NewMySQLStore(db)
	ctx := context.Background()
	start := time.Now().Add(4 * time.Hour)
	slot := model.Slot{
		ID: "SL-REAL", StoreID: model.StoreID, ServiceItemID: model.ServiceItemID,
		StartAt: start, EndAt: start.Add(time.Hour), Capacity: model.SlotCapacity,
	}
	if err := s.CreateSlot(ctx, slot); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		appt := model.NewAppointment(fmt.Sprintf("REAL-%d", i), model.StoreID,
			slot.ID, "U1", fmt.Sprintf("PET-%d", i), time.Now())
		if err := s.CreateAppointment(ctx, appt); err != nil {
			t.Fatal(err)
		}
	}

	const racers = 10
	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			appt := model.NewAppointment(fmt.Sprintf("RACE-%d", i), model.StoreID,
				slot.ID, "U1", fmt.Sprintf("RACE-PET-%d", i), time.Now())
			if err := s.CreateAppointment(ctx, appt); err == nil {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if success != 1 {
		t.Fatalf("真实 MySQL 上最后 1 个名额应恰好 1 人成功，实际 %d", success)
	}
	got, err := s.GetSlot(ctx, slot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.OccupiedCount != model.SlotCapacity {
		t.Fatalf("不得超卖，occupied_count = %d", got.OccupiedCount)
	}

	duplicate := model.NewAppointment("REAL-DUP", model.StoreID, slot.ID, "U1", "PET-0", time.Now())
	if err := s.CreateAppointment(ctx, duplicate); err == nil {
		t.Fatal("uk_pet_slot_active 唯一索引应拒绝同一宠物同一时段重复预约")
	}
	after, _ := s.GetSlot(ctx, slot.ID)
	if after.OccupiedCount != model.SlotCapacity {
		t.Fatalf("唯一索引冲突必须整体回滚，不得占名额: %d", after.OccupiedCount)
	}
}
