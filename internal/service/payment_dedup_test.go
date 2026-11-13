package service

import (
	"context"
	"testing"
	"time"

	"github.com/Abeautiful-crypto/Pet-store/internal/cache"
	"github.com/Abeautiful-crypto/Pet-store/internal/model"
	"github.com/Abeautiful-crypto/Pet-store/internal/store"
)

// TestService_ConfirmPaymentDedup 验证 Redis/Memory 缓存去重（SETNX+TTL）接入 ConfirmPayment：
// 重复 notify_key 被前置拦截为幂等命中，且不重复记支付单；缓存降级时由账本兜底。
func TestService_ConfirmPaymentDedup(t *testing.T) {
	ctx := context.Background()
	clock := &mutableClock{now: fixedNow}
	memCache := cache.NewMemoryCache(nil)

	svc := New(Config{
		Store:   store.NewMemoryStore(),
		Now:     func() time.Time { return clock.now },
		Ledger:  NewLedger(),
		Deduper: memCache,
		OwnsPet: func(string, string) bool { return true },
	})

	if err := svc.PublishSlot(ctx, serviceSlot("SLD")); err != nil {
		t.Fatal(err)
	}
	appt, err := svc.CreateAppointment(ctx, "U1", "P1", "SLD")
	if err != nil {
		t.Fatal(err)
	}

	// 第一次通知：去重放行，正常确认。
	if err := svc.ConfirmPayment(ctx, appt.ID, "NOTIFY-D", "TRADE-D"); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.store.GetAppointment(ctx, appt.ID); got.Status != model.StatusConfirmed {
		t.Fatalf("应为已确认，实际 %s", got.Status)
	}

	// 重复同一 notify_key：缓存去重拦截，直接幂等命中，不报错。
	if err := svc.ConfirmPayment(ctx, appt.ID, "NOTIFY-D", "TRADE-D2"); err != nil {
		t.Fatalf("重复通知应被去重拦截为幂等，实际: %v", err)
	}
	if n := len(svc.ledger.Payments()); n != 1 {
		t.Fatalf("去重后只应 1 条支付单，实际 %d", n)
	}

	// 降级场景：缓存不可用（Available=false）时 Dedup 放行，幂等由账本兜底。
	memCache.SetAvailable(false)
	if err := svc.ConfirmPayment(ctx, appt.ID, "NOTIFY-D", "TRADE-D3"); err != nil {
		t.Fatalf("降级后重复通知应由账本兜底为幂等: %v", err)
	}
	if n := len(svc.ledger.Payments()); n != 1 {
		t.Fatalf("降级重复仍应 1 条，实际 %d", n)
	}
}
