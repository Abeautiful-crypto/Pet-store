package service

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Abeautiful-crypto/Pet-store/internal/model"
	"github.com/Abeautiful-crypto/Pet-store/internal/store"
	_ "github.com/go-sql-driver/mysql"
)

// ledgerTestDB 打开真实 MySQL（PETSTORE_TEST_DSN），执行全部迁移并保证账本相关表为空。
func ledgerTestDB(t *testing.T) *sql.DB {
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

	// 执行全部版本化迁移，保证 schema 与本地一致（DDL 均 IF NOT EXISTS，幂等）。
	files, _ := filepath.Glob(filepath.Join("..", "..", "migrations", "*.up.sql"))
	for _, f := range files {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var lines []string
		for _, line := range strings.Split(string(body), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "--") {
				continue
			}
			lines = append(lines, line)
		}
		for _, stmt := range strings.Split(strings.Join(lines, "\n"), ";") {
			if strings.TrimSpace(stmt) == "" {
				continue
			}
			if _, err := db.Exec(stmt); err != nil {
				t.Fatalf("迁移 %s 失败: %v", filepath.Base(f), err)
			}
		}
	}
	for _, table := range []string{
		"event_logs", "service_records", "refunds", "refund_events",
		"cancel_requests", "payments", "appointments", "slots", "exceptions",
	} {
		if _, err := db.Exec("DELETE FROM " + table); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func ptrTime(t time.Time) *time.Time { return &t }

// TestMySQLLedger_PaymentLifecycle 验证支付单落库、notify_key 幂等、反查与列表。
func TestMySQLLedger_PaymentLifecycle(t *testing.T) {
	db := ledgerTestDB(t)
	l := NewMySQLLedger(db)
	ctx := context.Background()

	p := model.Payment{
		ID: "PAY-1", AppointmentID: "APPT-1", AmountFen: model.AmountFen,
		ChannelTradeNo: "T1", NotifyKey: "N1", Status: model.PaymentSucceeded,
	}
	if !l.SavePayment(p) {
		t.Fatal("首次保存应成功")
	}
	// 相同 notify_key（即便 payment_id 不同）应命中幂等，返回 false。
	dup := p
	dup.ID = "PAY-2"
	if l.SavePayment(dup) {
		t.Fatal("相同 notify_key 应幂等返回 false")
	}
	got, ok := l.PaymentByNotify("N1")
	if !ok || got.AppointmentID != "APPT-1" {
		t.Fatalf("PaymentByNotify 失败: %+v ok=%v", got, ok)
	}
	got2, ok := l.PaymentOf("APPT-1")
	if !ok || got2.NotifyKey != "N1" {
		t.Fatalf("PaymentOf 失败: %+v ok=%v", got2, ok)
	}
	if len(l.Payments()) != 1 {
		t.Fatalf("Payments 应仅 1 条，实际 %d", len(l.Payments()))
	}
	_ = ctx
}

// TestMySQLLedger_RefundCancelService 验证退款/取消/服务记录 upsert 与异常、Reset 清空。
func TestMySQLLedger_RefundCancelService(t *testing.T) {
	db := ledgerTestDB(t)
	l := NewMySQLLedger(db)

	now := time.Now()
	r := model.Refund{
		ID: "RFD-1", AppointmentID: "APPT-2", PaymentID: "PAY-X", AmountFen: model.AmountFen,
		Status: model.RefundSucceeded, CreatedAt: now, FinishedAt: ptrTime(now),
	}
	l.SaveRefund(r)
	l.AppendRefundEvent(r)
	// 同一 refund_id 重试：状态从成功变失败，应 upsert 为同一条。
	r.Status = model.RefundFailed
	r.RetryCount = 1
	r.LastError = "模拟渠道退款失败"
	r.FinishedAt = nil
	l.SaveRefund(r)
	l.AppendRefundEvent(r)

	got, ok := l.RefundOf("APPT-2")
	if !ok || got.Status != model.RefundFailed || got.RetryCount != 1 {
		t.Fatalf("RefundOf 失败: %+v ok=%v", got, ok)
	}
	if len(l.Refunds()) != 1 {
		t.Fatalf("Refunds 应 1 条（upsert），实际 %d", len(l.Refunds()))
	}
	if len(l.RefundEvents()) != 2 {
		t.Fatalf("RefundEvents 应 2 条（失败→重试状态链），实际 %d", len(l.RefundEvents()))
	}

	c := model.CancelRequest{
		ID: "CR-1", AppointmentID: "APPT-2", Reason: "x", ApplyAt: now,
		HoursToStart: 3, Type: "自动受理", Status: model.CancelRequestApproved,
		Reviewer: "system", ReviewedAt: ptrTime(now),
	}
	l.SaveCancelRequest(c)
	if gotC, ok := l.CancelRequestByID("CR-1"); !ok || gotC.AppointmentID != "APPT-2" {
		t.Fatalf("CancelRequestByID 失败: %+v ok=%v", gotC, ok)
	}
	if gotC, ok := l.CancelRequestOf("APPT-2"); !ok || gotC.ID != "CR-1" {
		t.Fatalf("CancelRequestOf 失败: %+v ok=%v", gotC, ok)
	}

	sr := model.ServiceRecord{
		ID: "SR-1", AppointmentID: "APPT-2", VerifyBy: "F1",
		VerifyAt: ptrTime(now), RevenueFen: model.ServiceRevenueFen,
	}
	l.SaveServiceRecord(sr)
	if gotSR, ok := l.ServiceRecordOf("APPT-2"); !ok || gotSR.VerifyBy != "F1" || gotSR.RevenueFen != model.ServiceRevenueFen {
		t.Fatalf("ServiceRecordOf 失败: %+v ok=%v", gotSR, ok)
	}
	sr.StartedAt = ptrTime(now)
	l.SaveServiceRecord(sr)
	if gotSR, _ := l.ServiceRecordOf("APPT-2"); gotSR.StartedAt == nil {
		t.Fatal("ServiceRecord upsert 应保留 StartedAt")
	}
	if len(l.ServiceRecords()) != 1 {
		t.Fatalf("ServiceRecords 应 1 条，实际 %d", len(l.ServiceRecords()))
	}

	l.AppendException(model.EventLog{
		ID: "EVT-X", AppointmentID: "APPT-2", Action: "late_pay_callback",
		Detail: "关闭后迟到回调", CreatedAt: now,
	})
	if len(l.Exceptions()) != 1 {
		t.Fatalf("Exceptions 应 1 条，实际 %d", len(l.Exceptions()))
	}

	l.Reset()
	if n := len(l.Payments()) + len(l.Refunds()) + len(l.ServiceRecords()) + len(l.Exceptions()); n != 0 {
		t.Fatalf("Reset 后应清空所有单据，剩余 %d", n)
	}
}

// TestMySQLLedger_ServiceEndToEnd 端到端：MySQLStore + MySQLLedger 同库，
// 走完整「预约→支付确认」并断言支付单真落 MySQL、日清与快照可反查。
func TestMySQLLedger_ServiceEndToEnd(t *testing.T) {
	db := ledgerTestDB(t)
	ctx := context.Background()

	svc := New(Config{
		Store:   store.NewMySQLStore(db),
		Now:     time.Now,
		Ledger:  NewMySQLLedger(db),
		OwnsPet: func(string, string) bool { return true },
	})

	start := time.Now().Add(4 * time.Hour)
	slot := model.Slot{
		ID: "SL-E2E", StoreID: model.StoreID, ServiceItemID: model.ServiceItemID,
		StartAt: start, EndAt: start.Add(time.Hour), Capacity: model.SlotCapacity,
	}
	if err := svc.PublishSlot(ctx, slot); err != nil {
		t.Fatal(err)
	}
	appt, err := svc.CreateAppointment(ctx, "U1", "P1", slot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ConfirmPayment(ctx, appt.ID, "N-E2E", "T-E2E"); err != nil {
		t.Fatal(err)
	}

	// 真相源：支付单已落入 MySQL。
	if p, ok := svc.ledger.PaymentOf(appt.ID); !ok || p.NotifyKey != "N-E2E" {
		t.Fatalf("支付单未落入 MySQL: %+v ok=%v", p, ok)
	}
	// 快照可反查支付单与已确认状态。
	view, err := svc.Snapshot(ctx, appt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Appointment.Status != model.StatusConfirmed {
		t.Fatalf("应为已确认，实际 %s", view.Appointment.Status)
	}
	if view.Payment == nil || view.Payment.NotifyKey != "N-E2E" {
		t.Fatal("快照应回带支付单")
	}
	// 日清金额三分类：已收款 = 9900。
	set, err := svc.DailySettlement(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if set.CollectedFen != model.AmountFen {
		t.Fatalf("已收款应为 %d，实际 %d", model.AmountFen, set.CollectedFen)
	}
}
