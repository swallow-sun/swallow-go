package data

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"

	"gorm.io/gorm"
)

// TableName 明确迁移记录表名，避免依赖 GORM 的复数命名规则。
func (MigrationRecord) TableName() string {
	return "schema_migrations"
}

// migrateSQLite 按版本顺序执行尚未完成的 SQL 迁移。
func migrateSQLite(ctx context.Context, db *gorm.DB, migrationsDir string) error {
	migrations, err := loadMigrations(migrationsDir)
	if err != nil {
		return err
	}
	if err := createMigrationTable(ctx, db); err != nil {
		return err
	}
	for _, migration := range migrations {
		if err := applyMigration(ctx, db, migration); err != nil {
			return err
		}
	}
	return nil
}

// loadMigrations 加载 NNNN_name.sql 文件，并为内容计算 SHA-256 校验值。
func loadMigrations(dir string) ([]Migration, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations dir %s: %w", dir, err)
	}
	pattern := regexp.MustCompile(`^(\d{4})_([a-z0-9][a-z0-9_-]*)\.sql$`)
	migrations := make([]Migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		matches := pattern.FindStringSubmatch(entry.Name())
		if matches == nil {
			return nil, fmt.Errorf("invalid migration filename %q: want NNNN_name.sql", entry.Name())
		}
		version, err := strconv.Atoi(matches[1])
		if err != nil {
			return nil, fmt.Errorf("parse migration version %q: %w", entry.Name(), err)
		}
		path := filepath.Join(dir, entry.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", path, err)
		}
		sum := sha256.Sum256(content)
		migrations = append(migrations, Migration{
			Version: version, Name: matches[2], Path: path,
			Checksum: fmt.Sprintf("%x", sum), SQL: string(content),
		})
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	for i := 1; i < len(migrations); i++ {
		if migrations[i-1].Version == migrations[i].Version {
			return nil, fmt.Errorf("duplicate migration version %04d", migrations[i].Version)
		}
	}
	if len(migrations) == 0 {
		return nil, fmt.Errorf("no migration files found in %s", dir)
	}
	return migrations, nil
}

func createMigrationTable(ctx context.Context, db *gorm.DB) error {
	migrator := db.WithContext(ctx).Migrator()
	if migrator.HasTable(&MigrationRecord{}) {
		return nil
	}
	// schema_migrations 是迁移器运行所依赖的元数据表，因此由 GORM 创建。
	// users、sessions 等业务表仍然只能通过 script/migrations 中的 SQL 修改。
	if err := migrator.CreateTable(&MigrationRecord{}); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	return nil
}

func applyMigration(ctx context.Context, db *gorm.DB, migration Migration) error {
	var existing MigrationRecord
	err := db.WithContext(ctx).Where("version = ?", migration.Version).Take(&existing).Error
	if err == nil && existing.Status == MigrationStatusCompleted {
		if existing.Name != migration.Name || existing.Checksum != migration.Checksum {
			return fmt.Errorf("migration %04d changed after completion", migration.Version)
		}
		return nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("read migration %04d record: %w", migration.Version, err)
	}

	startedAt := time.Now()
	record := MigrationRecord{
		Version: migration.Version, Name: migration.Name, Checksum: migration.Checksum,
		Status: MigrationStatusRunning, StartedAt: startedAt,
	}
	if err := db.WithContext(ctx).Save(&record).Error; err != nil {
		return fmt.Errorf("mark migration %04d running: %w", migration.Version, err)
	}

	completedAt := time.Now()
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(migration.SQL).Error; err != nil {
			return err
		}
		return tx.Model(&MigrationRecord{}).
			Where("version = ?", migration.Version).
			Updates(map[string]any{
				"status": MigrationStatusCompleted, "completed_at": completedAt,
				"error_message": nil,
			}).Error
	}); err != nil {
		message := err.Error()
		updateErr := db.WithContext(context.WithoutCancel(ctx)).Model(&MigrationRecord{}).
			Where("version = ?", migration.Version).
			Updates(map[string]any{"status": MigrationStatusFailed, "error_message": message}).Error
		if updateErr != nil {
			return fmt.Errorf("execute migration %04d: %v; record failure: %w", migration.Version, err, updateErr)
		}
		return fmt.Errorf("execute migration %04d (%s): %w", migration.Version, migration.Path, err)
	}
	return nil
}
