package store

import (
	"context"
	"time"

	"github.com/Abeautiful-crypto/Pet-store/internal/model"
)

// SQLSelectAppointments 供 5 分钟占位超时扫描与店长日清查询使用。
// 超时关闭的正确性以 MySQL pay_deadline_at 扫描为准，Redis 只做辅助（tech-stack.md §2.1）。
const SQLSelectAppointments = `SELECT appointment_id, store_id, slot_id, user_id, pet_id, status, amount_fen, created_at, pay_deadline_at, version FROM appointments ORDER BY created_at`

func (s *MemoryStore) ListAppointments(_ context.Context) ([]model.Appointment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.Appointment, 0, len(s.appointments))
	for _, appt := range s.appointments {
		out = append(out, appt)
	}
	return out, nil
}

func (s *MySQLStore) ListAppointments(ctx context.Context) ([]model.Appointment, error) {
	rows, err := s.db.QueryContext(ctx, SQLSelectAppointments)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.Appointment
	for rows.Next() {
		var appt model.Appointment
		var createdAt, payDeadlineAt time.Time
		if err := rows.Scan(&appt.ID, &appt.StoreID, &appt.SlotID, &appt.UserID,
			&appt.PetID, &appt.Status, &appt.AmountFen, &createdAt, &payDeadlineAt,
			&appt.Version); err != nil {
			return nil, err
		}
		appt.CreatedAt = createdAt
		appt.PayDeadlineAt = payDeadlineAt
		out = append(out, appt)
	}
	return out, rows.Err()
}
