package cache

import (
	"context"
	"testing"
	"time"

	"github.com/Abeautiful-crypto/Pet-store/internal/store"
)

// 缓存必须实现存储层的写失效钩子（tech-stack.md §2.2）。
var _ store.SlotCacheInvalidator = (*MemoryCache)(nil)

func fixedClock() (func() time.Time, func(time.Duration)) {
	now := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	return func() time.Time { return now }, func(d time.Duration) { now = now.Add(d) }
}

func TestCache_ImplementsStoreInvalidator(t *testing.T) {
	var inv store.SlotCacheInvalidator = NewMemoryCache(nil)
	if err := inv.InvalidateSlot(context.Background(), "SL1"); err != nil {
		t.Fatalf("失效单个时段不应报错: %v", err)
	}
	if err := inv.InvalidateAll(context.Background()); err != nil {
		t.Fatalf("失效全部不应报错: %v", err)
	}
}

func TestCache_CacheAsideSlotRead(t *testing.T) {
	ctx := context.Background()
	c := NewMemoryCache(nil)
	if _, hit := c.GetSlot(ctx, "SL1"); hit {
		t.Fatal("未回写前不应命中名额缓存")
	}
	c.SetSlot(ctx, "SL1", 2, time.Minute)
	occupied, hit := c.GetSlot(ctx, "SL1")
	if !hit || occupied != 2 {
		t.Fatalf("Cache-Aside 回写后应命中，occupied=%d hit=%v", occupied, hit)
	}
}

func TestCache_InvalidateSlotAndAll(t *testing.T) {
	ctx := context.Background()
	c := NewMemoryCache(nil)
	c.SetSlot(ctx, "SL1", 1, time.Minute)
	c.SetSlot(ctx, "SL2", 2, time.Minute)
	c.SetSession(ctx, "t1", Session{UserID: "U1", Role: "frontdesk", StoreID: "STORE-1"}, time.Minute)

	if err := c.InvalidateSlot(ctx, "SL1"); err != nil {
		t.Fatal(err)
	}
	if _, hit := c.GetSlot(ctx, "SL1"); hit {
		t.Fatal("失效后 SL1 应未命中")
	}
	if _, hit := c.GetSlot(ctx, "SL2"); !hit {
		t.Fatal("失效单个时段不得波及其他时段")
	}
	if err := c.InvalidateAll(ctx); err != nil {
		t.Fatal(err)
	}
	if _, hit := c.GetSlot(ctx, "SL2"); hit {
		t.Fatal("Reset 后所有名额缓存应清空")
	}
	if _, hit := c.GetSession(ctx, "t1"); hit {
		t.Fatal("Reset 后会话缓存应清空")
	}
}

func TestCache_DedupSETNXWithTTL(t *testing.T) {
	ctx := context.Background()
	clock, advance := fixedClock()
	c := NewMemoryCache(clock)

	first, err := c.Dedup(ctx, "pay:notify-1", 5*time.Minute)
	if err != nil || !first {
		t.Fatalf("首次 SETNX 应成功，first=%v err=%v", first, err)
	}
	second, err := c.Dedup(ctx, "pay:notify-1", 5*time.Minute)
	if err != nil || second {
		t.Fatalf("重复通知应被去重，second=%v err=%v", second, err)
	}
	advance(6 * time.Minute)
	third, err := c.Dedup(ctx, "pay:notify-1", 5*time.Minute)
	if err != nil || !third {
		t.Fatalf("TTL 过期后应可再次获取，third=%v err=%v", third, err)
	}
}

func TestCache_SessionRoundTripWithTTL(t *testing.T) {
	ctx := context.Background()
	clock, advance := fixedClock()
	c := NewMemoryCache(clock)
	want := Session{UserID: "U1", Role: "frontdesk", StoreID: "STORE-1"}
	c.SetSession(ctx, "token-1", want, time.Minute)

	got, hit := c.GetSession(ctx, "token-1")
	if !hit || got != want {
		t.Fatalf("会话应可回读，got=%+v hit=%v", got, hit)
	}
	advance(2 * time.Minute)
	if _, hit := c.GetSession(ctx, "token-1"); hit {
		t.Fatal("会话 TTL 过期后应未命中")
	}
}

func TestCache_RedisUnavailableDegrades(t *testing.T) {
	ctx := context.Background()
	c := NewMemoryCache(nil)
	c.SetSlot(ctx, "SL1", 2, time.Minute)
	c.SetAvailable(false)

	if c.Available() {
		t.Fatal("标记为不可用后 Available 应为 false")
	}
	if _, hit := c.GetSlot(ctx, "SL1"); hit {
		t.Fatal("Redis 不可用时读缓存必须降级为未命中，不得返回旧名额")
	}
	ok, err := c.Dedup(ctx, "pay:notify-2", time.Minute)
	if err != nil || !ok {
		t.Fatalf("降级时不得阻塞业务，幂等由 MySQL 唯一索引兜底: ok=%v err=%v", ok, err)
	}
	c.SetSession(ctx, "t2", Session{UserID: "U2"}, time.Minute)
	if _, hit := c.GetSession(ctx, "t2"); hit {
		t.Fatal("降级时不得写入会话缓存")
	}
	if err := c.InvalidateSlot(ctx, "SL1"); err != nil {
		t.Fatalf("降级时失效操作不得报错: %v", err)
	}
	if err := c.InvalidateAll(ctx); err != nil {
		t.Fatalf("降级时失效全部不得报错: %v", err)
	}

	c.SetAvailable(true)
	if !c.Available() {
		t.Fatal("恢复后应重新可用")
	}
	c.SetSlot(ctx, "SL1", 1, time.Minute)
	if occupied, hit := c.GetSlot(ctx, "SL1"); !hit || occupied != 1 {
		t.Fatal("恢复后缓存应可正常读写")
	}
}
