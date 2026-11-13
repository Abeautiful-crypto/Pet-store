package store

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Abeautiful-crypto/Pet-store/internal/model"
)

// MemoryStore 是演示原型期的内存回退实现，与 MySQL 实现同一套 StateStore 契约。
// 所有写操作在同一把锁内完成"先校验、后一次性写入"，等价于事务的原子性。
type MemoryStore struct {
	mu           sync.Mutex
	slots        map[string]model.Slot
	appointments map[string]model.Appointment
	released     map[string]bool
	events       []model.EventLog
}

// NewMemoryStore 创建空的内存存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		slots:        map[string]model.Slot{},
		appointments: map[string]model.Appointment{},
		released:     map[string]bool{},
	}
}

var _ StateStore = (*MemoryStore)(nil)

func (s *MemoryStore) CreateSlot(_ context.Context, slot model.Slot) error {
	if err := slot.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.slots[slot.ID]; exists {
		return fmt.Errorf("时段已存在: %s", slot.ID)
	}
	s.slots[slot.ID] = slot
	return nil
}

func (s *MemoryStore) GetSlot(_ context.Context, slotID string) (model.Slot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	slot, ok := s.slots[slotID]
	if !ok {
		return model.Slot{}, ErrSlotNotFound
	}
	return slot, nil
}

func (s *MemoryStore) CreateAppointment(_ context.Context, appt *model.Appointment) error {
	if appt == nil {
		return errors.New("预约不能为空")
	}
	if err := appt.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// 事务等价：以下任一校验失败都不写入任何状态。
	slot, ok := s.slots[appt.SlotID]
	if !ok {
		return ErrSlotNotFound
	}
	if slot.OccupiedCount >= slot.Capacity {
		return ErrSlotFull
	}
	for _, existing := range s.appointments {
		if existing.PetID == appt.PetID && existing.SlotID == appt.SlotID && model.IsActive(existing.Status) {
			return ErrDuplicateActive
		}
	}

	slot.OccupiedCount++
	s.slots[slot.ID] = slot
	s.appointments[appt.ID] = *appt
	return nil
}

func (s *MemoryStore) GetAppointment(_ context.Context, appointmentID string) (model.Appointment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	appt, ok := s.appointments[appointmentID]
	if !ok {
		return model.Appointment{}, ErrAppointmentNotFound
	}
	return appt, nil
}

func (s *MemoryStore) HasActiveAppointment(_ context.Context, petID, slotID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, appt := range s.appointments {
		if appt.PetID == petID && appt.SlotID == slotID && model.IsActive(appt.Status) {
			return true, nil
		}
	}
	return false, nil
}

func (s *MemoryStore) Transition(_ context.Context, appointmentID string, from model.Status, event model.Event, version int64) (model.Status, int64, error) {
	to, err := model.Next(from, event)
	if err != nil {
		return "", 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	appt, ok := s.appointments[appointmentID]
	if !ok {
		return "", 0, ErrAppointmentNotFound
	}
	if appt.Status != from || appt.Version != version {
		return "", 0, ErrVersionConflict
	}
	appt.Status = to
	appt.Version++
	s.appointments[appointmentID] = appt
	return to, appt.Version, nil
}

func (s *MemoryStore) ReleaseSlot(_ context.Context, appointmentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	appt, ok := s.appointments[appointmentID]
	if !ok {
		return ErrAppointmentNotFound
	}
	if s.released[appointmentID] {
		return ErrAlreadyReleased
	}
	slot, ok := s.slots[appt.SlotID]
	if !ok {
		return ErrSlotNotFound
	}
	if slot.OccupiedCount > 0 {
		slot.OccupiedCount--
		s.slots[slot.ID] = slot
	}
	s.released[appointmentID] = true
	return nil
}

func (s *MemoryStore) AppendEvent(_ context.Context, log model.EventLog) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, log)
	return nil
}

func (s *MemoryStore) ListEvents(_ context.Context) ([]model.EventLog, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.EventLog, len(s.events))
	copy(out, s.events)
	return out, nil
}

func (s *MemoryStore) Reset(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.slots = map[string]model.Slot{}
	s.appointments = map[string]model.Appointment{}
	s.released = map[string]bool{}
	s.events = nil
	return nil
}
