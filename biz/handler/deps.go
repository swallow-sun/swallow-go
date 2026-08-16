package handler

// deps.go 放共享依赖：Deps 结构体和 NewDeps 工厂函数。
// 整个程序启动时只调一次 NewDeps，创建好所有依赖塞进 Deps 里，
// 之后所有 handler 共用这一份，不重复创建。

import (
	"github.com/swallow-sun/swallow-go/internal/config"
	"github.com/swallow-sun/swallow-go/internal/data"
	"github.com/swallow-sun/swallow-go/internal/identity"
	"github.com/swallow-sun/swallow-go/internal/memory"
	"github.com/swallow-sun/swallow-go/internal/provider/llm"
)

// NewDeps 在 main.go 启动时调一次，组装所有共享依赖。
// 入参：配置（从 config.toml 读出来的）和数据库仓库（GORM 封装）。
// 返回：一个装满依赖的 Deps 结构体指针。
func NewDeps(cfg *config.Config, repo data.Repository) *Deps {
	return &Deps{
		// 配置，handler 里可能要读 LLM 参数等
		cfg: cfg,
		// 数据层，直接读写数据库
		repo: repo,
		// 身份管理（用户登录/会话创建），需要 repo 来存取数据
		idm: identity.New(repo),
		// 对话记忆，需要 repo 来存取历史消息
		mem: memory.New(repo),
		// LLM 客户端，用配置里的地址/密钥/模型名初始化
		llm: llm.NewOpenAICompat(llm.Config{
			// LLM 服务的 API 地址
			BaseURL: cfg.LLM.BaseURL,
			// 调 LLM 用的密钥
			APIKey:  cfg.LLM.APIKey,
			// 模型名，如 "gpt-4o"
			Model:   cfg.LLM.Model,
		}),
	}
}
