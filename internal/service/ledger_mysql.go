package service

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"

	"github.com/Abeautiful-crypto/Pet-store/internal/model"
)

// MySQLLedger 以 MySQL 为业务单据的真相源（F6.5）。
// 支付/退款/取消/服务/异常全部落库，状态语义与 memoryLedger 完全一致；
// 幂等由 payments 表的 notify_key / channel_trade_no 唯一索引兜底。
type MySQLLedger struct {
	db *sql.DB
}

// NewMySQLLedger 用已建好连接池的 *sql.DB 装配单据账本。
func NewMySQLLedger(db *sql.DB) *MySQLLedger {
	return &MySQLLedger{db: db}
}

var _ Ledger = (*MySQLLedger)(nil)

func ctx() context.Context { return context.Background() }

// ---- payments ----

const (
	sqlInsertPayment   = `INSERT IGNORE INTO payments (payment_id, appointment_id, amount_fen, channel_trade_no, notify_key, status) VALUES (?, ?, ?, ?, ?, ?)`
	sqlSelectPayment   = `SELECT payment_id, appointment_id, amount_fen, channel_trade_no, notify_key, status FROM payments`
	sqlPaymentByNotify = sqlSelectPayment + ` WHERE notify_key = ?`
	sqlPaymentOf       = sqlSelectPayment + ` WHERE appointment_id = ?`
)

