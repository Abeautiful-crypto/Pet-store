// Package app 负责配置解析与启动/生产守卫（.agent/backend/tech-stack.md §3）。
package app

import (
	"errors"
	"fmt"
)

// 运行环境与存储驱动取值。生产模式必须以 MySQL 为真相源，禁止内存回退。
const (
	EnvDev  = "dev"
	EnvProd = "prod"

	StoreMemory = "memory"
	StoreMySQL  = "mysql"
)

var (
	// ErrMissingConfig 表示必需配置缺失。
	ErrMissingConfig = errors.New("必需配置缺失")
	// ErrInvalidConfig 表示配置取值不在允许集合内。
	ErrInvalidConfig = errors.New("配置取值非法")
	// ErrProductionMemoryStore 表示生产模式误用内存回退存储。
	ErrProductionMemoryStore = errors.New("生产模式禁止内存存储")
)

// Config 是启动装配所需的配置集合。
type Config struct {
	Env         string
	StoreDriver string
	HTTPAddr    string
	MySQLDSN    string
	RedisAddr   string
}

// Parse 从环境变量映射解析配置，并立即执行启动守卫。
func Parse(env map[string]string) (Config, error) {
	cfg := Config{
		Env:         env["APP_ENV"],
		StoreDriver: env["STORE_DRIVER"],
		HTTPAddr:    env["HTTP_ADDR"],
		MySQLDSN:    env["MYSQL_DSN"],
		RedisAddr:   env["REDIS_ADDR"],
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate 执行启动守卫：必需项、枚举取值、生产模式存储真相源、MySQL DSN。
func (c Config) Validate() error {
	required := []struct {
		key string
		val string
	}{
		{"APP_ENV", c.Env},
		{"STORE_DRIVER", c.StoreDriver},
		{"HTTP_ADDR", c.HTTPAddr},
	}
	for _, item := range required {
		if item.val == "" {
			return fmt.Errorf("%w: %s 未配置", ErrMissingConfig, item.key)
		}
	}
	switch c.Env {
	case EnvDev, EnvProd:
	default:
		return fmt.Errorf("%w: APP_ENV=%s", ErrInvalidConfig, c.Env)
	}
	switch c.StoreDriver {
	case StoreMemory, StoreMySQL:
	default:
		return fmt.Errorf("%w: STORE_DRIVER=%s", ErrInvalidConfig, c.StoreDriver)
	}
	if c.Env == EnvProd && c.StoreDriver == StoreMemory {
		return fmt.Errorf("%w: 生产环境必须以 MySQL 为真相源", ErrProductionMemoryStore)
	}
	if c.StoreDriver == StoreMySQL && c.MySQLDSN == "" {
		return fmt.Errorf("%w: STORE_DRIVER=mysql 时 MYSQL_DSN 必填", ErrMissingConfig)
	}
	return nil
}
