// deps.go 放 handler 层的依赖组装逻辑.
//
// 做的事情:
//  1. 提供 NewDeps 工厂函数: 程序启动时调一次, 创建所有底层依赖(repo/idm/mem/llm).
//  2. 用底层依赖组装 service.Deps,再创建聊天、会话、历史、看板、记忆和设备 Service.
//  3. 返回装满 Service 的 Deps 结构体, 后续所有 handler 共用这一份.
package handler

import (
	"github.com/swallow-sun/swallow-go/biz/service"
	"github.com/swallow-sun/swallow-go/internal/config"
	"github.com/swallow-sun/swallow-go/internal/data"
	"github.com/swallow-sun/swallow-go/internal/identity"
	"github.com/swallow-sun/swallow-go/internal/memory"
	"github.com/swallow-sun/swallow-go/internal/provider/asr"
	"github.com/swallow-sun/swallow-go/internal/provider/llm"
	"github.com/swallow-sun/swallow-go/internal/provider/tts"
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
		memory.New(repo, cfg.MemorySafetyFilterEnabled()),
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

	// 创建 ASR Provider (语音识别).
	// 只有配置了 ASR 的 base_url 和 model 才创建, 否则为 nil, handler 里判空.
	var asrProvider asr.Provider
	if cfg.ASR.BaseURL != "" && cfg.ASR.Model != "" {
		asrProvider = asr.NewOpenAICompat(asr.Config{
			BaseURL: cfg.ASR.BaseURL,
			APIKey:  cfg.ASR.APIKey,
			Model:   cfg.ASR.Model,
		})
	}

	// 创建 TTS Provider (语音合成).
	// edge-tts 不需要 API key, 只要配置了 voice 就创建.
	var ttsProvider tts.Provider
	if cfg.TTS.Voice != "" {
		ttsProvider = tts.NewEdge(tts.Config{
			Voice:        cfg.TTS.Voice,
			OutputFormat: cfg.TTS.OutputFormat,
			Rate:         cfg.TTS.Rate,
			Volume:       cfg.TTS.Volume,
			Pitch:        cfg.TTS.Pitch,
		})
	}

	// 用 svcDeps 创建六个 Service,装进 Deps 结构体返回.
	// 六个 Service 共用同一份底层依赖(同一个数据库连接、同一个 LLM 客户端等).
	return &Deps{
		chat:      service.NewChatService(svcDeps),      // ChatService 处理 /api/chat 流式对话
		session:   service.NewSessionService(svcDeps),   // SessionService 处理 /api/session 会话创建
		history:   service.NewHistoryService(svcDeps),   // HistoryService 处理 /api/history 历史查询
		dashboard: service.NewDashboardService(svcDeps), // DashboardService 处理 /api/v1/dashboard 看板查询
		memory:    service.NewMemoryService(svcDeps),    // MemoryService 处理 /api/v1/memory-candidates 和 /api/v1/memories 记忆管理
		device:    service.NewDeviceService(svcDeps),    // DeviceService 处理设备注册和认证
		asr:       asrProvider,                          // ASR Provider 处理设备语音识别
		tts:       ttsProvider,                          // TTS Provider 处理设备语音合成
	}
}
