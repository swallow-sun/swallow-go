package handler

import (
	"github.com/swallow-sun/swallow-go/internal/config"
	"github.com/swallow-sun/swallow-go/internal/data"
	"github.com/swallow-sun/swallow-go/internal/identity"
	"github.com/swallow-sun/swallow-go/internal/memory"
	"github.com/swallow-sun/swallow-go/internal/provider/llm"
)

const (
	chatErrorAgentInit       = "agent_init_failed"
	chatErrorConnect         = "llm_connect_failed"
	chatErrorStreamRead      = "llm_stream_read_failed"
	chatErrorClientClosed    = "client_stream_closed"
	chatErrorUserMissing     = "user_dialogue_missing"
	chatErrorAssistantSave   = "assistant_save_failed"
	chatErrorRequestState    = "request_state_failed"
	chatErrorRequestFailed   = "chat_request_failed"
	chatErrorResultMissing   = "chat_result_missing"
	chatErrorRequestRunning  = "chat_request_in_progress"
	maxClientMessageIDLength = 128
)

// types.go 放所有 handler 共用的请求/响应结构体。
// 客户端发来的 JSON 会被反序列化到 "请求结构体" 里，
// handler 返回的 "响应结构体" 会被序列化成 JSON 发给客户端。

// Deps 是 HTTP Handler 共用的业务依赖集合。
type Deps struct {
	cfg  *config.Config
	repo data.Repository
	idm  *identity.Manager
	mem  *memory.Store
	llm  llm.Provider
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
	SessionID       string `json:"session_id"`        // 会话 ID，告诉服务端这段消息属于哪段对话
	ClientMessageID string `json:"client_message_id"` // 客户端生成的稳定消息 ID，网络重试时必须保持不变
	Message         string `json:"message"`           // 用户说的话
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
