// deps.go 放 handler 层的依赖组装逻辑。
//
// 做的事情：
//  1. 提供 NewDeps 工厂函数：程序启动时调一次，创建所有底层依赖（repo/idm/mem/llm）。
//  2. 用底层依赖组装 service.Deps，再用它创建三个 Service（Chat/Session/History）。
//  3. 返回装满 Service 的 Deps 结构体，后续所有 handler 共用这一份。
package handler

import (
	"github.com/swallow-sun/swallow-go/biz/service"
	"github.com/swallow-sun/swallow-go/internal/config"
	"github.com/swallow-sun/swallow-go/internal/data"
	"github.com/swallow-sun/swallow-go/internal/identity"
	"github.com/swallow-sun/swallow-go/internal/memory"
	"github.com/swallow-sun/swallow-go/internal/provider/llm"
)

// NewDeps 在 main.go 启动时调一次，组装所有共享依赖。
// 入参：配置（从 config.toml 读出来的）和数据库仓库（GORM 封装）。
// 返回：一个装满 Service 的 Deps 结构体指针。
func NewDeps(cfg *config.Config, repo data.Repository) *Deps {
	// 先组装 service 层的底层依赖
	svcDeps := service.NewDeps(
		cfg,
		repo,
		identity.New(repo),     // 身份管理（用户登录/会话创建），需要 repo 来存取数据
		memory.New(repo),      // 对话记忆，需要 repo 来存取历史消息
		llm.NewOpenAICompat(llm.Config{
			// LLM 服务的 API 地址
			BaseURL: cfg.LLM.BaseURL,
			// 调 LLM 用的密钥
			APIKey: cfg.LLM.APIKey,
			// 模型名，如 "gpt-4o"
			Model: cfg.LLM.Model,
		}),
	)

	return &Deps{
		chat:    service.NewChatService(svcDeps),
		session: service.NewSessionService(svcDeps),
		history: service.NewHistoryService(svcDeps),
	}
}
