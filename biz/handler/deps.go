// deps.go 放 handler 层的依赖组装逻辑.
//
// 做的事情:
//  1. 提供 NewDeps 工厂函数: 程序启动时调一次, 创建所有底层依赖(repo/idm/mem/llm).
//  2. 用底层依赖组装 service.Deps, 再用它创建三个 Service(Chat/Session/History).
//  3. 返回装满 Service 的 Deps 结构体, 后续所有 handler 共用这一份.
package handler

import (
	"github.com/swallow-sun/swallow-go/biz/service"
	"github.com/swallow-sun/swallow-go/internal/config"
	"github.com/swallow-sun/swallow-go/internal/data"
	"github.com/swallow-sun/swallow-go/internal/identity"
	"github.com/swallow-sun/swallow-go/internal/memory"
	"github.com/swallow-sun/swallow-go/internal/provider/llm"
)

// NewDeps 在 main.go 启动时调一次, 组装所有共享依赖.
// 入参: 配置(从 config.toml 读出来的)和数据库仓库(GORM 封装).
// 返回: 一个装满 Service 的 Deps 结构体指针.
func NewDeps(cfg *config.Config, repo data.Repository) *Deps {
	// 先组装 service 层的底层依赖.
	// service.NewDeps 是 service 层的构造函数, 接收五个依赖:
	//   cfg — 配置, 从 config.toml 读出来的
	//   repo — 数据库仓库, GORM 封装, 存取数据用
	//   idm — 身份管理, 用户登录/会话创建
	//   mem — 对话记忆, 存取历史消息
	//   llm — LLM 服务, 调 LLM API 用
	svcDeps := service.NewDeps(
		cfg,
		repo,
		// identity.New(repo) 创建身份管理器, 传入 repo 用来存取用户/会话数据
		identity.New(repo),
		// memory.New(repo) 创建对话记忆存储, 传入 repo 用来存取历史消息
		memory.New(repo),
		// llm.NewOpenAICompat 创建一个兼容 OpenAI API 的 LLM 客户端
		// llm.Config 是配置结构体, 包含 BaseURL, APIKey, Model 三个字段
		llm.NewOpenAICompat(llm.Config{
			// LLM 服务的 API 地址, 比如 "https://api.openai.com/v1"
			BaseURL: cfg.LLM.BaseURL,
			// 调 LLM 用的密钥, 用于鉴权
			APIKey: cfg.LLM.APIKey,
			// 模型名, 如 "gpt-4o", "deepseek-chat"
			Model: cfg.LLM.Model,
		}),
	)

	// 用 svcDeps 创建三个 Service, 装进 Deps 结构体返回.
	// service.NewChatService / NewSessionService / NewHistoryService 各自接收同一个 svcDeps,
	// 这样三个 Service 共用同一份底层依赖(同一个数据库连接, 同一个 LLM 客户端等)
	return &Deps{
		chat:    service.NewChatService(svcDeps), // ChatService 处理 /api/chat 流式对话
		session: service.NewSessionService(svcDeps), // SessionService 处理 /api/session 会话创建
		history: service.NewHistoryService(svcDeps), // HistoryService 处理 /api/history 历史查询
	}
}
