// Package cache 实现名额读缓存、支付幂等短期去重、登录会话与不可用降级。
// 真相源永远在 MySQL：Redis 只缓存读、不计数；幂等由 MySQL 唯一索引兜底
// （.agent/backend/tech-stack.md §2，禁止用 Redis 计数代替 MySQL 名额）。
package cache

import (
	"context"
	"sync"
	"time"
)

// Session 登录态，携带角色与门店归属，供核销归属校验（规则 5 / FR-19）。
type Session struct {
	UserID  string
	Role    string
	StoreID string
}

// Cache 是缓存层的统一契约；真实 Redis 适配器与内存实现共用此接口。
type Cache interface {
	// GetSlot / SetSlot 支撑 Cache-Aside 读：未命中查 MySQL 并回写（带 TTL）。
	GetSlot(ctx context.Context, slotID string) (int, bool)
	SetSlot(ctx context.Context, slotID string, occupied int, ttl time.Duration)
	// 写失效：存储层事务提交成功后调用（tech-stack.md §2.2）。
	InvalidateSlot(ctx context.Context, slotID string) error
	InvalidateAll(ctx context.Context) error
	// Dedup 等价于 SETNX + TTL，重复通知只放行一次（规则 2 / FR-05）。
	Dedup(ctx context.Context, key string, ttl time.Duration) (bool, error)
	GetSession(ctx context.Context, token string) (Session, bool)
	SetSession(ctx context.Context, token string, session Session, ttl time.Duration)
	Available() bool
}

type timedValue[T any] struct {
	value     T
	expiresAt time.Time
}

// MemoryCache 是零外部依赖的缓存实现，单测与演示环境使用。
// 注入 now 便于验证 TTL 而不真实等待；SetAvailable(false) 模拟 Redis 宕机降级。
type MemoryCache struct {
	mu        sync.Mutex
	now       func() time.Time
	available bool
	slots     map[string]timedValue[int]
	dedup     map[string]time.Time
	sessions  map[string]timedValue[Session]
}

// NewMemoryCache 创建可用缓存；now 为 nil 时使用系统时钟。
func NewMemoryCache(now func() time.Time) *MemoryCache {
	if now == nil {
		now = time.Now
	}
	return &MemoryCache{
		now:       now,
		available: true,
		slots:     map[string]timedValue[int]{},
		dedup:     map[string]time.Time{},
		sessions:  map[string]timedValue[Session]{},
	}
}

var _ Cache = (*MemoryCache)(nil)

func (c *MemoryCache) clearLocked() {
	c.slots = map[string]timedValue[int]{}
	c.dedup = map[string]time.Time{}
	c.sessions = map[string]timedValue[Session]{}
}

func (c *MemoryCache) fresh(expiresAt time.Time) bool {
	return c.now().Before(expiresAt)
}

// Available 报告缓存是否可用；不可用时所有读一律降级为 miss（不返回旧名额）。
func (c *MemoryCache) Available() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.available
}

// SetAvailable 模拟 Redis 连接状态切换；断开时清空本地缓存，避免恢复后读到旧值。
func (c *MemoryCache) SetAvailable(available bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.available = available
	if !available {
		c.clearLocked()
	}
}

func (c *MemoryCache) GetSlot(_ context.Context, slotID string) (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.available {
		return 0, false
	}
	entry, ok := c.slots[slotID]
	if !ok || !c.fresh(entry.expiresAt) {
		delete(c.slots, slotID)
		return 0, false
	}
	return entry.value, true
}

func (c *MemoryCache) SetSlot(_ context.Context, slotID string, occupied int, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.available {
		return
	}
	c.slots[slotID] = timedValue[int]{value: occupied, expiresAt: c.now().Add(ttl)}
}

func (c *MemoryCache) InvalidateSlot(_ context.Context, slotID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.slots, slotID)
	return nil
}

func (c *MemoryCache) InvalidateAll(_ context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.clearLocked()
	return nil
}

func (c *MemoryCache) Dedup(_ context.Context, key string, ttl time.Duration) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// 降级：不阻塞业务，幂等仍由 MySQL 的 notify_key / channel_trade_no 唯一索引兜底。
	if !c.available {
		return true, nil
	}
	if expiresAt, exists := c.dedup[key]; exists && c.fresh(expiresAt) {
		return false, nil
	}
	c.dedup[key] = c.now().Add(ttl)
	return true, nil
}

func (c *MemoryCache) GetSession(_ context.Context, token string) (Session, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.available {
		return Session{}, false
	}
	entry, ok := c.sessions[token]
	if !ok || !c.fresh(entry.expiresAt) {
		delete(c.sessions, token)
		return Session{}, false
	}
	return entry.value, true
}

func (c *MemoryCache) SetSession(_ context.Context, token string, session Session, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.available {
		return
	}
	c.sessions[token] = timedValue[Session]{value: session, expiresAt: c.now().Add(ttl)}
}
