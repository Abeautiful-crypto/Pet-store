package service

import (
	"sync"

	"github.com/Abeautiful-crypto/Pet-store/internal/model"
)

// Ledger 保存支付单、退款单、取消申请与异常登记，流水只增不改。
// 原型期为内存实现，状态语义与 SRS 5.2 E6/E7/E8/E10 一致；后续可替换为 MySQL 真相源。
type Ledger struct {
	mu              sync.Mutex
	payments        map[string]model.Payment
	paymentByNotify map[string]string
	refunds         map[string]model.Refund
	refundByAppt    map[string]string
	cancelRequests  map[string]model.CancelRequest
	requestByAppt   map[string]string
	exceptions      []model.EventLog
}

func NewLedger() *Ledger {
	return &Ledger{
		payments:        map[string]model.Payment{},
		paymentByNotify: map[string]string{},
		refunds:         map[string]model.Refund{},
		refundByAppt:    map[string]string{},
		cancelRequests:  map[string]model.CancelRequest{},
		requestByAppt:   map[string]string{},
	}
}

// SavePayment 以 notify_key 幂等；返回 false 表示重复通知，不新增流水。
func (l *Ledger) SavePayment(p model.Payment) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, exists := l.paymentByNotify[p.NotifyKey]; exists {
		return false
	}
	l.paymentByNotify[p.NotifyKey] = p.AppointmentID
	l.payments[p.AppointmentID] = p
	return true
}

func (l *Ledger) PaymentByNotify(notifyKey string) (model.Payment, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	appointmentID, ok := l.paymentByNotify[notifyKey]
	if !ok {
		return model.Payment{}, false
	}
	payment, ok := l.payments[appointmentID]
	return payment, ok
}

func (l *Ledger) PaymentOf(appointmentID string) (model.Payment, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	payment, ok := l.payments[appointmentID]
	return payment, ok
}

func (l *Ledger) Payments() []model.Payment {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]model.Payment, 0, len(l.payments))
	for _, payment := range l.payments {
		out = append(out, payment)
	}
	return out
}

// SaveRefund 记录退款单；退款状态独立于预约状态（规则 4 / FR-10）。
func (l *Ledger) SaveRefund(refund model.Refund) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.refunds[refund.ID] = refund
	l.refundByAppt[refund.AppointmentID] = refund.ID
}

func (l *Ledger) RefundOf(appointmentID string) (model.Refund, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	refundID, ok := l.refundByAppt[appointmentID]
	if !ok {
		return model.Refund{}, false
	}
	refund, ok := l.refunds[refundID]
	return refund, ok
}

func (l *Ledger) Refunds() []model.Refund {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]model.Refund, 0, len(l.refunds))
	for _, refund := range l.refunds {
		out = append(out, refund)
	}
	return out
}

// SaveCancelRequest 记录取消申请；按 appointmentID 建立索引供审核与查询回显。
func (l *Ledger) SaveCancelRequest(req model.CancelRequest) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cancelRequests[req.ID] = req
	l.requestByAppt[req.AppointmentID] = req.ID
}

func (l *Ledger) CancelRequestByID(requestID string) (model.CancelRequest, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	req, ok := l.cancelRequests[requestID]
	return req, ok
}

func (l *Ledger) CancelRequestOf(appointmentID string) (model.CancelRequest, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	requestID, ok := l.requestByAppt[appointmentID]
	if !ok {
		return model.CancelRequest{}, false
	}
	req, ok := l.cancelRequests[requestID]
	return req, ok
}

func (l *Ledger) AppendException(event model.EventLog) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.exceptions = append(l.exceptions, event)
}

func (l *Ledger) Exceptions() []model.EventLog {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]model.EventLog(nil), l.exceptions...)
}

func (l *Ledger) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.payments = map[string]model.Payment{}
	l.paymentByNotify = map[string]string{}
	l.refunds = map[string]model.Refund{}
	l.refundByAppt = map[string]string{}
	l.cancelRequests = map[string]model.CancelRequest{}
	l.requestByAppt = map[string]string{}
	l.exceptions = nil
}
