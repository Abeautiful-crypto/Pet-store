package service

import (
	"sync"

	"github.com/Abeautiful-crypto/Pet-store/internal/model"
)

// Ledger 保存支付单、退款单、取消申请、服务履约记录与异常登记，流水只增不改。
// 原型期为内存实现（memoryLedger），生产以 MySQLLedger 为真相源（F6.5）。
type Ledger interface {
	SavePayment(p model.Payment) bool
	PaymentByNotify(notifyKey string) (model.Payment, bool)
	PaymentOf(appointmentID string) (model.Payment, bool)
	Payments() []model.Payment
	SaveRefund(refund model.Refund)
	RefundOf(appointmentID string) (model.Refund, bool)
	Refunds() []model.Refund
	AppendRefundEvent(refund model.Refund)
	RefundEvents() []model.Refund
	SaveCancelRequest(req model.CancelRequest)
	CancelRequestByID(requestID string) (model.CancelRequest, bool)
	CancelRequestOf(appointmentID string) (model.CancelRequest, bool)
	SaveServiceRecord(record model.ServiceRecord)
	ServiceRecordOf(appointmentID string) (model.ServiceRecord, bool)
	ServiceRecords() []model.ServiceRecord
	AppendException(event model.EventLog)
	Exceptions() []model.EventLog
	Reset()
}

// memoryLedger 是 Ledger 的零依赖内存实现，用于单测与 dev 回退。
// 状态语义与 SRS 5.2 E6~E10 一致；后续可替换为 MySQL 真相源。
type memoryLedger struct {
	mu              sync.Mutex
	payments        map[string]model.Payment
	paymentByNotify map[string]string
	refunds         map[string]model.Refund
	refundByAppt    map[string]string
	refundEvents    []model.Refund
	cancelRequests  map[string]model.CancelRequest
	requestByAppt   map[string]string
	serviceRecords  map[string]model.ServiceRecord
	exceptions      []model.EventLog
}

func NewLedger() Ledger {
	return &memoryLedger{
		payments:        map[string]model.Payment{},
		paymentByNotify: map[string]string{},
		refunds:         map[string]model.Refund{},
		refundByAppt:    map[string]string{},
		cancelRequests:  map[string]model.CancelRequest{},
		requestByAppt:   map[string]string{},
		serviceRecords:  map[string]model.ServiceRecord{},
	}
}

func (l *memoryLedger) SavePayment(p model.Payment) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, exists := l.paymentByNotify[p.NotifyKey]; exists {
		return false
	}
	l.paymentByNotify[p.NotifyKey] = p.AppointmentID
	l.payments[p.AppointmentID] = p
	return true
}

func (l *memoryLedger) PaymentByNotify(notifyKey string) (model.Payment, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	appointmentID, ok := l.paymentByNotify[notifyKey]
	if !ok {
		return model.Payment{}, false
	}
	payment, ok := l.payments[appointmentID]
	return payment, ok
}

func (l *memoryLedger) PaymentOf(appointmentID string) (model.Payment, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	payment, ok := l.payments[appointmentID]
	return payment, ok
}

func (l *memoryLedger) Payments() []model.Payment {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]model.Payment, 0, len(l.payments))
	for _, payment := range l.payments {
		out = append(out, payment)
	}
	return out
}

func (l *memoryLedger) SaveRefund(refund model.Refund) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.refunds[refund.ID] = refund
	l.refundByAppt[refund.AppointmentID] = refund.ID
}

func (l *memoryLedger) RefundOf(appointmentID string) (model.Refund, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	refundID, ok := l.refundByAppt[appointmentID]
	if !ok {
		return model.Refund{}, false
	}
	refund, ok := l.refunds[refundID]
	return refund, ok
}

func (l *memoryLedger) Refunds() []model.Refund {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]model.Refund, 0, len(l.refunds))
	for _, refund := range l.refunds {
		out = append(out, refund)
	}
	return out
}

// AppendRefundEvent 记录退款状态的每次变化，形成失败→重试→成功的可查状态链（FR-10）。
func (l *memoryLedger) AppendRefundEvent(refund model.Refund) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.refundEvents = append(l.refundEvents, refund)
}

func (l *memoryLedger) RefundEvents() []model.Refund {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]model.Refund(nil), l.refundEvents...)
}

func (l *memoryLedger) SaveCancelRequest(req model.CancelRequest) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cancelRequests[req.ID] = req
	l.requestByAppt[req.AppointmentID] = req.ID
}

func (l *memoryLedger) CancelRequestByID(requestID string) (model.CancelRequest, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	req, ok := l.cancelRequests[requestID]
	return req, ok
}

func (l *memoryLedger) CancelRequestOf(appointmentID string) (model.CancelRequest, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	requestID, ok := l.requestByAppt[appointmentID]
	if !ok {
		return model.CancelRequest{}, false
	}
	req, ok := l.cancelRequests[requestID]
	return req, ok
}

func (l *memoryLedger) SaveServiceRecord(record model.ServiceRecord) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.serviceRecords[record.AppointmentID] = record
}

func (l *memoryLedger) ServiceRecordOf(appointmentID string) (model.ServiceRecord, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	record, ok := l.serviceRecords[appointmentID]
	return record, ok
}

func (l *memoryLedger) ServiceRecords() []model.ServiceRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]model.ServiceRecord, 0, len(l.serviceRecords))
	for _, record := range l.serviceRecords {
		out = append(out, record)
	}
	return out
}

func (l *memoryLedger) AppendException(event model.EventLog) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.exceptions = append(l.exceptions, event)
}

func (l *memoryLedger) Exceptions() []model.EventLog {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]model.EventLog(nil), l.exceptions...)
}

func (l *memoryLedger) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.payments = map[string]model.Payment{}
	l.paymentByNotify = map[string]string{}
	l.refunds = map[string]model.Refund{}
	l.refundByAppt = map[string]string{}
	l.refundEvents = nil
	l.cancelRequests = map[string]model.CancelRequest{}
	l.requestByAppt = map[string]string{}
	l.serviceRecords = map[string]model.ServiceRecord{}
	l.exceptions = nil
}
