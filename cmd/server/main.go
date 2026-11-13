// Command server 是宠物门店预约服务的入口装配：配置守卫、存储/缓存、预置数据、HTTP 路由。
// 业务规则不在入口实现，全部委托 internal/service。
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Abeautiful-crypto/Pet-store/internal/api"
	"github.com/Abeautiful-crypto/Pet-store/internal/app"
	"github.com/Abeautiful-crypto/Pet-store/internal/cache"
	"github.com/Abeautiful-crypto/Pet-store/internal/model"
	"github.com/Abeautiful-crypto/Pet-store/internal/service"
	"github.com/Abeautiful-crypto/Pet-store/internal/store"
)

func main() {
	env := map[string]string{}
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i > 0 {
			env[kv[:i]] = kv[i+1:]
		}
	}
	cfg, err := app.Parse(env)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// 按配置装配真相源：STORE_DRIVER=mysql 时连接本地 MySQL 并执行版本化迁移；
	// dev 允许内存回退，生产模式由 app.Validate 拒绝内存存储。
	st, db, err := app.OpenStore(cfg)
	if err != nil {
		log.Fatalf("装配真相源失败: %v", err)
	}
	if db != nil {
		defer db.Close()
		if _, err := app.Migrate(context.Background(), db, "migrations"); err != nil {
			log.Fatalf("执行迁移失败: %v", err)
		}
	}
	var slotCache store.SlotCacheInvalidator
	var deduper service.Deduper
	if cfg.RedisAddr != "" {
		redisCache := cache.NewRedisCache(cfg.RedisAddr)
		defer redisCache.Close()
		slotCache = redisCache
		deduper = redisCache
		if !redisCache.Available() {
			log.Printf("Redis %s 不可用，缓存已降级（真相源仍为 MySQL，支付去重由 notify_key 唯一索引兜底）", cfg.RedisAddr)
		}
	} else {
		memoryCache := cache.NewMemoryCache(nil)
		slotCache = memoryCache
		deduper = memoryCache
	}
	if setter, ok := st.(interface {
		SetInvalidator(store.SlotCacheInvalidator)
	}); ok {
		setter.SetInvalidator(slotCache)
	}

	// 业务单据账本：MySQL 模式落库为真相源（F6.5），否则回退内存。
	var ledger service.Ledger
	if db != nil {
		ledger = service.NewMySQLLedger(db)
	} else {
		ledger = service.NewLedger()
	}

	pets := map[string]string{}
	for i := 1; i <= 9; i++ {
		pets[fmt.Sprintf("P%d", i)] = "U1"
	}
	svc := service.New(service.Config{
		Store:   st,
		Now:     time.Now,
		Ledger:  ledger,
		Deduper: deduper,
		OwnsPet: func(userID, petID string) bool {
			return pets[petID] == userID && pets[petID] != ""
		},
	})

	// 预置 3 个未来时段（SRS 3.3 / 题目 2.4）；MySQL 库内已有时段时不重复写入。
	existing, err := svc.ListSlots(context.Background())
	if err != nil {
		log.Fatalf("读取时段失败: %v", err)
	}
	if len(existing) == 0 {
		base := time.Now().Add(4 * time.Hour).Truncate(time.Hour)
		for i := 1; i <= 3; i++ {
			start := base.Add(time.Duration(i) * time.Hour)
			if err := svc.PublishSlot(context.Background(), model.Slot{
				ID:            fmt.Sprintf("SL%d", i),
				StoreID:       model.StoreID,
				ServiceItemID: model.ServiceItemID,
				StartAt:       start,
				EndAt:         start.Add(time.Hour),
				Capacity:      model.SlotCapacity,
			}); err != nil {
				log.Fatalf("预置时段失败: %v", err)
			}
		}
	}

	mux := http.NewServeMux()
	mux.Handle("/api/", api.New(svc).Handler())
	mux.Handle("/", api.Static("web"))

	log.Printf("pet-store listening on %s (env=%s store=%s)", cfg.HTTPAddr, cfg.Env, cfg.StoreDriver)
	if err := http.ListenAndServe(cfg.HTTPAddr, mux); err != nil {
		log.Fatal(err)
	}
}
