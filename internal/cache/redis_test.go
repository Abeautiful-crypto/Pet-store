package cache

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func badRedisClient() *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr: "127.0.0.1:1", DialTimeout: 30 * time.Millisecond,
		ReadTimeout: 30 * time.Millisecond, WriteTimeout: 30 * time.Millisecond,
		MaxRetries: 0,
	})
}

func TestRedis_NewWithBadAddrMarksUnavailable(t *testing.T) {
	c := NewRedisCache("127.0.0.1:1")
	defer c.Close()
	if c.Available() {
		t.Fatal("Ping 失败应标记不可用")
	}
}

func TestRedis_UnavailableDegradesAllOperations(t *testing.T) {
	c := newRedisCacheWithClient(badRedisClient(), false)
	defer c.Close()
	ctx := context.Background()
	if _, hit := c.GetSlot(ctx, "SL1"); hit {
		t.Fatal("不可用时读缓存必须降级为 miss")
	}
	c.SetSlot(ctx, "SL1", 2, time.Minute)
	if ok, err := c.Dedup(ctx, "k", time.Minute); !ok || err != nil {
		t.Fatalf("降级时去重应放行: ok=%v err=%v", ok, err)
	}
	if err := c.InvalidateSlot(ctx, "SL1"); err != nil {
		t.Fatal(err)
	}
	if err := c.InvalidateAll(ctx); err != nil {
		t.Fatal(err)
	}
	c.SetSession(ctx, "t", Session{UserID: "U1"}, time.Minute)
	if _, hit := c.GetSession(ctx, "t"); hit {
		t.Fatal("降级时不得读回会话")
	}
	c.SetAvailable(true)
	if !c.Available() {
		t.Fatal("恢复后应重新可用")
	}
}

func TestRedis_ConnectionErrorTriggersDegrade(t *testing.T) {
	c := newRedisCacheWithClient(badRedisClient(), true)
	defer c.Close()
	ctx := context.Background()
	if _, hit := c.GetSlot(ctx, "SL1"); hit {
		t.Fatal("连接错误不应命中缓存")
	}
	if c.Available() {
		t.Fatal("连接错误应熔断降级")
	}
	c.available.Store(true)
	c.SetSlot(ctx, "SL1", 1, time.Minute)
	if c.Available() {
		t.Fatal("写入错误也应熔断")
	}
	c.available.Store(true)
	if err := c.InvalidateSlot(ctx, "SL1"); err != nil {
		t.Fatal(err)
	}
	if c.Available() {
		t.Fatal("失效错误应熔断")
	}
	c.available.Store(true)
	if ok, err := c.Dedup(ctx, "k", time.Minute); !ok || err != nil {
		t.Fatal("去重错误应降级放行")
	}
	if c.Available() {
		t.Fatal("去重错误应熔断")
	}
	c.available.Store(true)
	c.SetSession(ctx, "t", Session{UserID: "U1"}, time.Minute)
	if c.Available() {
		t.Fatal("会话写入错误应熔断")
	}
	c.available.Store(true)
	if _, hit := c.GetSession(ctx, "t"); hit {
		t.Fatal("会话读取错误不应命中")
	}
}

func TestRedis_RealIntegrationCacheAsideAndDedup(t *testing.T) {
	addr := os.Getenv("PETSTORE_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("未设置 PETSTORE_TEST_REDIS_ADDR，跳过真实 Redis 集成测试")
	}
	c := NewRedisCache(addr)
	defer c.Close()
	if !c.Available() {
		t.Fatal("本地 Redis 应可用")
	}
	ctx := context.Background()
	if err := c.InvalidateAll(ctx); err != nil {
		t.Fatal(err)
	}

	c.SetSlot(ctx, "SL-TEST", 2, time.Minute)
	if occupied, hit := c.GetSlot(ctx, "SL-TEST"); !hit || occupied != 2 {
		t.Fatalf("Cache-Aside 读失败: %d %v", occupied, hit)
	}
	if err := c.InvalidateSlot(ctx, "SL-TEST"); err != nil {
		t.Fatal(err)
	}
	if _, hit := c.GetSlot(ctx, "SL-TEST"); hit {
		t.Fatal("写提交后必须失效变化的 slot:{id}")
	}

	first, err := c.Dedup(ctx, "dedup-test", time.Minute)
	if err != nil || !first {
		t.Fatalf("首次 SETNX 应成功: %v %v", first, err)
	}
	second, err := c.Dedup(ctx, "dedup-test", time.Minute)
	if err != nil || second {
		t.Fatalf("重复通知应被去重: %v %v", second, err)
	}

	c.SetSession(ctx, "sess-test", Session{UserID: "U1", Role: "manager", StoreID: "STORE-1"}, time.Minute)
	if session, hit := c.GetSession(ctx, "sess-test"); !hit || session.UserID != "U1" {
		t.Fatalf("会话应可回读: %+v %v", session, hit)
	}

	// 非法缓存值不得污染展示层。
	if err := c.client.Set(ctx, "slot:bad", "not-a-number", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if _, hit := c.GetSlot(ctx, "bad"); hit {
		t.Fatal("非法数值应视为未命中")
	}
	if err := c.client.Set(ctx, "sess:bad", "not-json", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if _, hit := c.GetSession(ctx, "bad"); hit {
		t.Fatal("非法会话应视为未命中")
	}

	// TTL 过期后可再次获取去重键。
	if err := c.client.Del(ctx, "pay_dedup:ttl-test").Err(); err != nil {
		t.Fatal(err)
	}
	if ok, _ := c.Dedup(ctx, "ttl-test", 50*time.Millisecond); !ok {
		t.Fatal("首次获取应成功")
	}
	time.Sleep(80 * time.Millisecond)
	if ok, _ := c.Dedup(ctx, "ttl-test", 50*time.Millisecond); !ok {
		t.Fatal("TTL 过期后应可再次获取")
	}

	if err := c.InvalidateAll(ctx); err != nil {
		t.Fatal(err)
	}
	if _, hit := c.GetSlot(ctx, "SL-TEST"); hit {
		t.Fatal("Reset 应失效全部缓存")
	}
}
