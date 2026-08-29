// types.go 放 config 包的类型定义.
//
// 做的事情:
//  1. 定义 Config 根结构体和应用需要的全部配置分组.
//  2. 定义运行环境、服务、模型、数据库、鉴权、日志、记忆等子配置结构体.
//  3. 所有字段用 toml 标签映射配置文件,LoadedSources 记录实际加载的文件路径列表.
package config

const (
	// DefaultLogDirectory 是未配置时使用的本地日志目录。
	DefaultLogDirectory = "logs"
	// DefaultDevelopmentLogLevel 是开发环境默认最低日志等级。
	DefaultDevelopmentLogLevel = "debug"
	// DefaultProductionLogLevel 是生产环境默认最低日志等级。
	DefaultProductionLogLevel = "info"
	// LogLevelDebug 输出 Debug 及以上日志。
	LogLevelDebug = "debug"
	// LogLevelInfo 输出 Info 及以上日志。
	LogLevelInfo = "info"
	// LogLevelWarn 输出 Warn 及以上日志。
	LogLevelWarn = "warn"
	// LogLevelError 只输出 Error 及以上日志。
	LogLevelError = "error"
	// DefaultLogMaxSizeMB 是单个日志文件默认轮转大小。
	DefaultLogMaxSizeMB = 100
	// DefaultLogMaxBackups 是默认保留的旧日志文件数量。
	DefaultLogMaxBackups = 30
	// DefaultLogMaxAgeDays 是旧日志默认保留天数。
	DefaultLogMaxAgeDays = 30
	// DefaultLogCompress 表示默认压缩轮转后的旧日志。
	DefaultLogCompress = true
	// DefaultMemorySafetyFilterEnabled 表示长期记忆敏感信息过滤默认开启.
	DefaultMemorySafetyFilterEnabled = true
	// DefaultProfileAnalysisThreshold 是触发画像分析的默认对话轮数.
	DefaultProfileAnalysisThreshold = 30
	// DefaultEmotionMaxHistorySessions 是注入 system prompt 的情绪段默认最大条数.
	DefaultEmotionMaxHistorySessions = 5
	// DefaultReminderScanIntervalSeconds 是后台扫描到期提醒的默认间隔 (秒).
	DefaultReminderScanIntervalSeconds = 60
	// DefaultReminderMaxInjectReminders 是注入 system prompt 的默认最大提醒条数.
	DefaultReminderMaxInjectReminders = 5
)

// Config 是整个项目的配置根结构.
// 里面的每个字段对应 TOML 配置文件里的一个段(比如 [app],[server]).
// toml 标签告诉解析器:这个字段对应 TOML 文件里的哪个 key.
type Config struct {
	// App 对应 TOML 里的 [app] 段,放应用级配置(比如运行环境)
	App AppConfig `toml:"app"`
	// Server 对应 [server] 段,放 HTTP 服务配置(比如监听端口)
	Server ServerConfig `toml:"server"`
	// LLM 对应 [llm] 段,放大模型连接配置(地址,密钥,模型名)
	LLM LLMConfig `toml:"llm"`
	// Database 对应 [database] 段,放 PostgreSQL 连接和迁移目录
	Database DatabaseConfig `toml:"database"`
	// Auth 对应 [auth] 段,放身份鉴权配置(比如主人令牌)
	Auth AuthConfig `toml:"auth"`
	// Debug 对应 [debug] 段,放调试配置(如 pprof 端口)
	Debug DebugConfig `toml:"debug"`
	// Metrics 对应 [metrics] 段,放 Prometheus 指标服务配置
	Metrics MetricsConfig `toml:"metrics"`
	// Log 对应 [log] 段，控制统一日志最低级别和本地目录。
	Log LogConfig `toml:"log"`
	// OTel 对应 [otel] 段；只在 production 环境启用 OTLP 日志上报。
	OTel OTelConfig `toml:"otel"`
	// Memory 对应 [memory] 段,放长期记忆安全配置.
	Memory MemoryConfig `toml:"memory"`
	// ASR 对应 [asr] 段, 放语音识别 (ASR) 配置.
	ASR ASRConfig `toml:"asr"`
	// TTS 对应 [tts] 段, 放语音合成 (TTS) 配置.
	TTS TTSConfig `toml:"tts"`
	// Profile 对应 [profile] 段, 放用户画像分析配置.
	Profile ProfileConfig `toml:"profile"`
	// Emotion 对应 [emotion] 段, 放情绪感知配置.
	Emotion EmotionConfig `toml:"emotion"`
	// Reminder 对应 [reminder] 段, 放待办提醒配置.
	Reminder ReminderConfig `toml:"reminder"`
	// LoadedSources 记录实际加载了哪些配置文件,方便排查"配置从哪来的"
	// toml:"-" 的意思是:这个字段不参与 TOML 解析,不是从配置文件里读的,
	// 而是代码在加载文件时自己往里 append 的
	LoadedSources []string `toml:"-"`
}

