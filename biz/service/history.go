// history.go 放 HistoryService：对话历史查询业务逻辑。
//
// 做的事情：
//  1. 接收 handler 传来的 session_id。
//  2. 调 repo.GetRecentDialogues 从数据库查这个会话最近 50 条对话记录。
//  3. 把数据库结构体转成 HistoryItem 列表（角色、内容、格式化时间）。
//  4. 返回 HistoryResult 给 handler 转成 JSON 响应。
package service

import (
	"context"
	"fmt"
)

// HistoryResultLimit 是默认查询的对话条数上限。
const HistoryResultLimit = 50

// HistoryService 负责历史查询业务逻辑。
type HistoryService struct {
	deps *Deps
}

// NewHistoryService 创建一个 HistoryService。
func NewHistoryService(deps *Deps) *HistoryService {
	return &HistoryService{deps: deps}
}

// GetHistory 查询指定会话的最近 N 条对话记录。
// sessionID 为空时返回错误（handler 在 HTTP 层已校验，这里兜底）。
// 返回 HistoryResult，handler 直接转成 JSON 响应。
func (s *HistoryService) GetHistory(ctx context.Context, sessionID string) (HistoryResult, error) {
	// 从数据库查这个会话最近 50 条对话记录。
	// s.deps.repo 是底层的数据仓库，GetRecentDialogues 按时间倒序查
	dialogues, err := s.deps.repo.GetRecentDialogues(ctx, sessionID, HistoryResultLimit)
	if err != nil {
		return HistoryResult{}, fmt.Errorf("查询历史失败: %w", err)
	}

	// 把数据库结构体转成给客户端看的 JSON 结构体。
	// make 先分配好切片容量，免得 append 时频繁扩容
	// make([]HistoryItem, 0, len(dialogues)) 第一个参数是切片元素类型
	// 0 是初始长度，len(dialogues) 是预分配容量，等于查出来的记录数
	items := make([]HistoryItem, 0, len(dialogues))

	// 遍历每条对话记录，转成 HistoryItem
	// for _, dialogue := range dialogues 遍历切片，_ 忽略索引，dialogue 是每条记录
	for _, dialogue := range dialogues {
		items = append(items, HistoryItem{
			Role:    dialogue.Role,
			Content: dialogue.Content,
			// Go 的时间格式化很特殊：用 "2006-01-02 15:04:05"（Go 诞生时间做模板）
			// Format 方法按模板字符串格式化，返回格式化后的字符串
			Timestamp: dialogue.Timestamp.Format("2006-01-02 15:04:05"),
		})
	}

	// 组装成 HistoryResult 返回给 handler
	// handler 拿到后直接转成 JSON 写给客户端
	return HistoryResult{
		SessionID: sessionID,
		Items:     items,
	}, nil
}
