// types.go 放 identity 包的类型定义.
//
// 做的事情:
//  定义 Manager 结构体:持有 data.Repository,负责用户登录/创建和会话管理.
package identity

import "github.com/swallow-sun/swallow-go/internal/data"

// Manager 管理用户和会话.
// 只持有一个 repo 字段(data.Repository 接口),所有数据库操作都通过它来做.
type Manager struct {
	repo data.Repository
}
