package app

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// 零外部依赖的 fake SQL driver，用于验证 Migrate 的拆句与执行路径。
var migSeq atomic.Int64

type migDriver struct{ conn *migConn }

func (d *migDriver) Open(string) (driver.Conn, error) { return d.conn, nil }

type migConn struct {
	mu      sync.Mutex
	queries []string
	fail    bool
}

func (c *migConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused") }
func (c *migConn) Close() error                        { return nil }
func (c *migConn) Begin() (driver.Tx, error)           { return migTx{}, nil }

func (c *migConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail {
		return nil, errors.New("db down")
	}
	c.queries = append(c.queries, query)
	return migResult{}, nil
}

type migResult struct{}

func (migResult) LastInsertId() (int64, error) { return 0, nil }
func (migResult) RowsAffected() (int64, error) { return 0, nil }

type migTx struct{}

func (migTx) Commit() error   { return nil }
func (migTx) Rollback() error { return nil }

func newMigDB(t *testing.T, conn *migConn) *sql.DB {
	t.Helper()
	name := fmt.Sprintf("mig_%d", migSeq.Add(1))
	sql.Register(name, &migDriver{conn: conn})
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func migrationsDir() string { return filepath.Join("..", "..", "migrations") }

func TestDeps_OpenStoreMemory(t *testing.T) {
	st, db, err := OpenStore(Config{Env: EnvDev, StoreDriver: StoreMemory, HTTPAddr: ":0"})
	if err != nil || st == nil || db != nil {
		t.Fatalf("内存回退装配错误: st=%v db=%v err=%v", st, db, err)
	}
}

func TestDeps_OpenStoreValidation(t *testing.T) {
	if _, _, err := OpenStore(Config{Env: EnvProd, StoreDriver: StoreMemory, HTTPAddr: ":0"}); !errors.Is(err, ErrProductionMemoryStore) {
		t.Fatalf("生产模式必须拒绝内存存储: %v", err)
	}
	if _, _, err := OpenStore(Config{Env: EnvDev, StoreDriver: StoreMySQL, HTTPAddr: ":0"}); !errors.Is(err, ErrMissingConfig) {
		t.Fatalf("MySQL 模式必须配置 DSN: %v", err)
	}
	if _, _, err := OpenStore(Config{Env: EnvDev, StoreDriver: "sqlite", HTTPAddr: ":0"}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("未知驱动应拒绝: %v", err)
	}
}

func TestDeps_OpenStoreMySQLConnectFailure(t *testing.T) {
	_, _, err := OpenStore(Config{
		Env: EnvDev, StoreDriver: StoreMySQL, HTTPAddr: ":0",
		MySQLDSN: "root:wrong@tcp(127.0.0.1:1)/none?timeout=100ms",
	})
	if err == nil {
		t.Fatal("MySQL 连接失败必须显式报错")
	}
}

func TestDeps_MigrateSplitsAndExecutes(t *testing.T) {
	conn := &migConn{}
	db := newMigDB(t, conn)
	n, err := Migrate(context.Background(), db, migrationsDir())
	if err != nil {
		t.Fatal(err)
	}
	if n < 7 {
		t.Fatalf("应至少执行 7 条建表语句，实际 %d", n)
	}
	conn.mu.Lock()
	defer conn.mu.Unlock()
	found := false
	for _, query := range conn.queries {
		if strings.Contains(query, "CREATE TABLE IF NOT EXISTS slots") {
			found = true
		}
	}
	if !found {
		t.Fatal("迁移未执行 slots 建表")
	}
}

func TestDeps_MigrateErrorAndEmptyDir(t *testing.T) {
	failing := &migConn{fail: true}
	if _, err := Migrate(context.Background(), newMigDB(t, failing), migrationsDir()); err == nil {
		t.Fatal("迁移失败必须显式报错")
	}
	empty := &migConn{}
	if n, err := Migrate(context.Background(), newMigDB(t, empty), "no-such-dir"); err != nil || n != 0 {
		t.Fatalf("空目录应为 0 条且无错误: n=%d err=%v", n, err)
	}
}

func TestDeps_SplitStatements(t *testing.T) {
	statements := splitStatements("-- 注释\nCREATE TABLE a (id INT);\n\nCREATE TABLE b (id INT);\n")
	if len(statements) != 2 || !strings.Contains(statements[0], "CREATE TABLE a") {
		t.Fatalf("拆句结果错误: %v", statements)
	}
}

func TestDeps_RealMySQLOpenAndMigrate(t *testing.T) {
	dsn := os.Getenv("PETSTORE_TEST_DSN")
	if dsn == "" {
		t.Skip("未设置 PETSTORE_TEST_DSN，跳过真实 MySQL 集成测试")
	}
	st, db, err := OpenStore(Config{
		Env: EnvDev, StoreDriver: StoreMySQL, HTTPAddr: ":0", MySQLDSN: dsn,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if st == nil {
		t.Fatal("应返回 MySQL 真相源")
	}
	n, err := Migrate(context.Background(), db, migrationsDir())
	if err != nil || n < 7 {
		t.Fatalf("真实迁移失败: n=%d err=%v", n, err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='slots'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("slots 表应存在")
	}
}
