package data

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMigrateSQLiteRunsOnce(t *testing.T) {
	db := openMigrationTestDB(t)
	dir := writeMigrationTestFile(t, "0001_create_items.sql", `
CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT NOT NULL);
INSERT INTO items (id, name) VALUES (1, 'first');`)

	if err := migrateSQLite(context.Background(), db, dir); err != nil {
		t.Fatalf("first migration: %v", err)
	}
	if err := migrateSQLite(context.Background(), db, dir); err != nil {
		t.Fatalf("repeated migration: %v", err)
	}

	var itemCount int64
	if err := db.Table("items").Count(&itemCount).Error; err != nil {
		t.Fatalf("count items: %v", err)
	}
	if itemCount != 1 {
		t.Fatalf("item count = %d, want 1", itemCount)
	}
	var record MigrationRecord
	if err := db.First(&record, "version = ?", 1).Error; err != nil {
		t.Fatalf("read migration record: %v", err)
	}
	if record.Status != MigrationStatusCompleted || record.CompletedAt == nil {
		t.Fatalf("unexpected migration record: %+v", record)
	}
}

func TestMigrateSQLiteRejectsChangedCompletedMigration(t *testing.T) {
	db := openMigrationTestDB(t)
	dir := writeMigrationTestFile(t, "0001_create_items.sql", `CREATE TABLE items (id INTEGER PRIMARY KEY);`)
	if err := migrateSQLite(context.Background(), db, dir); err != nil {
		t.Fatalf("first migration: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "0001_create_items.sql"), []byte(`CREATE TABLE changed (id INTEGER);`), 0644); err != nil {
		t.Fatalf("change migration: %v", err)
	}
	if err := migrateSQLite(context.Background(), db, dir); err == nil || !strings.Contains(err.Error(), "changed after completion") {
		t.Fatalf("error = %v, want changed migration error", err)
	}
}

func TestMigrateSQLiteRecordsFailureAndRollsBack(t *testing.T) {
	db := openMigrationTestDB(t)
	dir := writeMigrationTestFile(t, "0001_broken.sql", `
CREATE TABLE should_rollback (id INTEGER PRIMARY KEY);
THIS IS NOT SQL;`)

	if err := migrateSQLite(context.Background(), db, dir); err == nil {
		t.Fatal("broken migration unexpectedly succeeded")
	}
	if db.Migrator().HasTable("should_rollback") {
		t.Fatal("table from failed migration was not rolled back")
	}
	var record MigrationRecord
	if err := db.First(&record, "version = ?", 1).Error; err != nil {
		t.Fatalf("read failed migration record: %v", err)
	}
	if record.Status != MigrationStatusFailed || record.ErrorMessage == nil {
		t.Fatalf("unexpected failed migration record: %+v", record)
	}
}

func TestMigrateSQLiteAcceptsExistingSchema(t *testing.T) {
	db := openMigrationTestDB(t)
	if err := db.Exec(`CREATE TABLE existing_table (id INTEGER PRIMARY KEY)`).Error; err != nil {
		t.Fatalf("create existing table: %v", err)
	}
	dir := writeMigrationTestFile(t, "0001_baseline.sql", `CREATE TABLE IF NOT EXISTS existing_table (id INTEGER PRIMARY KEY);`)
	if err := migrateSQLite(context.Background(), db, dir); err != nil {
		t.Fatalf("baseline existing schema: %v", err)
	}
}

func openMigrationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "migration.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open migration test database: %v", err)
	}
	return db
}

func writeMigrationTestFile(t *testing.T, name, sql string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "migrations")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatalf("create migration directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(sql), 0644); err != nil {
		t.Fatalf("write migration: %v", err)
	}
	return dir
}
