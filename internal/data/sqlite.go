// 本文件负责通过 GORM 初始化 SQLite 连接并执行版本化迁移。
package data

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// NewSQLite 创建基于 GORM 的 SQLite Repository。
// dbPath 是数据库文件路径，migrationsDir 是版本化 SQL 所在目录。
func NewSQLite(dbPath, migrationsDir string) (Repository, error) {
	dir := filepath.Dir(dbPath)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("create db dir: %w", err)
		}
	}

	// WAL 允许读写并发；busy_timeout 在短暂写冲突时等待。
	// 项目只保存关联 ID，不使用数据库外键约束。
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=5000", dbPath)
	// 关闭 GORM 自带输出，错误由 Repository 返回后统一通过 pkg/logger 记录。
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger:                                   gormlogger.Default.LogMode(gormlogger.Silent),
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		return nil, fmt.Errorf("open sqlite with gorm: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sqlite connection pool: %w", err)
	}
	if err := sqlDB.Ping(); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	if err := migrateSQLite(context.Background(), db, migrationsDir); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("migrate sqlite schema: %w", err)
	}

	logger.Info("SQLite 数据库已完成版本化迁移", zap.String("path", dbPath))
	return &sqliteRepo{db: db}, nil
}
