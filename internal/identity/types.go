// types.go 放 identity 包的类型定义。
//
// 做的事情：
//  定义 Manager 结构体：持有 data.Repository，负责用户登录/创建和会话管理。
package identity

import "github.com/swallow-sun/swallow-go/internal/data"

// Manager 管理用户和会话。
type Manager struct {
	repo data.Repository
}
