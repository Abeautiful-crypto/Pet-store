// Package service 是题目 8 条业务规则与 SRS 20 条 FR 的唯一落点。
// 所有角色围绕同一条 appointment 主记录协作；状态变更只经 store.Transition 一个写入点。
package service

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/Abeautiful-crypto/Pet-store/internal/model"
	"github.com/Abeautiful-crypto/Pet-store/internal/store"
)

// Deduper 是支付通知去重的抽象；Redis/Memory 缓存实现都满足（SETNX + TTL 语义）。
// 重复通知只放行一次；缓存不可用时返回 (true, nil) 由 MySQL 唯一索引兜底。
type Deduper interface {
	Dedup(ctx context.Context, key string, ttl time.Duration) (bool, error)
}

var (
	// ErrPetNotOwned 表示用户试图为他人宠物预约（FR-02）。
	ErrPetNotOwned = errors.New("不能为他人宠物预约")
	// ErrSlotStarted 表示当前服务器时间已到/超过 start_at（FR-03）。
	ErrSlotStarted = errors.New("时段已开始，不可预约")
)

// Config 装配业务服务；Now 注入服务端时钟，便于测试 2 小时线/5 分钟占位。
// Ledger/Deduper 为可选依赖：未注入时 Ledger 回退内存、Deduper 为 nil（不接入缓存去重）。
type Config struct {
	Store   store.StateStore
	Now     func() time.Time
	NewID   func(prefix string) string
	OwnsPet func(userID, petID string) bool
	Ledger  Ledger
	Deduper Deduper
}

// Service 编排预约、支付、取消、退款、核销、履约、未到店与日清。
type Service struct {
	store    store.StateStore
	ledger   Ledger
	deduper  Deduper
	now      func() time.Time
	newID    func(prefix string) string
	ownsPet  func(userID, petID string) bool
	seq      atomic.Int64
	refundOK atomic.Bool
}

// New 创建业务服务；未注入的时钟/ID/宠物归属使用安全默认值。
func New(cfg Config) *Service {
	ledger := cfg.Ledger
	if ledger == nil {
		ledger = NewLedger()
	}
	s := &Service{
		store:   cfg.Store,
		ledger:  ledger,
		deduper: cfg.Deduper,
		now:     cfg.Now,
		newID:   cfg.NewID,
		ownsPet: cfg.OwnsPet,
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.newID == nil {
		s.newID = func(prefix string) string {
			return fmt.Sprintf("%s-%d", prefix, s.seq.Add(1))
		}
	}
	if s.ownsPet == nil {
		s.ownsPet = func(string, string) bool { return false }
	}
	s.refundOK.Store(true) // 模拟渠道默认退款成功
	return s
}

// SetRefundOutcome 配置模拟支付渠道的退款结果，用于制造失败与重试（FR-10 / FR-20）。
func (s *Service) SetRefundOutcome(succeeds bool) {
	s.refundOK.Store(succeeds)
}

// PublishSlot 店长发布带明确起止时间的时段（FR-01 / 规则 1）。
func (s *Service) PublishSlot(ctx context.Context, slot model.Slot) error {
	return s.store.CreateSlot(ctx, slot)
}

// CreateAppointment 用户为自己的宠物创建待支付占位预约（FR-02、FR-03 / 规则 1、2）。
func (s *Service) CreateAppointment(ctx context.Context, userID, petID, slotID string) (*model.Appointment, error) {
	if !s.ownsPet(userID, petID) {
		return nil, ErrPetNotOwned
	}
	slot, err := s.store.GetSlot(ctx, slotID)
	if err != nil {
		return nil, err
	}
	if !s.now().Before(slot.StartAt) {
		return nil, ErrSlotStarted
	}
	appt := model.NewAppointment(s.newID("APPT"), model.StoreID, slotID, userID, petID, s.now())
	if err := s.store.CreateAppointment(ctx, appt); err != nil {
		return nil, err
	}
	return appt, nil
}