// MemoryConfig 控制长期记忆候选和正式记忆的安全行为.
type MemoryConfig struct {
	// SafetyFilterEnabled 控制敏感信息写入过滤;指针用于区分未配置和明确关闭.
	SafetyFilterEnabled *bool `toml:"safety_filter_enabled"`
}

// LogConfig 控制控制台和本地文件共用的日志等级与存储目录。
type LogConfig struct {
	Level      string `toml:"level"`        // debug、info、warn 或 error
	Directory  string `toml:"directory"`    // 本地日志目录，相对于程序工作目录或绝对路径
	MaxSizeMB  int    `toml:"max_size_mb"`  // 单个文件达到该 MB 数后轮转
	MaxBackups int    `toml:"max_backups"`  // 最多保留的旧日志数量
	MaxAgeDays int    `toml:"max_age_days"` // 旧日志最多保留天数
	Compress   *bool  `toml:"compress"`     // 是否 gzip 压缩旧日志；指针用于区分未配置和 false
}

// OTelConfig 控制生产环境的标准 OTLP/gRPC 日志出口。
// development 环境会无条件忽略这组配置，不创建 exporter，也不发起网络连接。
type OTelConfig struct {
	Endpoint string `toml:"endpoint"` // Alloy/Collector OTLP gRPC 地址，例如 localhost:4317
	Insecure *bool  `toml:"insecure"` // 本地明文接收端设为 true；生产 TLS 接收端设为 false
}

// AppConfig 放应用级配置(运行环境等).
type AppConfig struct {
	// environment 运行环境,只能是 development(开发)或 production(生产)
	// toml 标签 "environment" 对应配置文件里的 app.environment
	Environment string `toml:"environment"` // 运行环境:development 或 production
}

// ServerConfig 放 HTTP 服务相关配置.
type ServerConfig struct {
	// Port 是 HTTP 监听端口号,合法范围 1-65535
	// 比如 config.toml 里写 server.port = 8080,这里就拿到 8080
	Port int `toml:"port"` // HTTP 监听端口,1-65535

	// GrpcPort 是 gRPC 监听端口号, 用于 TTS 流式合成.
	// 0 表示使用默认值 9881. gRPC 和 HTTP 并行运行, 互不干扰.
	GrpcPort int `toml:"grpc_port"` // gRPC 监听端口, 默认 9881
}

// LLMConfig 放 LLM 服务连接配置.
type LLMConfig struct {
	// Provider 是模型供应商名称,比如 "deepseek","openai","anthropic"
	// 写进 model_usages 表的 provider 字段,用来区分是哪家供应商的调用量
	Provider string `toml:"provider"` // 模型供应商名称,如 "deepseek","openai"
	// BaseURL 是大模型 API 的基础地址,比如 "https://api.openai.com/v1"
	// 后面的 /chat/completions 等路径由 provider 层自己拼
	BaseURL string `toml:"base_url"` // LLM 服务的 API 基础地址,如 "https://api.openai.com/v1"
	// APIKey 是调 LLM 用的密钥
	// 允许在配置文件里留空,因为运行时会从数据库加密配置里补充
	APIKey string `toml:"-"` // 仅由 PostgreSQL encrypted_secrets 注入
	// Model 是默认用的模型名,比如 "gpt-4o","deepseek-chat"
	Model string `toml:"model"` // 默认模型名,如 "gpt-4o"
}

