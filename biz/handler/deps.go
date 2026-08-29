// deps.go 放 handler 层的依赖组装逻辑.
//
// 做的事情:
//  1. 提供 NewDeps 工厂函数: 程序启动时调一次, 创建所有底层依赖(repo/idm/mem/llm).
//  2. 用底层依赖组装 service.Deps,再创建聊天、会话、历史、看板、记忆和设备 Service.
//  3. 返回装满 Service 的 Deps 结构体, 后续所有 handler 共用这一份.
package handler

import (
	"fmt"
	"strings"

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
// 返回：装满 Service 的 Deps；ASR Provider 配置非法时返回错误并阻止启动。
func NewDeps(cfg *config.Config, repo data.Repository) (*Deps, error) {
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

	// 严格按 [asr].provider 创建唯一的语音识别实现。
	// 工厂不会在阿里云、硅基流动等供应商之间自动切换；配置错误直接阻止启动。
	selectedASR := cfg.ASR.SelectedASRProviderConfig()
	asrProvider, err := asr.NewProvider(cfg.ASR.Provider, asr.Config{
		BaseURL:   selectedASR.BaseURL,
		APIKey:    selectedASR.APIKey,
		Model:     selectedASR.Model,
		Language:  selectedASR.Language,
		EnableITN: selectedASR.EnableITN,
	})
	if err != nil {
		return nil, fmt.Errorf("create ASR provider: %w", err)
	}

	// 创建 TTS Provider (语音合成).
	// 根据 config.toml 里 [tts].provider 选择不同的 TTS 供应商:
	//   - "siliconflow": 硅基流动 TTS, HTTP POST, 国内直连, 需要 api_key
	//   - "aliyun": 阿里云百炼 CosyVoice, WebSocket 双向流式, 需要 api_key
	//   - "edge": 微软 edge-tts, WebSocket, 不需要 key (国内不稳定)
	var ttsProvider tts.Provider
	switch cfg.TTS.Provider {
	case "siliconflow":
		ttsProvider = tts.NewSiliconFlow(tts.Config{
			BaseURL:        cfg.TTS.BaseURL,
			APIKey:         cfg.TTS.APIKey,
			Model:          cfg.TTS.Model,
			Voice:          cfg.TTS.Voice,
			ReferenceAudio: cfg.TTS.ReferenceAudio,
			ReferenceText:  cfg.TTS.ReferenceText,
			OutputFormat:   cfg.TTS.OutputFormat,
			SampleRate:     cfg.TTS.SampleRate,
			Speed:          cfg.TTS.Speed,
		})
	case "aliyun":
		selected := cfg.TTS.SelectedTTSProviderConfig()
		if strings.TrimSpace(selected.APIKey) == "" {
			return nil, fmt.Errorf("create TTS provider: tts.aliyun.api_key is required")
		}
		ttsProvider = tts.NewAliyun(tts.Config{
			BaseURL:     selected.BaseURL,
			APIKey:      selected.APIKey,
			WorkspaceID: selected.WorkspaceID,
			Model:       selected.Model,
			Voice:       selected.Voice,
			SampleRate:  selected.SampleRate,
			Speed:       selected.Speed,
		})
	case "zhipu":
		ttsProvider = tts.NewZhipu(tts.Config{
			BaseURL:      cfg.TTS.BaseURL,
			APIKey:       cfg.TTS.APIKey,
			Model:        cfg.TTS.Model,
			Voice:        cfg.TTS.Voice,
			OutputFormat: cfg.TTS.OutputFormat,
			Speed:        cfg.TTS.Speed,
		})
	case "edge":
		ttsProvider = tts.NewEdge(tts.Config{
			Voice:        cfg.TTS.Voice,
			OutputFormat: cfg.TTS.OutputFormat,
			Rate:         cfg.TTS.Rate,
			Volume:       cfg.TTS.Volume,
			Pitch:        cfg.TTS.Pitch,
		})
	default:
		return nil, fmt.Errorf("create TTS provider: unsupported cloud provider %q", cfg.TTS.Provider)
	}

	// 用 svcDeps 创建六个 Service,装进 Deps 结构体返回.
	// 六个 Service 共用同一份底层依赖(同一个数据库连接、同一个 LLM 客户端等).
	return &Deps{
		config:     cfg,                                   // 向设备下发非敏感运行参数
		chat:       service.NewChatService(svcDeps),       // ChatService 处理 /api/chat 流式对话
		session:    service.NewSessionService(svcDeps),    // SessionService 处理 /api/session 会话创建
		history:    service.NewHistoryService(svcDeps),    // HistoryService 处理 /api/history 历史查询
		dashboard:  service.NewDashboardService(svcDeps),  // DashboardService 处理 /api/v1/dashboard 看板查询
		memory:     service.NewMemoryService(svcDeps),     // MemoryService 处理 /api/v1/memory-candidates 和 /api/v1/memories 记忆管理
		device:     service.NewDeviceService(svcDeps),     // DeviceService 处理设备注册和认证
		deviceSync: service.NewDeviceSyncService(svcDeps), // DeviceSyncService 处理设备 sync_outbox 批量上报
		profile:    service.NewProfileService(svcDeps),    // ProfileService 处理 /api/v1/profiles 和 /api/v1/tags
		emotion:    service.NewEmotionService(svcDeps),    // EmotionService 处理 /api/v1/emotion-sessions
		reminder:   service.NewReminderService(svcDeps),   // ReminderService 处理 /api/v1/reminders
		asr:        asrProvider,                           // ASR Provider 处理设备语音识别
		tts:        ttsProvider,                           // TTS Provider 处理设备语音合成
	}, nil
}
