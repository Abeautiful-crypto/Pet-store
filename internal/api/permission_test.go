package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Abeautiful-crypto/Pet-store/internal/model"
	"github.com/Abeautiful-crypto/Pet-store/internal/service"
)

// ---- F4.2 前台 3 页与核销准入展示 ----

func TestAPI_VerifyRequiresStoreAndConditions(t *testing.T) {
	f := newFixture(t)
	f.publish(t, "SL1")

	// 未确认预约不得核销。
	if rec := f.do(t, http.MethodPost, "/api/appointments", RoleUser, "U1", model.StoreID,
		map[string]any{"pet_id": "P9", "slot_id": "SL1"}); rec.Code != http.StatusCreated {
		t.Fatalf("创建预约失败: %d", rec.Code)
	}
	var unpaid model.Appointment
	// 重新取回该预约 ID：通过时段列表无法直接取，改用新预约响应解析。
	rec := f.do(t, http.MethodPost, "/api/appointments", RoleUser, "U1", model.StoreID,
		map[string]any{"pet_id": "P8", "slot_id": "SL1"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("创建第二笔预约失败: %d", rec.Code)
	}
	if err := json.NewDecoder(rec.Body).Decode(&unpaid); err != nil {
		t.Fatal(err)
	}
	if got := f.do(t, http.MethodPost, "/api/appointments/"+unpaid.ID+"/verify", RoleFrontDesk, "FD1", model.StoreID, map[string]any{}).Code; got != http.StatusConflict {
		t.Fatalf("未确认预约核销应 409，实际 %d", got)
	}

	// 非本店员工核销被拦截。
	paid := f.createPaid(t, "SL1", "P1", "N1")
	if got := f.do(t, http.MethodPost, "/api/appointments/"+paid+"/verify", RoleFrontDesk, "FD1", "OTHER-STORE", map[string]any{}).Code; got != http.StatusForbidden {
		t.Fatalf("非本店员工核销应 403，实际 %d", got)
	}

	// 取消审核中禁止核销。
	f.clock.Advance(3 * time.Hour)
	if got := f.do(t, http.MethodPost, "/api/appointments/"+paid+"/cancel", RoleUser, "U1", model.StoreID,
		map[string]any{"reason": "临时有事"}).Code; got != http.StatusOK {
		t.Fatalf("申请取消失败: %d", got)
	}
	if got := f.do(t, http.MethodPost, "/api/appointments/"+paid+"/verify", RoleFrontDesk, "FD1", model.StoreID, map[string]any{}).Code; got != http.StatusConflict {
		t.Fatalf("待审核期间核销应被拒，实际 %d", got)
	}
}

func TestAPI_WrongStoreDoesNotChangeRecord(t *testing.T) {
	f := newFixture(t)
	f.publish(t, "SL1")
	id := f.createPaid(t, "SL1", "P1", "N1")

	if got := f.do(t, http.MethodPost, "/api/appointments/"+id+"/verify", RoleFrontDesk, "FD1", "OTHER-STORE", map[string]any{}).Code; got != http.StatusForbidden {
		t.Fatalf("非本店操作应 403，实际 %d", got)
	}
	view := f.view(t, id)
	if view.Appointment.Status != model.StatusConfirmed || view.ServiceRecord != nil {
		t.Fatalf("被拦截操作不得改动记录: %+v", view)
	}
	rec := f.do(t, http.MethodGet, "/api/slots", RoleManager, "M1", model.StoreID, nil)
	var payload struct {
		Slots []model.Slot `json:"slots"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Slots) != 1 || payload.Slots[0].OccupiedCount != 1 {
		t.Fatalf("被拦截操作不得改动名额: %+v", payload.Slots)
	}
}

// ---- F4.5 渠道控制台与越权演示 ----

func TestAPI_ChannelConsoleConfiguresOutcomes(t *testing.T) {
	f := newFixture(t)
	f.publish(t, "SL1")
	f.publish(t, "SL2")

	// 重复支付通知：同一 notify_key 二次到达，状态不变。
	id := f.createPaid(t, "SL1", "P1", "N1")
	if got := f.do(t, http.MethodPost, "/api/appointments/"+id+"/pay", RoleChannel, "CH1", model.StoreID,
		map[string]any{"notify_key": "N1", "channel_trade_no": "T-N1"}).Code; got != http.StatusOK {
		t.Fatalf("重复通知应幂等命中，实际 %d", got)
	}
	if got := f.view(t, id).Appointment.Status; got != model.StatusConfirmed {
		t.Fatalf("重复通知不得改变状态，实际 %s", got)
	}

	// 迟到支付回调：占位超时关闭后才收到成功通知，不复活预约。
	rec := f.do(t, http.MethodPost, "/api/appointments", RoleUser, "U1", model.StoreID,
		map[string]any{"pet_id": "P2", "slot_id": "SL2"})
	var late model.Appointment
	if err := json.NewDecoder(rec.Body).Decode(&late); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(model.HoldDuration + time.Second)
	if got := f.do(t, http.MethodPost, "/api/holds/expire", RoleChannel, "CH1", model.StoreID, map[string]any{}).Code; got != http.StatusOK {
		t.Fatalf("超时扫描失败: %d", got)
	}
	if got := f.do(t, http.MethodPost, "/api/appointments/"+late.ID+"/pay", RoleChannel, "CH1", model.StoreID,
		map[string]any{"notify_key": "N2", "channel_trade_no": "T-N2"}).Code; got != http.StatusOK {
		t.Fatalf("迟到回调应被登记，实际 %d", got)
	}
	if got := f.do(t, http.MethodGet, "/api/appointments/"+late.ID, RoleUser, "U1", model.StoreID, nil); got.Code != http.StatusOK {
		t.Fatalf("查询迟到预约失败: %d", got.Code)
	}

	// 退款失败可配置。
	if got := f.do(t, http.MethodPost, "/api/channel/refund-outcome", RoleChannel, "CH1", model.StoreID,
		map[string]any{"succeeds": false}).Code; got != http.StatusOK {
		t.Fatal("渠道配置失败")
	}
	if got := f.do(t, http.MethodPost, "/api/appointments/"+id+"/cancel", RoleUser, "U1", model.StoreID,
		map[string]any{"reason": ""}).Code; got != http.StatusOK {
		t.Fatalf("取消失败: %d", got)
	}
	if refund := f.view(t, id).Refund; refund == nil || refund.Status != model.RefundFailed {
		t.Fatalf("渠道失败应反映在退款状态，实际 %+v", refund)
	}
}

func TestAPI_ThreeKindsOfOverreachDoNotBreakRecords(t *testing.T) {
	f := newFixture(t)
	f.publish(t, "SL1")
	id := f.createPaid(t, "SL1", "P1", "N1")

	// 1) 非本店员工核销。
	if got := f.do(t, http.MethodPost, "/api/appointments/"+id+"/verify", RoleFrontDesk, "FD1", "OTHER-STORE", map[string]any{}).Code; got != http.StatusForbidden {
		t.Fatalf("越权 1 应 403，实际 %d", got)
	}
	// 2) 用户取消他人预约。
	if got := f.do(t, http.MethodPost, "/api/appointments/"+id+"/cancel", RoleUser, "U2", model.StoreID,
		map[string]any{"reason": ""}).Code; got != http.StatusForbidden {
		t.Fatalf("越权 2 应 403，实际 %d", got)
	}
	// 3) 已核销预约再走取消退款流程。
	if got := f.do(t, http.MethodPost, "/api/appointments/"+id+"/verify", RoleFrontDesk, "FD1", model.StoreID, map[string]any{}).Code; got != http.StatusOK {
		t.Fatalf("合法核销失败: %d", got)
	}
	if got := f.do(t, http.MethodPost, "/api/appointments/"+id+"/cancel", RoleUser, "U1", model.StoreID,
		map[string]any{"reason": ""}).Code; got != http.StatusConflict {
		t.Fatalf("越权 3 应被拒，实际 %d", got)
	}

	view := f.view(t, id)
	if view.Appointment.Status != model.StatusVerified || view.Refund != nil {
		t.Fatalf("三类越权均不得破坏记录: %+v", view)
	}
	rec := f.do(t, http.MethodGet, "/api/settlement", RoleManager, "M1", model.StoreID, nil)
	var settlement service.Settlement
	if err := json.NewDecoder(rec.Body).Decode(&settlement); err != nil {
		t.Fatal(err)
	}
	if settlement.CollectedFen != model.AmountFen || settlement.RefundedFen != 0 || settlement.RevenueFen != 0 {
		t.Fatalf("金额记录无重复无丢失: %+v", settlement)
	}
}
