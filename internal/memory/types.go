// types.go 放 memory 包的类型定义。
//
// 做的事情：
//  定义 Store 结构体：持有 data.Repository，负责对话历史的存取。
package memory

import "github.com/swallow-sun/swallow-go/internal/data"

// Store 管理当前会话对话历史的存取。
type Store struct {
	repo data.Repository
}
