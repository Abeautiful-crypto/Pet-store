package app

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Abeautiful-crypto/Pet-store/internal/store"
	_ "github.com/go-sql-driver/mysql"
)

// OpenStore 按配置装配真相源：生产模式必须 MySQL（Config.Validate 守卫），dev 可内存回退。
// 返回 *sql.DB 仅供迁移与优雅关闭使用；内存模式为 nil。
func OpenStore(cfg Config) (store.StateStore, *sql.DB, error) {
	if err := cfg.Validate(); err != nil {
		return nil, nil, err
	}
	if cfg.StoreDriver == StoreMemory {
		return store.NewMemoryStore(), nil, nil
	}
	db, err := openMySQL(cfg.MySQLDSN)
	if err != nil {
		return nil, nil, err
	}
	return store.NewMySQLStore(db), db, nil
}

func openMySQL(dsn string) (*sql.DB, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, fmt.Errorf("%w: MYSQL_DSN", ErrMissingConfig)
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("MySQL 连接失败: %w", err)
	}
	return db, nil
}

// Migrate 按文件名顺序执行版本化 up 迁移；DDL 使用 IF NOT EXISTS，可重复执行。
func Migrate(ctx context.Context, db *sql.DB, dir string) (int, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.up.sql"))
	if err != nil {
		return 0, err
	}
	sort.Strings(files)
	executed := 0
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			return executed, err
		}
		for _, statement := range splitStatements(string(body)) {
			if _, err := db.ExecContext(ctx, statement); err != nil {
				return executed, fmt.Errorf("迁移 %s 失败: %w", filepath.Base(file), err)
			}
			executed++
		}
	}
	return executed, nil
}

// splitStatements 去掉整行注释后按分号拆句（DDL 内无分号字面量）。
func splitStatements(script string) []string {
	var lines []string
	for _, line := range strings.Split(script, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "--") {
			continue
		}
		lines = append(lines, line)
	}
	var out []string
	for _, statement := range strings.Split(strings.Join(lines, "\n"), ";") {
		if trimmed := strings.TrimSpace(statement); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
