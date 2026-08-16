package memory

import "github.com/swallow-sun/swallow-go/internal/data"

// Store 管理当前会话对话历史的持久化。
type Store struct {
	repo data.Repository
}
