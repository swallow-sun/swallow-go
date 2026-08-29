package data

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// NewPostgres 打开 PostgreSQL 连接、验证连通性并执行版本化迁移。
func NewPostgres(dsn, migrationsDir string) (Repository, error) {
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger:                                   logger.NewGORMLogger(),
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		return nil, fmt.Errorf("open postgres with gorm: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get postgres connection pool: %w", err)
	}
	sqlDB.SetMaxOpenConns(25)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	if err := migrateDatabase(context.Background(), db, migrationsDir, postgresMigrationSQL); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("migrate postgres schema: %w", err)
	}

	logger.Info("PostgreSQL database migration completed", zap.String("driver", "postgres"))
	return &gormRepo{db: db}, nil
}

// postgresMigrationSQL 将冻结的 SQLite 历史迁移映射为等价 PostgreSQL DDL。
// 原文件不直接修改，避免破坏已落库迁移的 checksum；新 SQL 应尽量使用两端兼容语法。
func postgresMigrationSQL(sql string) string {
	replacer := strings.NewReplacer(
		"INTEGER PRIMARY KEY AUTOINCREMENT", "BIGSERIAL PRIMARY KEY",
		"DATETIME", "TIMESTAMPTZ",
		" BLOB", " BYTEA",
		"MAX(next_version, excluded.next_version)", "GREATEST(memory_sync_cursors.next_version, excluded.next_version)",
		"success INTEGER DEFAULT 1", "success BOOLEAN DEFAULT TRUE",
		"success NUMERIC DEFAULT true", "success BOOLEAN DEFAULT TRUE",
		"allow_teasing           INTEGER NOT NULL DEFAULT 1", "allow_teasing           BOOLEAN NOT NULL DEFAULT TRUE",
		"allow_strict_reminder   INTEGER NOT NULL DEFAULT 1", "allow_strict_reminder   BOOLEAN NOT NULL DEFAULT TRUE",
		"allow_affection         INTEGER NOT NULL DEFAULT 1", "allow_affection         BOOLEAN NOT NULL DEFAULT TRUE",
	)
	return replacer.Replace(sql)
}
