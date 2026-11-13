package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Abeautiful-crypto/Pet-store/internal/model"
	"github.com/Abeautiful-crypto/Pet-store/internal/service"
	"github.com/Abeautiful-crypto/Pet-store/internal/store"
)

// F5.1 覆盖率补测：只补测试，不改实现、不放宽断言。

func rawDo(f *fixture, method, path, role, userID, storeID, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("X-Role", role)
	req.Header.Set("X-User-Id", userID)
	if storeID != "" {
		req.Header.Set("X-Store-Id", storeID)
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestCoverage_StatusForAllClasses(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{service.ErrPetNotOwned, http.StatusForbidden},
		{service.ErrNotOwner, http.StatusForbidden},
		{service.ErrWrongStore, http.StatusForbidden},
		{store.ErrSlotNotFound, http.StatusNotFound},
		{store.ErrAppointmentNotFound, http.StatusNotFound},
		{service.ErrCancelRequestNotFound, http.StatusNotFound},
		{service.ErrRefundNotFound, http.StatusNotFound},
		{store.ErrSlotFull, http.StatusConflict},
		{store.ErrDuplicateActive, http.StatusConflict},
		{store.ErrVersionConflict, http.StatusConflict},
		{store.ErrAlreadyReleased, http.StatusConflict},
		{model.ErrIllegalTransition, http.StatusConflict},
		{model.ErrTerminalStatus, http.StatusConflict},
		{service.ErrPendingCancel, http.StatusConflict},
		{service.ErrSlotStarted, http.StatusConflict},
		{service.ErrReasonRequired, http.StatusConflict},
		{service.ErrSlotNotEnded, http.StatusConflict},
		{service.ErrAlreadyVerified, http.StatusConflict},
		{service.ErrCancelAlreadyReviewed, http.StatusConflict},
		{service.ErrRefundNotRetryable, http.StatusConflict},
		{fmt.Errorf("wrapped: %w", service.ErrNotOwner), http.StatusForbidden},
		{errors.New("unknown"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		if got := statusFor(tc.err); got != tc.want {
			t.Fatalf("statusFor(%v) = %d, want %d", tc.err, got, tc.want)
		}
	}
}

func TestCoverage_BadRequests(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name, method, path, role, body string
		want                           int
	}{
		{"publish 非法 JSON", "POST", "/api/slots", RoleManager, "{bad", 400},
		{"publish start_at 非法", "POST", "/api/slots", RoleManager, `{"slot_id":"S","start_at":"x","end_at":"y"}`, 400},
		{"publish end_at 非法", "POST", "/api/slots", RoleManager, `{"slot_id":"S","start_at":"2026-11-13T14:00:00Z","end_at":"y"}`, 400},
		{"publish 时段非法", "POST", "/api/slots", RoleManager, `{"slot_id":"S","start_at":"2026-11-13T14:00:00Z","end_at":"2026-11-13T14:00:00Z"}`, 500},
		{"create 非法 JSON", "POST", "/api/appointments", RoleUser, "{bad", 400},
		{"pay 非法 JSON", "POST", "/api/appointments/X/pay", RoleChannel, "{bad", 400},
		{"cancel 非法 JSON", "POST", "/api/appointments/X/cancel", RoleUser, "{bad", 400},
		{"no-show 非法 JSON", "POST", "/api/appointments/X/no-show", RoleManager, "{bad", 400},
		{"review 非法 JSON", "POST", "/api/cancel-requests/X/review", RoleCS, "{bad", 400},
		{"retry 非法 JSON", "POST", "/api/refunds/retry", RoleCS, "{bad", 400},
		{"channel 非法 JSON", "POST", "/api/channel/refund-outcome", RoleChannel, "{bad", 400},
		{"create 时段不存在", "POST", "/api/appointments", RoleUser, `{"pet_id":"P1","slot_id":"NOPE"}`, 404},
		{"verify 预约不存在", "POST", "/api/appointments/NOPE/verify", RoleFrontDesk, `{}`, 404},
		{"start 预约不存在", "POST", "/api/appointments/NOPE/start", RoleFrontDesk, `{}`, 404},
		{"finish 预约不存在", "POST", "/api/appointments/NOPE/finish", RoleFrontDesk, `{}`, 404},
		{"no-show 预约不存在", "POST", "/api/appointments/NOPE/no-show", RoleManager, `{"reason":"x"}`, 404},
		{"review 申请不存在", "POST", "/api/cancel-requests/NOPE/review", RoleCS, `{"approve":true}`, 404},
		{"retry 退款单不存在", "POST", "/api/refunds/retry", RoleCS, `{"appointment_id":"NOPE"}`, 404},
		{"get 预约不存在", "GET", "/api/appointments/NOPE", RoleManager, "", 404},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := rawDo(f, tc.method, tc.path, tc.role, "U1", model.StoreID, tc.body)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d, body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestCoverage_EmptyBodyAndDefaultStoreID(t *testing.T) {
	f := newFixture(t)
	f.publish(t, "SL1")
	id := f.createPaid(t, "SL1", "P1", "N1")

	// cancel 允许空 body（EOF），走默认 reason 分支。
	rec := rawDo(f, "POST", "/api/appointments/"+id+"/cancel", RoleUser, "U1", model.StoreID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("空 body 取消应可解析: %d %s", rec.Code, rec.Body.String())
	}
	// 不传 X-Store-Id 时 identityFrom 回落到预置门店。
	rec = rawDo(f, "POST", "/api/appointments/"+id+"/verify", RoleFrontDesk, "FD1", "", "{}")
	if rec.Code != http.StatusConflict {
		t.Fatalf("已取消预约核销应被拒: %d %s", rec.Code, rec.Body.String())
	}
	// 成功核销路径（默认门店）。
	f2 := newFixture(t)
	f2.publish(t, "SL1")
	id2 := f2.createPaid(t, "SL1", "P1", "N1")
	if rec := rawDo(f2, "POST", "/api/appointments/"+id2+"/verify", RoleFrontDesk, "FD1", "", "{}"); rec.Code != http.StatusOK {
		t.Fatalf("默认门店核销应成功: %d %s", rec.Code, rec.Body.String())
	}
}

func TestCoverage_HandlerErrorBranches(t *testing.T) {
	f := newFixture(t)
	f.publish(t, "SL1")

	// 未支付预约核销 -> 409；越权取消 -> 403；pay 状态冲突 -> 409。
	rec := f.do(t, http.MethodPost, "/api/appointments", RoleUser, "U1", model.StoreID,
		map[string]any{"pet_id": "P1", "slot_id": "SL1"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("创建失败: %d", rec.Code)
	}
	var appt model.Appointment
	if err := json.NewDecoder(rec.Body).Decode(&appt); err != nil {
		t.Fatal(err)
	}
	if got := f.do(t, http.MethodPost, "/api/appointments/"+appt.ID+"/verify", RoleFrontDesk, "FD1", model.StoreID, map[string]any{}).Code; got != http.StatusConflict {
		t.Fatalf("未确认核销应 409: %d", got)
	}
	if got := f.do(t, http.MethodPost, "/api/appointments/"+appt.ID+"/cancel", RoleUser, "U2", model.StoreID, map[string]any{"reason": ""}).Code; got != http.StatusForbidden {
		t.Fatalf("越权取消应 403: %d", got)
	}
	if got := f.do(t, http.MethodPost, "/api/appointments/"+appt.ID+"/start", RoleFrontDesk, "FD1", "OTHER", map[string]any{}).Code; got != http.StatusForbidden {
		t.Fatalf("非本店登记开始应 403: %d", got)
	}
	if got := f.do(t, http.MethodPost, "/api/appointments/"+appt.ID+"/finish", RoleFrontDesk, "FD1", "OTHER", map[string]any{}).Code; got != http.StatusForbidden {
		t.Fatalf("非本店登记完成应 403: %d", got)
	}
}
