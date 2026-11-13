// Package api 提供 REST/JSON 接口与五角色权限校验。
// 前端隐藏按钮不等于安全：每个写操作在后端独立 requireRole + 数据归属校验。
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Abeautiful-crypto/Pet-store/internal/model"
	"github.com/Abeautiful-crypto/Pet-store/internal/service"
	"github.com/Abeautiful-crypto/Pet-store/internal/store"
)

const (
	RoleUser      = "user"
	RoleFrontDesk = "frontdesk"
	RoleCS        = "cs"
	RoleManager   = "manager"
	RoleChannel   = "channel"
)

// Identity 从请求头解析演示身份；真实系统应由登录态/会话提供，不得由客户端伪造。
type Identity struct {
	Role    string
	UserID  string
	StoreID string
}

type Server struct {
	svc *service.Service
	mux *http.ServeMux
}

func New(svc *service.Service) *Server {
	s := &Server{svc: svc, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/slots", s.require([]string{RoleUser, RoleFrontDesk, RoleCS, RoleManager}, s.listSlots))
	s.mux.HandleFunc("POST /api/slots", s.require([]string{RoleManager}, s.publishSlot))
	s.mux.HandleFunc("POST /api/appointments", s.require([]string{RoleUser}, s.createAppointment))
	s.mux.HandleFunc("GET /api/appointments/{id}", s.require([]string{RoleUser, RoleFrontDesk, RoleCS, RoleManager}, s.getAppointment))
	s.mux.HandleFunc("POST /api/appointments/{id}/pay", s.require([]string{RoleUser, RoleChannel}, s.pay))
	s.mux.HandleFunc("POST /api/appointments/{id}/cancel", s.require([]string{RoleUser}, s.cancel))
	s.mux.HandleFunc("POST /api/appointments/{id}/verify", s.require([]string{RoleFrontDesk}, s.verify))
	s.mux.HandleFunc("POST /api/appointments/{id}/start", s.require([]string{RoleFrontDesk}, s.startService))
	s.mux.HandleFunc("POST /api/appointments/{id}/finish", s.require([]string{RoleFrontDesk}, s.finishService))
	s.mux.HandleFunc("POST /api/appointments/{id}/no-show", s.require([]string{RoleManager}, s.markNoShow))
	s.mux.HandleFunc("POST /api/cancel-requests/{id}/review", s.require([]string{RoleCS}, s.reviewCancel))
	s.mux.HandleFunc("POST /api/refunds/retry", s.require([]string{RoleCS}, s.retryRefund))
	s.mux.HandleFunc("GET /api/settlement", s.require([]string{RoleManager}, s.settlement))
	s.mux.HandleFunc("POST /api/channel/refund-outcome", s.require([]string{RoleChannel}, s.refundOutcome))
	s.mux.HandleFunc("POST /api/holds/expire", s.require([]string{RoleManager, RoleChannel}, s.expireHolds))
}

func identityFrom(r *http.Request) Identity {
	storeID := r.Header.Get("X-Store-Id")
	if storeID == "" {
		storeID = model.StoreID
	}
	return Identity{
		Role:    r.Header.Get("X-Role"),
		UserID:  r.Header.Get("X-User-Id"),
		StoreID: storeID,
	}
}

// requireRole 守卫：每个写操作第一行校验角色，不通过返回 403 且声明未做任何数据改动。
func (s *Server) require(roles []string, next func(http.ResponseWriter, *http.Request, Identity)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := identityFrom(r)
		allowed := false
		for _, role := range roles {
			if id.Role == role {
				allowed = true
				break
			}
		}
		if !allowed {
			writeError(w, http.StatusForbidden, "无权访问：本次操作被拦截，未做任何数据改动")
			return
		}
		next(w, r, id)
	}
}

func writeJSON(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, code int, message string) {
	writeJSON(w, code, map[string]string{"error": message})
}

func decode(r *http.Request, dst any) error {
	return json.NewDecoder(r.Body).Decode(dst)
}

func fail(w http.ResponseWriter, err error) {
	writeError(w, statusFor(err), err.Error())
}

