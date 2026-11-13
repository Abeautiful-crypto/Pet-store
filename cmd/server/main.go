// Command server 是宠物门店预约服务的入口装配。
// 后续阶段在此接入 HTTP 路由、存储与缓存；业务规则不在入口实现。
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/Abeautiful-crypto/Pet-store/internal/app"
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
	fmt.Printf("env=%s store=%s addr=%s\n", cfg.Env, cfg.StoreDriver, cfg.HTTPAddr)
}
