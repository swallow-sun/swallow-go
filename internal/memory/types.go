// types.go 放 memory 包的类型定义.
//
// 做的事情:
//  定义 Store 结构体:持有 data.Repository,负责对话历史的存取.
package memory

import "github.com/swallow-sun/swallow-go/internal/data"

// Store 管理当前会话对话历史的存取.
// 只持有一个 repo 字段(data.Repository 接口),所有数据库操作都通过它来做.
type Store struct {
	repo data.Repository
}
