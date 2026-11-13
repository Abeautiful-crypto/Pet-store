package cache

import (
	"context"
	"encoding/json"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisCache 承接名额读缓存、支付幂等去重与登录会话（tech-stack.md §2）。
// 真相源永远在 MySQL：Redis 只缓存读、不计数；不可用或报错时一律降级，不阻塞业务。
type RedisCache struct {
	client    *redis.Client
	available atomic.Bool
}

// NewRedisCache 连接本地 Redis；Ping 失败即标记不可用，服务仍可启动。
func NewRedisCache(addr string) *RedisCache {
	if addr == "" {
		addr = "127.0.0.1:6379"
	}
	client := redis.NewClient(&redis.Options{
		Addr:         addr,
		DialTimeout:  500 * time.Millisecond,
		ReadTimeout:  500 * time.Millisecond,
		WriteTimeout: 500 * time.Millisecond,
		MaxRetries:   0,
	})
	c := &RedisCache{client: client}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c.available.Store(client.Ping(ctx).Err() == nil)
	return c
}

// newRedisCacheWithClient 供单测注入不可用/故障客户端。
func newRedisCacheWithClient(client *redis.Client, available bool) *RedisCache {
	c := &RedisCache{client: client}
	c.available.Store(available)
	return c
}

func (c *RedisCache) Available() bool      { return c.available.Load() }
func (c *RedisCache) SetAvailable(ok bool) { c.available.Store(ok) }
func (c *RedisCache) Close() error         { return c.client.Close() }

// degrade 熔断：Redis 故障后所有读降级为 miss，直至外部重新 SetAvailable(true)。
func (c *RedisCache) degrade() { c.available.Store(false) }

func (c *RedisCache) GetSlot(ctx context.Context, slotID string) (int, bool) {
	if !c.Available() {
		return 0, false
	}
	text, err := c.client.Get(ctx, "slot:"+slotID).Result()
	if err != nil {
		if err != redis.Nil {
			c.degrade()
		}
		return 0, false
	}
	occupied, err := strconv.Atoi(text)
	if err != nil {
		return 0, false
	}
	return occupied, true
}

func (c *RedisCache) SetSlot(ctx context.Context, slotID string, occupied int, ttl time.Duration) {
	if !c.Available() {
		return
	}
	if err := c.client.Set(ctx, "slot:"+slotID, occupied, ttl).Err(); err != nil {
		c.degrade()
	}
}

func (c *RedisCache) InvalidateSlot(ctx context.Context, slotID string) error {
	if !c.Available() {
		return nil
	}
	if err := c.client.Del(ctx, "slot:"+slotID).Err(); err != nil {
		c.degrade()
	}
	return nil
}

func (c *RedisCache) InvalidateAll(ctx context.Context) error {
	if !c.Available() {
		return nil
	}
	for _, prefix := range []string{"slot:", "pay_dedup:", "sess:"} {
		iter := c.client.Scan(ctx, 0, prefix+"*", 100).Iterator()
		var keys []string
		for iter.Next(ctx) {
			keys = append(keys, iter.Val())
		}
		if err := iter.Err(); err != nil {
			c.degrade()
			return nil
		}
		if len(keys) > 0 {
			if err := c.client.Del(ctx, keys...).Err(); err != nil {
				c.degrade()
				return nil
			}
		}
	}
	return nil
}

func (c *RedisCache) Dedup(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	if !c.Available() {
		// 降级时不阻塞业务，幂等由 MySQL 唯一索引兜底。
		return true, nil
	}
	acquired, err := c.client.SetNX(ctx, "pay_dedup:"+key, 1, ttl).Result()
	if err != nil {
		c.degrade()
		return true, nil
	}
	return acquired, nil
}

func (c *RedisCache) GetSession(ctx context.Context, token string) (Session, bool) {
	if !c.Available() {
		return Session{}, false
	}
	text, err := c.client.Get(ctx, "sess:"+token).Result()
	if err != nil {
		if err != redis.Nil {
			c.degrade()
		}
		return Session{}, false
	}
	var session Session
	if err := json.Unmarshal([]byte(text), &session); err != nil {
		return Session{}, false
	}
	return session, true
}

func (c *RedisCache) SetSession(ctx context.Context, token string, session Session, ttl time.Duration) {
	if !c.Available() {
		return
	}
	body, err := json.Marshal(session)
	if err != nil {
		return
	}
	if err := c.client.Set(ctx, "sess:"+token, body, ttl).Err(); err != nil {
		c.degrade()
	}
}

var _ Cache = (*RedisCache)(nil)
