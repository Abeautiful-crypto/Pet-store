package app

import (
	"errors"
	"testing"
)

func baseEnv() map[string]string {
	return map[string]string{
		"APP_ENV":      EnvDev,
		"STORE_DRIVER": StoreMemory,
		"HTTP_ADDR":    ":8080",
	}
}

func TestApp_DevelopmentMemoryStoreAllowed(t *testing.T) {
	cfg, err := Parse(baseEnv())
	if err != nil {
		t.Fatalf("开发模式内存回退应允许: %v", err)
	}
	if cfg.Env != EnvDev || cfg.StoreDriver != StoreMemory || cfg.HTTPAddr != ":8080" {
		t.Fatalf("配置解析结果不符: %+v", cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("合法配置应通过 Validate: %v", err)
	}
}

func TestApp_ProductionRejectsMemoryStore(t *testing.T) {
	env := baseEnv()
	env["APP_ENV"] = EnvProd
	if _, err := Parse(env); !errors.Is(err, ErrProductionMemoryStore) {
		t.Fatalf("生产模式必须拒绝内存存储，实际: %v", err)
	}
}

func TestApp_RequiredConfigMissing(t *testing.T) {
	cases := []struct {
		name string
		key  string
	}{
		{"缺少 APP_ENV", "APP_ENV"},
		{"缺少 STORE_DRIVER", "STORE_DRIVER"},
		{"缺少 HTTP_ADDR", "HTTP_ADDR"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := baseEnv()
			delete(env, tc.key)
			if _, err := Parse(env); !errors.Is(err, ErrMissingConfig) {
				t.Fatalf("必需配置缺失应报 ErrMissingConfig，实际: %v", err)
			}
		})
	}
}

func TestApp_MySQLDriverRequiresDSN(t *testing.T) {
	env := baseEnv()
	env["STORE_DRIVER"] = StoreMySQL
	if _, err := Parse(env); !errors.Is(err, ErrMissingConfig) {
		t.Fatalf("MySQL 存储必须配置 MYSQL_DSN，实际: %v", err)
	}
}

func TestApp_ProductionMySQLAllowed(t *testing.T) {
	env := baseEnv()
	env["APP_ENV"] = EnvProd
	env["STORE_DRIVER"] = StoreMySQL
	env["MYSQL_DSN"] = "root:root@tcp(127.0.0.1:3306)/petstore"
	cfg, err := Parse(env)
	if err != nil {
		t.Fatalf("生产模式 MySQL 真相源应允许: %v", err)
	}
	if cfg.MySQLDSN == "" {
		t.Fatal("MySQLDSN 未写入配置")
	}
}

func TestApp_UnknownValuesRejected(t *testing.T) {
	cases := []struct {
		name string
		key  string
		val  string
	}{
		{"未知运行环境", "APP_ENV", "staging"},
		{"未知存储驱动", "STORE_DRIVER", "sqlite"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := baseEnv()
			env[tc.key] = tc.val
			if _, err := Parse(env); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("未知取值应报 ErrInvalidConfig，实际: %v", err)
			}
		})
	}
}
