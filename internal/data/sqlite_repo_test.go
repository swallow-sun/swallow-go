package data

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/swallow-sun/swallow-go/pkg/logger"
)

// TestSQLiteRepository 验证 GORM 实现保持 Repository 的主要行为和错误约定。
func TestSQLiteRepository(t *testing.T) {
	if err := logger.Init(); err != nil {
		t.Fatalf("init logger: %v", err)
	}

	tempDir := t.TempDir()
	migrationsDir := filepath.Join(tempDir, "migrations")
	if err := os.Mkdir(migrationsDir, 0755); err != nil {
		t.Fatalf("create migrations directory: %v", err)
	}
	migrationSQL, err := os.ReadFile(filepath.Join("..", "..", "script", "migrations", "0001_initial.sql"))
	if err != nil {
		t.Fatalf("read initial migration: %v", err)
	}
	if err := os.WriteFile(filepath.Join(migrationsDir, "0001_initial.sql"), migrationSQL, 0644); err != nil {
		t.Fatalf("write initial migration: %v", err)
	}

	repo, err := NewSQLite(filepath.Join(tempDir, "swallow.db"), migrationsDir)
	if err != nil {
		t.Fatalf("new sqlite repository: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })

	ctx := context.Background()
	if _, err := repo.GetUserByName(ctx, "owner"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing user error = %v, want sql.ErrNoRows", err)
	}

	user, err := repo.CreateUser(ctx, "owner", "owner")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if user.ID == 0 || user.Name != "owner" || user.CreatedAt.IsZero() {
		t.Fatalf("unexpected user: %+v", user)
	}

	session, err := repo.CreateSession(ctx, "session-1", user.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if session.Status != "active" || session.StartedAt.IsZero() {
		t.Fatalf("unexpected session: %+v", session)
	}

	if _, err := repo.InsertDialogue(ctx, session.ID, user.ID, "user", "hello", TokenUsage{}, "trace-1"); err != nil {
		t.Fatalf("insert user dialogue: %v", err)
	}
	usage := TokenUsage{PromptTokens: 10, CompletionTokens: 4, CacheHitTokens: 6, CacheMissTokens: 4, ReasoningTokens: 2, TotalTokens: 14}
	if _, err := repo.InsertDialogue(ctx, session.ID, user.ID, "assistant", "hi", usage, "trace-1"); err != nil {
		t.Fatalf("insert assistant dialogue: %v", err)
	}

	history, err := repo.GetRecentDialogues(ctx, session.ID, 20)
	if err != nil {
		t.Fatalf("get recent dialogues: %v", err)
	}
	if len(history) != 2 || history[0].Role != "user" || history[1].Role != "assistant" {
		t.Fatalf("history is not chronological: %+v", history)
	}
	if history[1].TotalTokens != 14 || history[1].CacheHitTokens != 6 || history[1].ReasoningTokens != 2 {
		t.Fatalf("token usage was not persisted: %+v", history[1])
	}

	if err := repo.InsertEvent(ctx, "llm.stream", nil, `{}`, 12, true, "trace-1"); err != nil {
		t.Fatalf("insert event: %v", err)
	}
}