// DatabaseConfig 放数据库连接配置.
type DatabaseConfig struct {
	// DSN 是 PostgreSQL 连接串。密钥只放在不会提交的 config.local.toml 中。
	DSN string `toml:"dsn"`
	// MasterKeyPath 是数据库敏感配置的本地主密钥文件路径。
	MasterKeyPath string `toml:"master_key_path"`
	// MigrationsDir 是版本化 SQL 迁移文件所在的目录,比如 "script/migrations"
	// 迁移器启动时从这个目录加载所有 NNNN_name.sql 文件
	MigrationsDir string `toml:"migrations_dir"` // 版本化 SQL 迁移文件所在目录
}

// AuthConfig 放身份认证配置.
type AuthConfig struct {
	// OwnerToken 是主人令牌,调接口时用这个来证明"我是主人"
	// 允许在配置文件里留空,运行时会从数据库加密配置里补充
	OwnerToken string `toml:"owner_token"` // 主人令牌,用于鉴权(允许为空,由数据库加密配置补充)
}

// MetricsConfig 放 Prometheus 指标服务配置.
// MetricsPort=0 表示不启动;非 0 时在该端口启动一个 HTTP 服务,
// 暴露 /metrics 路径供 Prometheus 抓取.
// 开发时设 9100,生产按需开启.
type MetricsConfig struct {
	// MetricsPort 是 Prometheus metrics HTTP 服务的监听端口,0 表示不启动
	// 开发时设 9100,curl http://localhost:9100/metrics 能看到所有指标
	MetricsPort int `toml:"metrics_port"` // Prometheus metrics 端口,0=不启动
}

// DebugConfig 放调试配置.
// 当前只有一个字段 PProfPort:pprof HTTP 服务监听端口.
// PProfPort=0 表示不启动 pprof;非 0 时会在该端口启动一个标准库的 pprof server.
// pprof 用于排查内存泄漏,goroutine 泄漏,CPU 热点等问题.
type DebugConfig struct {
	// PProfPort 是 pprof HTTP 服务的监听端口,0 表示不启动
	// 开发时设成非 0(如 6060),然后用 go tool pprof http://localhost:6060/debug/pprof/heap 抓堆快照
	PProfPort int `toml:"pprof_port"` // pprof HTTP 端口,0=不启动
}

// ASRConfig 放语音识别 (ASR) 服务连接配置。
// Provider 决定唯一实现，不会在供应商之间自动降级。
type ASRConfig struct {
	// Provider 是 ASR 供应商名称：aliyun、siliconflow、groq、
	// openai_compatible 或 disabled。
	Provider string `toml:"provider"` // ASR 供应商名称
	// BaseURL 是 ASR 服务的 API 基础地址。
	// 不同 Provider 会自行追加 chat/completions 或 audio/transcriptions。
	BaseURL string `toml:"base_url"` // ASR 服务的 API 基础地址
	// APIKey 是调 ASR 用的密钥
	APIKey string `toml:"-"` // 仅由 PostgreSQL encrypted_secrets 注入
	// Model 是 ASR 模型名，如 qwen3-asr-flash。
	Model string `toml:"model"` // ASR 模型名
	// Language 是旧版平铺配置的语种提示；auto 表示自动检测。
	Language string `toml:"language"`
	// EnableITN 控制数字、日期等口语结果的书面化规整。
	// 目前仅阿里云 Qwen-ASR 使用，其他 Provider 会忽略。
	EnableITN bool `toml:"enable_itn"`
	// Aliyun 保存阿里云独立连接配置，切换供应商时不会误用其他家的密钥。
	Aliyun ASRProviderConfig `toml:"aliyun"`
	// SiliconFlow 保存硅基流动独立连接配置。
	SiliconFlow ASRProviderConfig `toml:"siliconflow"`
}

