package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Abeautiful-crypto/Pet-store/internal/model"
	"github.com/Abeautiful-crypto/Pet-store/internal/service"
	"github.com/Abeautiful-crypto/Pet-store/internal/store"
)

var apiNow = time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)

type apiClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *apiClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *apiClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type fixture struct {
	srv   *Server
	svc   *service.Service
	clock *apiClock
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st := store.NewMemoryStore()
	clock := &apiClock{now: apiNow}
	pets := map[string]string{}
	for i := 1; i <= 10; i++ {
		pets[fmt.Sprintf("P%d", i)] = "U1"
	}
	pets["PX"] = "U2"
	svc := service.New(service.Config{
		Store:   st,
		Now:     clock.Now,
		OwnsPet: func(userID, petID string) bool { return pets[petID] == userID && pets[petID] != "" },
	})
	return &fixture{srv: New(svc), svc: svc, clock: clock}
}

func (f *fixture) do(t *testing.T, method, path, role, userID, storeID string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	req.Header.Set("X-Role", role)
	req.Header.Set("X-User-Id", userID)
	req.Header.Set("X-Store-Id", storeID)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(rec, req)
	return rec
}

func (f *fixture) publish(t *testing.T, slotID string) {
	t.Helper()
	start := apiNow.Add(4 * time.Hour)
	rec := f.do(t, http.MethodPost, "/api/slots", RoleManager, "M1", model.StoreID, map[string]any{
		"slot_id":  slotID,
		"start_at": start.Format(time.RFC3339),
		"end_at":   start.Add(time.Hour).Format(time.RFC3339),
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("发布时段失败: %d %s", rec.Code, rec.Body.String())
	}
}

func (f *fixture) createPaid(t *testing.T, slotID, petID, notifyKey string) string {
	t.Helper()
	rec := f.do(t, http.MethodPost, "/api/appointments", RoleUser, "U1", model.StoreID,
		map[string]any{"pet_id": petID, "slot_id": slotID})
	if rec.Code != http.StatusCreated {
		t.Fatalf("创建预约失败: %d %s", rec.Code, rec.Body.String())
	}
	var appt model.Appointment
	if err := json.NewDecoder(rec.Body).Decode(&appt); err != nil {
		t.Fatal(err)
	}
	pay := f.do(t, http.MethodPost, "/api/appointments/"+appt.ID+"/pay", RoleUser, "U1", model.StoreID,
		map[string]any{"notify_key": notifyKey, "channel_trade_no": "T-" + notifyKey})
	if pay.Code != http.StatusOK {
		t.Fatalf("支付失败: %d %s", pay.Code, pay.Body.String())
	}
	return appt.ID
}

func (f *fixture) view(t *testing.T, appointmentID string) service.AppointmentView {
	t.Helper()
	rec := f.do(t, http.MethodGet, "/api/appointments/"+appointmentID, RoleUser, "U1", model.StoreID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("查询预约失败: %d %s", rec.Code, rec.Body.String())
	}
	var view service.AppointmentView
	if err := json.NewDecoder(rec.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	return view
}

// ---- F4.1 用户端 4 页与接口鉴权 ----

func TestAPI_UserCanOnlyUseOwnPet(t *testing.T) {
	f := newFixture(t)
	f.publish(t, "SL1")
	if rec := f.do(t, http.MethodPost, "/api/appointments", RoleUser, "U1", model.StoreID,
		map[string]any{"pet_id": "PX", "slot_id": "SL1"}); rec.Code != http.StatusForbidden {
		t.Fatalf("为他人宠物预约应 403，实际 %d", rec.Code)
	}
	if rec := f.do(t, http.MethodPost, "/api/appointments", RoleUser, "U1", model.StoreID,
		map[string]any{"pet_id": "P1", "slot_id": "SL1"}); rec.Code != http.StatusCreated {
		t.Fatalf("自己的宠物应可预约，实际 %d %s", rec.Code, rec.Body.String())
	}
}

func TestAPI_UnauthorizedReturns403(t *testing.T) {
	f := newFixture(t)
	if rec := f.do(t, http.MethodPost, "/api/slots", RoleUser, "U1", model.StoreID, map[string]any{}); rec.Code != http.StatusForbidden {
		t.Fatalf("用户访问店长接口应 403，实际 %d", rec.Code)
	}
	if rec := f.do(t, http.MethodGet, "/api/settlement", "", "U1", model.StoreID, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("无角色访问日清应 403，实际 %d", rec.Code)
	}
	f.publish(t, "SL1")
	id := f.createPaid(t, "SL1", "P1", "N1")
	if rec := f.do(t, http.MethodGet, "/api/appointments/"+id, RoleUser, "U2", model.StoreID, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("用户查看他人预约应 403，实际 %d", rec.Code)
	}
}

func TestAPI_AppointmentOutcomesVisible(t *testing.T) {
	f := newFixture(t)
	for i := 1; i <= 4; i++ {
		f.publish(t, fmt.Sprintf("SL%d", i))
	}
	a1 := f.createPaid(t, "SL1", "P1", "N1")
	a2 := f.createPaid(t, "SL2", "P2", "N2")
	a3 := f.createPaid(t, "SL3", "P3", "N3")
	a4 := f.createPaid(t, "SL4", "P4", "N4")

	for _, step := range []string{"verify", "start", "finish"} {
		if rec := f.do(t, http.MethodPost, "/api/appointments/"+a1+"/"+step, RoleFrontDesk, "FD1", model.StoreID, map[string]any{}); rec.Code != http.StatusOK {
			t.Fatalf("A1 %s 失败: %d %s", step, rec.Code, rec.Body.String())
		}
	}
	if rec := f.do(t, http.MethodPost, "/api/channel/refund-outcome", RoleChannel, "CH1", model.StoreID,
		map[string]any{"succeeds": false}); rec.Code != http.StatusOK {
		t.Fatal("渠道配置失败")
	}
	if rec := f.do(t, http.MethodPost, "/api/appointments/"+a3+"/cancel", RoleUser, "U1", model.StoreID,
		map[string]any{"reason": ""}); rec.Code != http.StatusOK {
		t.Fatalf("A3 取消失败: %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.do(t, http.MethodPost, "/api/channel/refund-outcome", RoleChannel, "CH1", model.StoreID,
		map[string]any{"succeeds": true}); rec.Code != http.StatusOK {
		t.Fatal("渠道恢复失败")
	}
	if rec := f.do(t, http.MethodPost, "/api/appointments/"+a2+"/cancel", RoleUser, "U1", model.StoreID,
		map[string]any{"reason": ""}); rec.Code != http.StatusOK {
		t.Fatalf("A2 取消失败: %d %s", rec.Code, rec.Body.String())
	}
	f.clock.Advance(6 * time.Hour)
	if rec := f.do(t, http.MethodPost, "/api/appointments/"+a4+"/no-show", RoleManager, "M1", model.StoreID,
		map[string]any{"reason": "主人未到场"}); rec.Code != http.StatusOK {
		t.Fatalf("A4 未到店失败: %d %s", rec.Code, rec.Body.String())
	}

	want := map[string]string{
		a1: "已完成服务", a2: "已取消退款", a3: "退款失败待跟进", a4: "未到店",
	}
	for id, expected := range want {
		if got := f.view(t, id).Outcome; got != expected {
			t.Fatalf("%s 结局 = %s, want %s", id, got, expected)
		}
	}
}

// ---- F4.3 客服 3 页与结果回显 ----

func TestAPI_ReviewResultsVisible(t *testing.T) {
	f := newFixture(t)
	f.publish(t, "SL1")
	id := f.createPaid(t, "SL1", "P1", "N1")
	f.clock.Advance(3 * time.Hour)

	if rec := f.do(t, http.MethodPost, "/api/appointments/"+id+"/cancel", RoleUser, "U1", model.StoreID,
		map[string]any{"reason": "临时有事"}); rec.Code != http.StatusOK {
		t.Fatalf("申请取消失败: %d %s", rec.Code, rec.Body.String())
	}
	var reqID string
	for _, action := range []struct {
		approve bool
		want    string
	}{
		{false, "进行中"},
		{true, "已取消退款"},
	} {
		if action.approve {
			if rec := f.do(t, http.MethodPost, "/api/appointments/"+id+"/cancel", RoleUser, "U1", model.StoreID,
				map[string]any{"reason": "临时有事"}); rec.Code != http.StatusOK {
				t.Fatalf("再次申请取消失败: %d %s", rec.Code, rec.Body.String())
			}
		}
		view := f.view(t, id)
		if view.CancelRequest == nil {
			t.Fatal("取消申请应对用户可见")
		}
		reqID = view.CancelRequest.ID
		if rec := f.do(t, http.MethodPost, "/api/cancel-requests/"+reqID+"/review", RoleCS, "CS1", model.StoreID,
			map[string]any{"approve": action.approve}); rec.Code != http.StatusOK {
			t.Fatalf("客服审核失败: %d %s", rec.Code, rec.Body.String())
		}
		if got := f.view(t, id).Outcome; got != action.want {
			t.Fatalf("审核后用户可见结局 = %s, want %s", got, action.want)
		}
	}

	rec := f.do(t, http.MethodGet, "/api/settlement", RoleManager, "M1", model.StoreID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("店长日清失败: %d", rec.Code)
	}
	var settlement service.Settlement
	if err := json.NewDecoder(rec.Body).Decode(&settlement); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, cancelled := range settlement.Cancelled {
		if cancelled == id {
			found = true
		}
	}
	if !found {
		t.Fatal("用户看到的已取消结果与店长日清不一致")
	}
}

// ---- F4.4 店长 4 页与日清核算 ----

func TestAPI_SettlementMatchesLedger(t *testing.T) {
	f := newFixture(t)
	f.publish(t, "SL1")
	f.publish(t, "SL2")
	a1 := f.createPaid(t, "SL1", "P1", "N1")
	a2 := f.createPaid(t, "SL2", "P2", "N2")
	for _, step := range []string{"verify", "start", "finish"} {
		if rec := f.do(t, http.MethodPost, "/api/appointments/"+a1+"/"+step, RoleFrontDesk, "FD1", model.StoreID, map[string]any{}); rec.Code != http.StatusOK {
			t.Fatalf("%s 失败: %d %s", step, rec.Code, rec.Body.String())
		}
	}
	if rec := f.do(t, http.MethodPost, "/api/appointments/"+a2+"/cancel", RoleUser, "U1", model.StoreID,
		map[string]any{"reason": ""}); rec.Code != http.StatusOK {
		t.Fatalf("取消失败: %d %s", rec.Code, rec.Body.String())
	}

	rec := f.do(t, http.MethodGet, "/api/settlement", RoleManager, "M1", model.StoreID, nil)
	var settlement service.Settlement
	if err := json.NewDecoder(rec.Body).Decode(&settlement); err != nil {
		t.Fatal(err)
	}
	if settlement.CollectedFen != 19800 || settlement.RefundedFen != 9900 ||
		settlement.UnrefundedFen != 9900 || settlement.RevenueFen != 9900 {
		t.Fatalf("四指标与逐笔记录不符: %+v", settlement)
	}
	if len(settlement.Completed) != 1 || settlement.Completed[0] != a1 ||
		len(settlement.Cancelled) != 1 || settlement.Cancelled[0] != a2 {
		t.Fatalf("分类与逐笔对账表不一致: %+v", settlement)
	}
	if got := f.view(t, a1).ServiceRecord; got == nil || got.RevenueFen != model.AmountFen {
		t.Fatalf("已完成服务收入应可由服务记录反查，实际 %+v", got)
	}
}
