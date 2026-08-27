// types.go 放 reminder 包的类型定义.
//
// 做的事情:
//  1. 定义 Store 结构体: 持有 repo, 负责提醒的增删改查.
//  2. 定义 ReminderHint 结构体: 检测器从用户输入里提取的提醒线索.
//  3. 提供 New 构造函数.
package reminder

import "github.com/swallow-sun/swallow-go/internal/data"

// Store 管理待办提醒的存储和查询.
// 只持有一个 repo 字段(data.Repository 接口), 所有数据库操作都通过它来做.
type Store struct {
	repo data.Repository
}

// New 创建一个 reminder Store.
// repo 是数据访问层接口, 所有提醒相关的数据库操作都通过它执行.
func New(repo data.Repository) *Store {
	return &Store{repo: repo}
}

// ReminderHint 是检测器从用户输入里提取的提醒线索.
// 这是一个纯数据结构, 不关联数据库, 也不包含解析后的时间.
// 检测器只做文本扫描, 时间解析由上层负责.
type ReminderHint struct {
	// Content 是提取出来的提醒内容, 比如 "买牛奶"
	Content string
	// When 是自然语言时间表达, 比如 "明天下午3点", "in 2 hours", "2026-08-22 10:00"
	When string
}
