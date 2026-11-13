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

	// 演示原型期使用内存回退存储；生产模式由 app.Validate 拒绝内存存储。
	st := store.NewMemoryStore()
	st.SetInvalidator(cache.NewMemoryCache(nil))

	pets := map[string]string{}
	for i := 1; i <= 9; i++ {
		pets[fmt.Sprintf("P%d", i)] = "U1"
	}
	svc := service.New(service.Config{
		Store: st,
		Now:   time.Now,
		OwnsPet: func(userID, petID string) bool {
			return pets[petID] == userID && pets[petID] != ""
		},
	})

	// 预置 3 个未来时段（SRS 3.3 / 题目 2.4）。
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

	mux := http.NewServeMux()
	mux.Handle("/api/", api.New(svc).Handler())
	mux.Handle("/", http.FileServer(http.Dir("web")))

	log.Printf("pet-store listening on %s (env=%s store=%s)", cfg.HTTPAddr, cfg.Env, cfg.StoreDriver)
	if err := http.ListenAndServe(cfg.HTTPAddr, mux); err != nil {
		log.Fatal(err)
	}
}
