package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Abeautiful-crypto/Pet-store/internal/model"
)

// MySQL 真相源的 SQL 契约。名额占用/释放一律条件更新，以影响行数判定成败；
// 禁止"先 SELECT 再 UPDATE"（.agent/backend/tech-stack.md §1.1）。
const (
	SQLInsertSlot = `INSERT INTO slots (slot_id, store_id, service_item_id, start_at, end_at, capacity, occupied_count) VALUES (?, ?, ?, ?, ?, ?, ?)`
	SQLSelectSlot = `SELECT slot_id, store_id, service_item_id, start_at, end_at, capacity, occupied_count FROM slots WHERE slot_id = ?`

	SQLOccupySlot = `UPDATE slots SET occupied_count = occupied_count + 1 WHERE slot_id = ? AND occupied_count < capacity`

	SQLInsertAppointment = `INSERT INTO appointments (appointment_id, store_id, slot_id, user_id, pet_id, status, amount_fen, created_at, pay_deadline_at, version, active, slot_released) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, 0)`
	SQLSelectAppointment = `SELECT appointment_id, store_id, slot_id, user_id, pet_id, status, amount_fen, created_at, pay_deadline_at, version FROM appointments WHERE appointment_id = ?`
	SQLCountActive       = `SELECT COUNT(*) FROM appointments WHERE pet_id = ? AND slot_id = ? AND active = 1`

	// 状态迁移：同一行 CAS + 乐观锁 version，取消与核销并发只有一个胜者（规则 6）。
	SQLTransition = `UPDATE appointments SET status = ?, version = version + 1, active = ? WHERE appointment_id = ? AND status = ? AND version = ?`

	// 名额释放带 slot_released = 0 的一次性守卫，重复释放不会二次扣减（规则 4）。
	SQLReleaseSlot = `UPDATE slots s JOIN appointments a ON a.slot_id = s.slot_id SET s.occupied_count = s.occupied_count - 1, a.slot_released = 1 WHERE a.appointment_id = ? AND a.slot_released = 0 AND s.occupied_count > 0`

	SQLInsertEvent  = `INSERT INTO event_logs (event_id, appointment_id, action, from_status, to_status, actor_role, actor_store_id, detail, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	SQLSelectEvents = `SELECT event_id, appointment_id, action, from_status, to_status, actor_role, actor_store_id, detail, created_at FROM event_logs ORDER BY created_at`

	SQLResetEvents       = `DELETE FROM event_logs`
	SQLResetAppointments = `DELETE FROM appointments`
	SQLResetSlots        = `DELETE FROM slots`
)

// MySQLStore 以 MySQL 为真相源，Redis 只做缓存/去重，不参与名额计数。
type MySQLStore struct {
	mu          sync.Mutex
	db          *sql.DB
	invalidator SlotCacheInvalidator
}

// NewMySQLStore 用已建好连接池的 *sql.DB 装配存储层。
func NewMySQLStore(db *sql.DB) *MySQLStore {
	return &MySQLStore{db: db}
}

var _ StateStore = (*MySQLStore)(nil)

func (s *MySQLStore) SetInvalidator(inv SlotCacheInvalidator) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invalidator = inv
}

func (s *MySQLStore) currentInvalidator() SlotCacheInvalidator {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.invalidator
}

func (s *MySQLStore) invalidateSlot(ctx context.Context, slotID string) {
	if inv := s.currentInvalidator(); inv != nil {
		_ = inv.InvalidateSlot(ctx, slotID)
	}
}

func (s *MySQLStore) invalidateAll(ctx context.Context) {
	if inv := s.currentInvalidator(); inv != nil {
		_ = inv.InvalidateAll(ctx)
	}
}

func (s *MySQLStore) CreateSlot(ctx context.Context, slot model.Slot) error {
	if err := slot.Validate(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, SQLInsertSlot, slot.ID, slot.StoreID, slot.ServiceItemID,
		slot.StartAt, slot.EndAt, slot.Capacity, slot.OccupiedCount)
	if err != nil {
		return err
	}
	s.invalidateSlot(ctx, slot.ID)
	return nil
}

func (s *MySQLStore) GetSlot(ctx context.Context, slotID string) (model.Slot, error) {
	var slot model.Slot
	err := s.db.QueryRowContext(ctx, SQLSelectSlot, slotID).Scan(
		&slot.ID, &slot.StoreID, &slot.ServiceItemID, &slot.StartAt, &slot.EndAt,
		&slot.Capacity, &slot.OccupiedCount)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Slot{}, ErrSlotNotFound
	}
	return slot, err
}

func (s *MySQLStore) CreateAppointment(ctx context.Context, appt *model.Appointment) error {
	if appt == nil {
		return errors.New("预约不能为空")
	}
	if err := appt.Validate(); err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// 规则 1：原子条件更新占名额，以影响行数判定成败。
	res, err := tx.ExecContext(ctx, SQLOccupySlot, appt.SlotID)
	if err != nil {
		return fmt.Errorf("原子占名额失败: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrSlotFull
	}

	// 规则 1：uk_pet_slot_active 唯一索引保证同一宠物同一时段只有一条有效预约。
	_, err = tx.ExecContext(ctx, SQLInsertAppointment, appt.ID, appt.StoreID, appt.SlotID,
		appt.UserID, appt.PetID, appt.Status, appt.AmountFen, appt.CreatedAt,
		appt.PayDeadlineAt, appt.Version)
	if err != nil {
		return fmt.Errorf("写预约主记录失败: %w", err)
	}

	event := model.EventLog{
		ID:            "EVT-" + appt.ID,
		AppointmentID: appt.ID,
		Action:        string(model.EventCreate),
		FromStatus:    model.StatusNone,
		ToStatus:      appt.Status,
		Detail:        "创建预约并占用名额",
		CreatedAt:     appt.CreatedAt,
	}
	_, err = tx.ExecContext(ctx, SQLInsertEvent, event.ID, event.AppointmentID, event.Action,
		event.FromStatus, event.ToStatus, event.ActorRole, event.ActorStoreID,
		event.Detail, event.CreatedAt)
	if err != nil {
		return fmt.Errorf("写事件流水失败: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	// 仅在事务提交成功后失效缓存；任何回滚路径都不得误失效。
	s.invalidateSlot(ctx, appt.SlotID)
	return nil
}

func (s *MySQLStore) GetAppointment(ctx context.Context, appointmentID string) (model.Appointment, error) {
	var appt model.Appointment
	var createdAt, payDeadlineAt time.Time
	err := s.db.QueryRowContext(ctx, SQLSelectAppointment, appointmentID).Scan(
		&appt.ID, &appt.StoreID, &appt.SlotID, &appt.UserID, &appt.PetID,
		&appt.Status, &appt.AmountFen, &createdAt, &payDeadlineAt, &appt.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Appointment{}, ErrAppointmentNotFound
	}
	if err != nil {
		return model.Appointment{}, err
	}
	appt.CreatedAt = createdAt
	appt.PayDeadlineAt = payDeadlineAt
	return appt, nil
}

func (s *MySQLStore) HasActiveAppointment(ctx context.Context, petID, slotID string) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, SQLCountActive, petID, slotID).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *MySQLStore) Transition(ctx context.Context, appointmentID string, from model.Status, event model.Event, version int64) (model.Status, int64, error) {
	to, err := model.Next(from, event)
	if err != nil {
		return "", 0, err
	}
	var active any = 1
	if !model.OccupiesSlot(to) {
		active = nil // 多个 NULL 不触发唯一索引，终态预约不再占唯一性（SRS 13 章 A4）
	}
	res, err := s.db.ExecContext(ctx, SQLTransition, to, active, appointmentID, from, version)
	if err != nil {
		return "", 0, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return "", 0, err
	}
	if affected == 0 {
		return "", 0, ErrVersionConflict
	}
	return to, version + 1, nil
}

func (s *MySQLStore) ReleaseSlot(ctx context.Context, appointmentID string) error {
	// 尽力取时段用于缓存失效；查询失败不阻塞释放，正确性由条件更新与 slot_released 守卫保证。
	slotID := ""
	if appt, err := s.GetAppointment(ctx, appointmentID); err == nil {
		slotID = appt.SlotID
	}
	res, err := s.db.ExecContext(ctx, SQLReleaseSlot, appointmentID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrAlreadyReleased
	}
	if slotID != "" {
		s.invalidateSlot(ctx, slotID)
	}
	return nil
}

func (s *MySQLStore) AppendEvent(ctx context.Context, log model.EventLog) error {
	_, err := s.db.ExecContext(ctx, SQLInsertEvent, log.ID, log.AppointmentID, log.Action,
		log.FromStatus, log.ToStatus, log.ActorRole, log.ActorStoreID, log.Detail, log.CreatedAt)
	return err
}

func (s *MySQLStore) ListEvents(ctx context.Context) ([]model.EventLog, error) {
	rows, err := s.db.QueryContext(ctx, SQLSelectEvents)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []model.EventLog
	for rows.Next() {
		var log model.EventLog
		if err := rows.Scan(&log.ID, &log.AppointmentID, &log.Action, &log.FromStatus,
			&log.ToStatus, &log.ActorRole, &log.ActorStoreID, &log.Detail, &log.CreatedAt); err != nil {
			return nil, err
		}
		logs = append(logs, log)
	}
	return logs, rows.Err()
}

func (s *MySQLStore) Reset(ctx context.Context) error {
	for _, query := range []string{SQLResetEvents, SQLResetAppointments, SQLResetSlots} {
		if _, err := s.db.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	s.invalidateAll(ctx)
	return nil
}