// SavePayment 写入支付单；notify_key 已存在时 INSERT IGNORE 影响行数为 0，返回 false（幂等命中）。
func (l *MySQLLedger) SavePayment(p model.Payment) bool {
	res, err := l.db.ExecContext(ctx(), sqlInsertPayment,
		p.ID, p.AppointmentID, p.AmountFen, p.ChannelTradeNo, p.NotifyKey, string(p.Status))
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

func scanPayment(scan func(...interface{}) error) (model.Payment, error) {
	var p model.Payment
	if err := scan(&p.ID, &p.AppointmentID, &p.AmountFen, &p.ChannelTradeNo, &p.NotifyKey, &p.Status); err != nil {
		return model.Payment{}, err
	}
	return p, nil
}

func (l *MySQLLedger) PaymentByNotify(notifyKey string) (model.Payment, bool) {
	row := l.db.QueryRowContext(ctx(), sqlPaymentByNotify, notifyKey)
	p, err := scanPayment(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Payment{}, false
	}
	if err != nil {
		return model.Payment{}, false
	}
	return p, true
}

func (l *MySQLLedger) PaymentOf(appointmentID string) (model.Payment, bool) {
	row := l.db.QueryRowContext(ctx(), sqlPaymentOf, appointmentID)
	p, err := scanPayment(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Payment{}, false
	}
	if err != nil {
		return model.Payment{}, false
	}
	return p, true
}

func (l *MySQLLedger) Payments() []model.Payment {
	rows, err := l.db.QueryContext(ctx(), sqlSelectPayment)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []model.Payment
	for rows.Next() {
		p, err := scanPayment(rows.Scan)
		if err != nil {
			return out
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return out
	}
	return out
}

// ---- refunds ----

const (
	sqlInsertRefund = `INSERT INTO refunds (refund_id, appointment_id, payment_id, amount_fen, status, retry_count, last_error, created_at, finished_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			appointment_id = VALUES(appointment_id), payment_id = VALUES(payment_id), amount_fen = VALUES(amount_fen),
			status = VALUES(status), retry_count = VALUES(retry_count), last_error = VALUES(last_error),
			created_at = VALUES(created_at), finished_at = VALUES(finished_at)`
	sqlSelectRefund = `SELECT refund_id, appointment_id, payment_id, amount_fen, status, retry_count, last_error, created_at, finished_at FROM refunds`
	sqlRefundOf     = sqlSelectRefund + ` WHERE appointment_id = ? ORDER BY created_at DESC LIMIT 1`
)

func (l *MySQLLedger) SaveRefund(refund model.Refund) {
	var finished any
	if refund.FinishedAt != nil {
		finished = *refund.FinishedAt
	}
	_, _ = l.db.ExecContext(ctx(), sqlInsertRefund,
		refund.ID, refund.AppointmentID, refund.PaymentID, refund.AmountFen, string(refund.Status),
		refund.RetryCount, nullString(refund.LastError), refund.CreatedAt, finished)
}

func scanRefund(scan func(...interface{}) error) (model.Refund, error) {
	var r model.Refund
	var finished sql.NullTime
	var lastErrStr sql.NullString
	if err := scan(&r.ID, &r.AppointmentID, &r.PaymentID, &r.AmountFen, &r.Status,
		&r.RetryCount, &lastErrStr, &r.CreatedAt, &finished); err != nil {
		return model.Refund{}, err
	}
	if lastErrStr.Valid {
		r.LastError = lastErrStr.String
	}
	if finished.Valid {
		t := finished.Time
		r.FinishedAt = &t
	}
	return r, nil
}

func (l *MySQLLedger) RefundOf(appointmentID string) (model.Refund, bool) {
	row := l.db.QueryRowContext(ctx(), sqlRefundOf, appointmentID)
	r, err := scanRefund(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Refund{}, false
	}
	if err != nil {
		return model.Refund{}, false
	}
	return r, true
}

func (l *MySQLLedger) Refunds() []model.Refund {
	rows, err := l.db.QueryContext(ctx(), sqlSelectRefund)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []model.Refund
	for rows.Next() {
		r, err := scanRefund(rows.Scan)
		if err != nil {
			return out
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return out
	}
	return out
}

const (
	sqlInsertRefundEvent  = `INSERT IGNORE INTO refund_events (event_id, refund_id, appointment_id, status, retry_count, last_error, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`
	sqlSelectRefundEvents = `SELECT event_id, refund_id, appointment_id, status, retry_count, last_error, created_at FROM refund_events ORDER BY created_at`
)

// AppendRefundEvent 记录退款状态每次变化，形成失败→重试→成功可查状态链（FR-10）。
func (l *MySQLLedger) AppendRefundEvent(refund model.Refund) {
	_, _ = l.db.ExecContext(ctx(), sqlInsertRefundEvent,
		refund.ID+"#"+strconv.Itoa(refund.RetryCount), refund.ID, refund.AppointmentID,
		string(refund.Status), refund.RetryCount, nullString(refund.LastError), refund.CreatedAt)
}

func (l *MySQLLedger) RefundEvents() []model.Refund {
	rows, err := l.db.QueryContext(ctx(), sqlSelectRefundEvents)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []model.Refund
	for rows.Next() {
		var _id, _rid, _aid, status string
		var retry int
		var lastError sql.NullString
		var createdAt time.Time
		if err := rows.Scan(&_id, &_rid, &_aid, &status, &retry, &lastError, &createdAt); err != nil {
			return out
		}
		out = append(out, model.Refund{
			ID:            _rid,
			AppointmentID: _aid,
			Status:        model.RefundStatus(status),
			RetryCount:    retry,
			LastError:     lastError.String,
			CreatedAt:     createdAt,
		})
	}
	return out
}

// ---- cancel requests ----

const (
	sqlInsertCancel = `INSERT INTO cancel_requests (request_id, appointment_id, reason, apply_at, hours_to_start, type, status, reviewer, reviewed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			appointment_id = VALUES(appointment_id), reason = VALUES(reason), apply_at = VALUES(apply_at),
			hours_to_start = VALUES(hours_to_start), type = VALUES(type), status = VALUES(status),
			reviewer = VALUES(reviewer), reviewed_at = VALUES(reviewed_at)`
	sqlSelectCancel = `SELECT request_id, appointment_id, reason, apply_at, hours_to_start, type, status, reviewer, reviewed_at FROM cancel_requests`
	sqlCancelByID   = sqlSelectCancel + ` WHERE request_id = ?`
	sqlCancelOf     = sqlSelectCancel + ` WHERE appointment_id = ? ORDER BY apply_at DESC LIMIT 1`
)

func (l *MySQLLedger) SaveCancelRequest(req model.CancelRequest) {
	var reviewed any
	if req.ReviewedAt != nil {
		reviewed = *req.ReviewedAt
	}
	_, _ = l.db.ExecContext(ctx(), sqlInsertCancel,
		req.ID, req.AppointmentID, req.Reason, req.ApplyAt, req.HoursToStart, req.Type,
		string(req.Status), nullString(req.Reviewer), reviewed)
}

func scanCancel(scan func(...interface{}) error) (model.CancelRequest, error) {
	var r model.CancelRequest
	var reviewed sql.NullTime
	if err := scan(&r.ID, &r.AppointmentID, &r.Reason, &r.ApplyAt, &r.HoursToStart,
		&r.Type, &r.Status, &r.Reviewer, &reviewed); err != nil {
		return model.CancelRequest{}, err
	}
	if reviewed.Valid {
		t := reviewed.Time
		r.ReviewedAt = &t
	}
	return r, nil
}

func (l *MySQLLedger) CancelRequestByID(requestID string) (model.CancelRequest, bool) {
	row := l.db.QueryRowContext(ctx(), sqlCancelByID, requestID)
	r, err := scanCancel(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return model.CancelRequest{}, false
	}
	if err != nil {
		return model.CancelRequest{}, false
	}
	return r, true
}

func (l *MySQLLedger) CancelRequestOf(appointmentID string) (model.CancelRequest, bool) {
	row := l.db.QueryRowContext(ctx(), sqlCancelOf, appointmentID)
	r, err := scanCancel(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return model.CancelRequest{}, false
	}
	if err != nil {
		return model.CancelRequest{}, false
	}
	return r, true
}

// ---- service records ----

const (
	sqlInsertService = `INSERT INTO service_records (record_id, appointment_id, verify_by, verify_at, started_at, finished_at, revenue_fen, no_show_reason)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			appointment_id = VALUES(appointment_id), verify_by = VALUES(verify_by), verify_at = VALUES(verify_at),
			started_at = VALUES(started_at), finished_at = VALUES(finished_at), revenue_fen = VALUES(revenue_fen),
			no_show_reason = VALUES(no_show_reason)`
	sqlSelectService = `SELECT record_id, appointment_id, verify_by, verify_at, started_at, finished_at, revenue_fen, no_show_reason FROM service_records`
	sqlServiceOf     = sqlSelectService + ` WHERE appointment_id = ?`
)

func (l *MySQLLedger) SaveServiceRecord(record model.ServiceRecord) {
	var verify, started, finished any
	if record.VerifyAt != nil {
		verify = *record.VerifyAt
	}
	if record.StartedAt != nil {
		started = *record.StartedAt
	}
	if record.FinishedAt != nil {
		finished = *record.FinishedAt
	}
	_, _ = l.db.ExecContext(ctx(), sqlInsertService,
		record.ID, record.AppointmentID, nullString(record.VerifyBy), verify, started, finished,
		record.RevenueFen, nullString(record.NoShowReason))
}

func scanService(scan func(...interface{}) error) (model.ServiceRecord, error) {
	var r model.ServiceRecord
	var verify, started, finished sql.NullTime
	var verifyBy, noShow sql.NullString
	if err := scan(&r.ID, &r.AppointmentID, &verifyBy, &verify, &started, &finished, &r.RevenueFen, &noShow); err != nil {
		return model.ServiceRecord{}, err
	}
	if verifyBy.Valid {
		r.VerifyBy = verifyBy.String
	}
	if noShow.Valid {
		r.NoShowReason = noShow.String
	}
	if verify.Valid {
		t := verify.Time
		r.VerifyAt = &t
	}
	if started.Valid {
		t := started.Time
		r.StartedAt = &t
	}
	if finished.Valid {
		t := finished.Time
		r.FinishedAt = &t
	}
	return r, nil
}

func (l *MySQLLedger) ServiceRecordOf(appointmentID string) (model.ServiceRecord, bool) {
	row := l.db.QueryRowContext(ctx(), sqlServiceOf, appointmentID)
	r, err := scanService(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return model.ServiceRecord{}, false
	}
	if err != nil {
		return model.ServiceRecord{}, false
	}
	return r, true
}

func (l *MySQLLedger) ServiceRecords() []model.ServiceRecord {
	rows, err := l.db.QueryContext(ctx(), sqlSelectService)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []model.ServiceRecord
	for rows.Next() {
		r, err := scanService(rows.Scan)
		if err != nil {
			return out
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return out
	}
	return out
}

// ---- exceptions ----

const (
	sqlInsertException  = `INSERT IGNORE INTO exceptions (exception_id, appointment_id, action, detail, created_at) VALUES (?, ?, ?, ?, ?)`
	sqlSelectExceptions = `SELECT exception_id, appointment_id, action, detail, created_at FROM exceptions ORDER BY created_at`
)

func (l *MySQLLedger) AppendException(event model.EventLog) {
	_, _ = l.db.ExecContext(ctx(), sqlInsertException,
		event.ID, event.AppointmentID, event.Action, event.Detail, event.CreatedAt)
}

func (l *MySQLLedger) Exceptions() []model.EventLog {
	rows, err := l.db.QueryContext(ctx(), sqlSelectExceptions)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []model.EventLog
	for rows.Next() {
		var e model.EventLog
		if err := rows.Scan(&e.ID, &e.AppointmentID, &e.Action, &e.Detail, &e.CreatedAt); err != nil {
			return out
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return out
	}
	return out
}

// Reset 清空所有业务单据表（演示重置）；不含 slots/appointments/event_logs（由 store.Reset 负责）。
func (l *MySQLLedger) Reset() {
	for _, q := range []string{
		`DELETE FROM exceptions`,
		`DELETE FROM refund_events`,
		`DELETE FROM refunds`,
		`DELETE FROM cancel_requests`,
		`DELETE FROM service_records`,
		`DELETE FROM payments`,
	} {
		_, _ = l.db.ExecContext(ctx(), q)
	}
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
