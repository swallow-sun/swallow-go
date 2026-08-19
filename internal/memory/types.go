// types.go 放 memory 包的类型定义.
//
// 做的事情:
//
//	定义 Store 结构体:持有 data.Repository,负责对话历史的存取.
package memory

import "github.com/swallow-sun/swallow-go/internal/data"

const DefaultSearchLimit = 10

// Store 管理当前会话对话历史的存取.
// 只持有一个 repo 字段(data.Repository 接口),所有数据库操作都通过它来做.
type Store struct {
	repo data.Repository
}

// CandidateSpec 是创建长期记忆候选时使用的领域参数。
type CandidateSpec struct {
	UserID     int64
	SessionID  string
	TraceID    string
	Content    string
	MemoryType string
	Source     string
	Reason     string
	UsageHint  string
}

// SearchResult 是长期记忆检索结果和数量统计。
type SearchResult struct {
	Rows     []data.Memory
	Limit    int
	Returned int
}

type CandidateService struct {
	repo   data.Repository // 候选和正式记忆数据仓库
	policy *Policy         // 确定性候选生成规则
}

type Service struct {
	repo data.Repository /* 正式记忆数据仓库 */
}
type Retriever struct {
	repo data.Repository /* 记忆检索数据仓库 */
}
type Policy struct{}
