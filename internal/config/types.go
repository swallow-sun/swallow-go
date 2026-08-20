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
	// Database 对应 [database] 段,放数据库路径和迁移目录
	Database DatabaseConfig `toml:"database"`
	// Auth 对应 [auth] 段,放身份鉴权配置(比如主人令牌)
	Auth AuthConfig `toml:"auth"`
	// Debug 对应 [debug] 段,放调试配置(如 pprof 端口)
	Debug DebugConfig `toml:"debug"`
	// Metrics 对应 [metrics] 段,放 Prometheus 指标服务配置
	Metrics MetricsConfig `toml:"metrics"`
	// Log 对应 [log] 段，控制统一日志最低级别和本地目录。
	Log LogConfig `toml:"log"`
	// Memory 对应 [memory] 段,放长期记忆安全配置.
	Memory MemoryConfig `toml:"memory"`
	// ASR 对应 [asr] 段, 放语音识别 (ASR) 配置.
	ASR ASRConfig `toml:"asr"`
	// TTS 对应 [tts] 段, 放语音合成 (TTS) 配置.
	TTS TTSConfig `toml:"tts"`
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
	APIKey string `toml:"api_key"` // 调用 LLM 用的密钥,允许在配置文件中为空(由数据库加密配置补充)
	// Model 是默认用的模型名,比如 "gpt-4o","deepseek-chat"
	Model string `toml:"model"` // 默认模型名,如 "gpt-4o"
}

// DatabaseConfig 放数据库连接配置.
type DatabaseConfig struct {
	// Path 是 SQLite 数据库文件的路径,比如 "data/swallow.db"
	// 数据库引擎根据这个路径打开或创建 .db 文件
	Path string `toml:"path"` // SQLite 数据库文件路径
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

// ASRConfig 放语音识别 (ASR) 服务连接配置.
// 当前用 Groq Whisper, 兼容 OpenAI /v1/audio/transcriptions 接口.
type ASRConfig struct {
	// Provider 是 ASR 供应商名称, 如 "groq"
	Provider string `toml:"provider"` // ASR 供应商名称
	// BaseURL 是 ASR 服务的 API 基础地址, 如 "https://api.groq.com/openai/v1"
	// 后面的 /audio/transcriptions 路径由 provider 层自己拼
	BaseURL string `toml:"base_url"` // ASR 服务的 API 基础地址
	// APIKey 是调 ASR 用的密钥
	APIKey string `toml:"api_key"` // ASR API 密钥
	// Model 是 ASR 模型名, 如 "whisper-large-v3"
	Model string `toml:"model"` // ASR 模型名
}

// TTSConfig 放语音合成 (TTS) 服务配置.
// 当前用 edge-tts, 微软免费 TTS, 不需要 API key.
type TTSConfig struct {
	// Voice 是默认语音名称, 如 "zh-CN-XiaoxiaoNeural" (女声晓晓)
	Voice string `toml:"voice"` // 默认语音名称
	// OutputFormat 是音频输出格式, 如 "audio-48khz-192kbitrate-mono-mp3"
	OutputFormat string `toml:"output_format"` // 音频输出格式
	// Rate 是语速, 如 "+0%", "-10%", "+20%"
	Rate string `toml:"rate"` // 语速
	// Volume 是音量, 如 "+0%", "-50%"
	Volume string `toml:"volume"` // 音量
	// Pitch 是音调, 如 "+0Hz", "-50Hz"
	Pitch string `toml:"pitch"` // 音调
}