// ASRProviderConfig 是单个云 ASR 供应商的独立配置。
// 各供应商分别保存地址、密钥和模型，provider 只负责选择，不复制配置。
type ASRProviderConfig struct {
	BaseURL   string `toml:"base_url"`
	APIKey    string `toml:"-"`
	Model     string `toml:"model"`
	Language  string `toml:"language"`   // auto 表示让模型自动检测
	EnableITN bool   `toml:"enable_itn"` // 数字、日期等口语结果书面化
}

// TTSConfig 放语音合成 (TTS) 服务配置.
// 支持四种云端 provider:
//   - siliconflow: 硅基流动 TTS, HTTP POST, 支持 API 级流式, 需要 api_key (国内直连)
//   - edge: 微软 edge-tts, 走 WebSocket, 不需要 api_key (国内不稳定, 不支持流式)
//   - zhipu: 智谱 GLM-TTS, 支持官方音色和 GLM-TTS-Clone 私有音色
//   - aliyun: 阿里云百炼 CosyVoice WebSocket, 双向流式、低首包延迟
type TTSConfig struct {
	// Provider 是 TTS 供应商名称: siliconflow、aliyun、zhipu 或 edge。
	Provider string `toml:"provider"` // TTS 供应商名称
	// Aliyun 是阿里云百炼实时 TTS 的独立配置，不与硅基流动密钥混用。
	Aliyun TTSProviderConfig `toml:"aliyun"`
	// BaseURL 是 TTS 服务的 API 基础地址 (siliconflow 用, edge 不需要)
	// 如 "https://api.siliconflow.cn/v1"
	BaseURL string `toml:"base_url"` // TTS 服务的 API 基础地址
	// APIKey 是调 TTS 用的密钥 (siliconflow 用, edge 不需要)
	// 只从 TOML 读, 不 seed 到数据库 (和 ASR 一样)
	APIKey string `toml:"-"` // 仅由 PostgreSQL encrypted_secrets 注入
	// Model 是 TTS 模型名 (siliconflow 用, edge 不需要)
	// 如 "FunAudioLLM/CosyVoice2-0.5B"
	Model string `toml:"model"` // TTS 模型名
	// Voice 是默认语音名称
	// edge: "zh-CN-XiaoxiaoNeural" (女声晓晓)
	// siliconflow: "FunAudioLLM/CosyVoice2-0.5B:alex" (男声 Alex)
	Voice string `toml:"voice"` // 默认语音名称
	// ReferenceAudio 是声音克隆参考音频的本地路径 (siliconflow CosyVoice2 用).
	// 配置后 TTS 会读这个文件转 base64, 通过 references 字段发给 API 克隆音色.
	// 配了此字段后 voice 字段不发送 (两者互斥). 不配则用 voice 预设音色.
	// 支持 .wav/.mp3 格式, 建议 3-10 秒清晰人声.
	ReferenceAudio string `toml:"reference_audio"` // 声音克隆参考音频路径
	// ReferenceText 是参考音频的转录文本 (siliconflow CosyVoice2 用).
	// SiliconFlow API 实测必填, 不传会返回 500 错误.
	ReferenceText string `toml:"reference_text"` // 参考音频转录文本
	// OutputFormat 是音频输出格式
	// edge: "riff-16khz-16bit-mono-pcm" (裸 PCM, Go 侧拼 WAV 头)
	// siliconflow: "wav" (直接输出完整 WAV 文件)
	OutputFormat string `toml:"output_format"` // 音频输出格式
	// SampleRate 是输出采样率 (siliconflow 用, edge 不支持)
	// 如 16000 (和 C++ waveOut 匹配)
	SampleRate int `toml:"sample_rate"` // 输出采样率
	// Speed 是语速 (siliconflow 用), 0.25~4.0, 默认 1.0
	Speed float64 `toml:"speed"` // 语速
	// PlaybackMode 控制设备端如何组织一轮文字：full_turn 收齐后一次合成，
	// low_latency 按较小文本单元尽早起播。配置通过设备运行配置接口下发。
	PlaybackMode string `toml:"playback_mode"`
	// MaxSynthesisUnitBytes 是单次 TTS 请求的最大 UTF-8 字节数。
	MaxSynthesisUnitBytes int `toml:"max_synthesis_unit_bytes"`
	// FinalPaddingMs 是设备端在整轮原始 PCM 后追加的零静音时长。
	FinalPaddingMs int `toml:"final_padding_ms"`
	// CrossfadeMs 是极长回复包含多个合成单元时的交叉淡化时长。
	CrossfadeMs int `toml:"crossfade_ms"`
	// StartPrebufferMs 是首次开口前至少积累的可播放音频时长。
	// 太大会显得反应迟钝，太小则容易被上游到包抖动耗尽。
	StartPrebufferMs int `toml:"start_prebuffer_ms"`
	// RecoveryPrebufferMs 是发生真实欠载后恢复播放前重新积累的音频时长。
	RecoveryPrebufferMs int `toml:"recovery_prebuffer_ms"`
	// Rate 是语速 (edge 用), 如 "+0%", "-10%", "+20%"
	Rate string `toml:"rate"` // 语速 (edge-tts)
	// Volume 是音量 (edge 用), 如 "+0%", "-50%"
	Volume string `toml:"volume"` // 音量 (edge-tts)
	// Pitch 是音调 (edge 用), 如 "+0Hz", "-50Hz"
	Pitch string `toml:"pitch"` // 音调 (edge-tts)

}

