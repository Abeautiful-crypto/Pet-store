package store

import (
	"context"
	"time"

	"github.com/Abeautiful-crypto/Pet-store/internal/model"
)

const SQLSelectSlots = `SELECT slot_id, store_id, service_item_id, start_at, end_at, capacity, occupied_count FROM slots ORDER BY start_at`

func (s *MemoryStore) ListSlots(_ context.Context) ([]model.Slot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.Slot, 0, len(s.slots))
	for _, slot := range s.slots {
		out = append(out, slot)
	}
	return out, nil
}

func (s *MySQLStore) ListSlots(ctx context.Context) ([]model.Slot, error) {
	rows, err := s.db.QueryContext(ctx, SQLSelectSlots)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.Slot
	for rows.Next() {
		var slot model.Slot
		var startAt, endAt time.Time
		if err := rows.Scan(&slot.ID, &slot.StoreID, &slot.ServiceItemID,
			&startAt, &endAt, &slot.Capacity, &slot.OccupiedCount); err != nil {
			return nil, err
		}
		slot.StartAt = startAt
		slot.EndAt = endAt
		out = append(out, slot)
	}
	return out, rows.Err()
}
