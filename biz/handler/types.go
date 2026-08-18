// types.go 放所有 handler 共用的类型定义。
//
// 做的事情：
//  1. 定义 Deps 结构体：handler 层的依赖集合，持有三个 Service。
//  2. 定义 HTTP 请求/响应结构体：createSessionReq、chatReq、historyResp 等，
//     客户端发来的 JSON 反序列化到请求结构体，响应结构体序列化成 JSON 发给客户端。
package handler

import (
	"github.com/swallow-sun/swallow-go/biz/service"
)

// Deps 是 handler 层的依赖集合，持有三个 Service。
// handler 通过 Service 间接访问数据层，跟 HTTP 解析和业务逻辑分开。
type Deps struct {
	chat    *service.ChatService
	session *service.SessionService
	history *service.HistoryService
}

// createSessionReq 是 POST /api/session 的请求体。
// 客户端传 {"user_name": "owner"}，解析到这个结构体里。
type createSessionReq struct {
	UserName string `json:"user_name"` // 用户名，没传的话 handler 里会默认填 "owner"
}

// createSessionResp 是 POST /api/session 的响应体。
// handler 返回这个结构体，序列化成 JSON 发给客户端。
type createSessionResp struct {
	SessionID string `json:"session_id"` // 新创建的会话 ID（UUID），客户端后续对话要带上它
	UserName  string `json:"user_name"`  // 用户名（可能是客户端传的，也可能是默认的 "owner"）
	UserID    int64  `json:"user_id"`    // 用户在数据库里的自增 ID
}

// chatReq 是 POST /api/chat 的请求体。
// 客户端传 {"session_id": "xxx", "message": "你好"}，解析到这个结构体里。
type chatReq struct {
	SessionID        string `json:"session_id"`         // 会话 ID，告诉服务端这段消息属于哪段对话
	ClientMessageID string `json:"client_message_id"`  // 客户端生成的稳定消息 ID，网络重试时必须保持不变
	Message         string `json:"message"`            // 用户说的话
}

// historyItem 是一条对话记录，对应数据库 dialogues 表里的一行。
type historyItem struct {
	Role      string `json:"role"`      // 角色："user"（用户说的）或 "assistant"（助手回的）
	Content   string `json:"content"`   // 消息内容
	Timestamp string `json:"timestamp"` // 发生时间，已格式化成可读字符串
}

// historyResp 是 GET /api/history 的响应体。
type historyResp struct {
	SessionID string        `json:"session_id"` // 哪个会话的历史
	Items     []historyItem `json:"items"`      // 对话记录列表，按时间正序排列
}