// TTSProviderConfig 保存一个远程 TTS 供应商的连接参数。
// 独立配置可以避免切换 provider 时误把其他平台的密钥发送出去。
type TTSProviderConfig struct {
	BaseURL     string  `toml:"base_url"`
	APIKey      string  `toml:"-"`
	WorkspaceID string  `toml:"workspace_id"`
	Model       string  `toml:"model"`
	Voice       string  `toml:"voice"`
	SampleRate  int     `toml:"sample_rate"`
	Speed       float64 `toml:"speed"`
}

// ProfileConfig 放用户画像分析配置.
// 方案 16.12.6 节: 每轮对话打标签, 达到阈值后用统计数据调 LLM 归纳画像.
type ProfileConfig struct {
	// AnalysisThreshold 是触发画像分析所需的对话轮数.
	// 每轮对话累加, 达到这个数后后台异步调 LLM 归纳画像, 然后计数器清零.
	// 默认 30 轮.
	AnalysisThreshold int `toml:"analysis_threshold"`
}

// EmotionConfig 放情绪感知配置.
// 方案 16.12.6 节: 连续相同情绪合并为一段, 记录开始/结束/持续时长.
type EmotionConfig struct {
	// MaxHistorySessions 是注入 system prompt 的情绪段最大条数.
	// 超过的旧段不注入, 只保留最近的. 默认 5 条.
	MaxHistorySessions int `toml:"max_history_sessions"`
}

// ReminderConfig 放待办提醒配置.
// 方案 16.12.6 节: 从对话提取 + 用户确认, 定时扫描到期提醒注入 system prompt.
type ReminderConfig struct {
	// ScanIntervalSeconds 是后台扫描到期提醒的间隔 (秒).
	// 后台 goroutine 每隔这个时间查一次 pending 且到期的提醒.
	// 默认 60 秒.
	ScanIntervalSeconds int `toml:"scan_interval_seconds"`
	// MaxInjectReminders 是注入 system prompt 的最大提醒条数.
	// 超过的不注入, 只保留最近到期的. 默认 5 条.
	MaxInjectReminders int `toml:"max_inject_reminders"`
}