func statusFor(err error) int {
	switch {
	case errors.Is(err, service.ErrPetNotOwned), errors.Is(err, service.ErrNotOwner),
		errors.Is(err, service.ErrWrongStore):
		return http.StatusForbidden
	case errors.Is(err, store.ErrSlotNotFound), errors.Is(err, store.ErrAppointmentNotFound),
		errors.Is(err, service.ErrCancelRequestNotFound), errors.Is(err, service.ErrRefundNotFound):
		return http.StatusNotFound
	case errors.Is(err, store.ErrSlotFull), errors.Is(err, store.ErrDuplicateActive),
		errors.Is(err, store.ErrVersionConflict), errors.Is(err, store.ErrAlreadyReleased),
		errors.Is(err, model.ErrIllegalTransition), errors.Is(err, model.ErrTerminalStatus),
		errors.Is(err, service.ErrPendingCancel), errors.Is(err, service.ErrSlotStarted),
		errors.Is(err, service.ErrReasonRequired), errors.Is(err, service.ErrSlotNotEnded),
		errors.Is(err, service.ErrAlreadyVerified), errors.Is(err, service.ErrCancelAlreadyReviewed),
		errors.Is(err, service.ErrRefundNotRetryable):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func (s *Server) publishSlot(w http.ResponseWriter, r *http.Request, _ Identity) {
	var req struct {
		SlotID  string `json:"slot_id"`
		StartAt string `json:"start_at"`
		EndAt   string `json:"end_at"`
	}
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体非法")
		return
	}
	startAt, err := time.Parse(time.RFC3339, req.StartAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, "start_at 需为 RFC3339")
		return
	}
	endAt, err := time.Parse(time.RFC3339, req.EndAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, "end_at 需为 RFC3339")
		return
	}
	slot := model.Slot{
		ID: req.SlotID, StoreID: model.StoreID, ServiceItemID: model.ServiceItemID,
		StartAt: startAt, EndAt: endAt, Capacity: model.SlotCapacity,
	}
	if err := s.svc.PublishSlot(r.Context(), slot); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, slot)
}

func (s *Server) listSlots(w http.ResponseWriter, r *http.Request, _ Identity) {
	slots, err := s.svc.ListSlots(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"slots": slots})
}

func (s *Server) createAppointment(w http.ResponseWriter, r *http.Request, id Identity) {
	var req struct {
		PetID  string `json:"pet_id"`
		SlotID string `json:"slot_id"`
	}
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体非法")
		return
	}
	appt, err := s.svc.CreateAppointment(r.Context(), id.UserID, req.PetID, req.SlotID)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, appt)
}

func (s *Server) getAppointment(w http.ResponseWriter, r *http.Request, id Identity) {
	view, err := s.svc.Snapshot(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	// 数据隔离：用户端只能查看自己的预约（过滤在 API 层，不在渲染时假过滤）。
	if id.Role == RoleUser && view.Appointment.UserID != id.UserID {
		writeError(w, http.StatusForbidden, "无权查看他人预约")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) pay(w http.ResponseWriter, r *http.Request, _ Identity) {
	var req struct {
		NotifyKey      string `json:"notify_key"`
		ChannelTradeNo string `json:"channel_trade_no"`
	}
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体非法")
		return
	}
	if err := s.svc.ConfirmPayment(r.Context(), r.PathValue("id"), req.NotifyKey, req.ChannelTradeNo); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"result": "ok"})
}

func (s *Server) cancel(w http.ResponseWriter, r *http.Request, id Identity) {
	var req struct {
		Reason string `json:"reason"`
	}
	if err := decode(r, &req); err != nil && err.Error() != "EOF" {
		writeError(w, http.StatusBadRequest, "请求体非法")
		return
	}
	refund, err := s.svc.Cancel(r.Context(), r.PathValue("id"), id.UserID, req.Reason)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"refund": refund})
}

func (s *Server) verify(w http.ResponseWriter, r *http.Request, id Identity) {
	if err := s.svc.Verify(r.Context(), r.PathValue("id"), id.StoreID, id.UserID); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"result": "verified"})
}

func (s *Server) startService(w http.ResponseWriter, r *http.Request, id Identity) {
	if err := s.svc.StartService(r.Context(), r.PathValue("id"), id.StoreID); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"result": "started"})
}

func (s *Server) finishService(w http.ResponseWriter, r *http.Request, id Identity) {
	if err := s.svc.FinishService(r.Context(), r.PathValue("id"), id.StoreID); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"result": "finished"})
}

func (s *Server) markNoShow(w http.ResponseWriter, r *http.Request, _ Identity) {
	var req struct {
		Reason string `json:"reason"`
	}
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体非法")
		return
	}
	if err := s.svc.MarkNoShow(r.Context(), r.PathValue("id"), req.Reason); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"result": "no_show"})
}

func (s *Server) reviewCancel(w http.ResponseWriter, r *http.Request, id Identity) {
	var req struct {
		Approve bool `json:"approve"`
	}
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体非法")
		return
	}
	refund, err := s.svc.ReviewCancel(r.Context(), r.PathValue("id"), id.UserID, req.Approve)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"refund": refund})
}

func (s *Server) retryRefund(w http.ResponseWriter, r *http.Request, _ Identity) {
	var req struct {
		AppointmentID string `json:"appointment_id"`
	}
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体非法")
		return
	}
	refund, err := s.svc.RetryRefund(r.Context(), req.AppointmentID)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"refund": refund})
}

func (s *Server) settlement(w http.ResponseWriter, r *http.Request, _ Identity) {
	result, err := s.svc.DailySettlement(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) refundOutcome(w http.ResponseWriter, r *http.Request, _ Identity) {
	var req struct {
		Succeeds bool `json:"succeeds"`
	}
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体非法")
		return
	}
	s.svc.SetRefundOutcome(req.Succeeds)
	writeJSON(w, http.StatusOK, map[string]bool{"succeeds": req.Succeeds})
}

func (s *Server) expireHolds(w http.ResponseWriter, r *http.Request, _ Identity) {
	n, err := s.svc.ExpireHolds(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"expired": n})
}
