// config.go 放配置加载和校验逻辑.
//
// 做的事情:
//  1. Load:从 TOML 文件加载配置(config.toml + config.local.toml),后者覆盖前者.
//  2. ValidateRuntime:校验运行时配置的合法性(端口范围,路径非空等).
//
// 只从 TOML 文件加载,不读取环境变量.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// Load 从 toml 文件加载配置.
// 优先读 config.local.toml(个人配置,不提交 git),没有再读 config.toml.
// 流程:先读 config.toml → 如果有 config.local.toml 就覆盖加载 → 校验启动配置.
func Load() (*Config, error) {
	// 声明一个空的 Config 结构体,后面用 toml.DecodeFile 往里填值
	var cfg Config

	// os.Stat 检查文件存不存在.如果 config.toml 不存在直接报错,因为这是必须的配置文件
	// os.Stat 返回文件信息(FileInfo)和错误,如果文件不存在会返回一个 error
	if _, err := os.Stat("config.toml"); err != nil {
		return nil, fmt.Errorf("config.toml: %w", err)
	}
	// toml.DecodeFile 读 config.toml 文件,把里面的内容解析后塞进 cfg 结构体
	// toml 标签(如 `toml:"app"`)决定每个字段对应 TOML 里的哪个段
	if _, err := toml.DecodeFile("config.toml", &cfg); err != nil {
		return nil, fmt.Errorf("decode config.toml: %w", err)
	}
	// 记录一下:配置是从 config.toml 来的
	cfg.LoadedSources = append(cfg.LoadedSources, "config.toml")

	// 检查 config.local.toml 是否存在(个人配置文件,不提交 git)
	// os.Stat 的错误是 nil 说明文件存在
	if _, err := os.Stat("config.local.toml"); err == nil {
		// config.local.toml 存在,再 DecodeFile 一次,覆盖 config.toml 里的同名配置
		// 举例:config.toml 里 server.port=8080,config.local.toml 里 server.port=9090
		// 解码完后 cfg.Server.Port 就是 9090(后者覆盖前者)
		if _, err := toml.DecodeFile("config.local.toml", &cfg); err != nil {
			return nil, fmt.Errorf("decode config.local.toml: %w", err)
		}
		cfg.LoadedSources = append(cfg.LoadedSources, "config.local.toml")
	} else if !os.IsNotExist(err) {
		// 文件不是"不存在"而是别的错误(比如没权限读取),就报错
		// os.IsNotExist 判断错误是不是"文件不存在"这一种
		return nil, fmt.Errorf("stat config.local.toml: %w", err)
	}
	// 旧配置没有 [log] 时补默认值，保证升级后仍能直接启动。
	cfg.applyLogDefaults()
	// 加载完后校验启动配置(环境,端口,数据库路径等),不合法就报错
	if err := cfg.ValidateBootstrap(); err != nil {
		return nil, err
	}
	// 返回配置指针,调用方拿着这个 cfg 就能拿到所有配置值
	return &cfg, nil
}

// applyLogDefaults 根据运行环境补齐未显式配置的日志等级和目录。
func (cfg *Config) applyLogDefaults() {
	if strings.TrimSpace(cfg.Log.Directory) == "" {
		cfg.Log.Directory = DefaultLogDirectory
	}
	if strings.TrimSpace(cfg.Log.Level) != "" {
		cfg.Log.Level = strings.ToLower(strings.TrimSpace(cfg.Log.Level))
	} else if cfg.App.Environment == "production" {
		cfg.Log.Level = DefaultProductionLogLevel
	} else {
		cfg.Log.Level = DefaultDevelopmentLogLevel
	}
	if cfg.Log.MaxSizeMB == 0 {
		cfg.Log.MaxSizeMB = DefaultLogMaxSizeMB
	}
	if cfg.Log.MaxBackups == 0 {
		cfg.Log.MaxBackups = DefaultLogMaxBackups
	}
	if cfg.Log.MaxAgeDays == 0 {
		cfg.Log.MaxAgeDays = DefaultLogMaxAgeDays
	}
	if cfg.Log.Compress == nil {
		compress := DefaultLogCompress
		cfg.Log.Compress = &compress
	}
}

// Validate 校验服务端口,LLM 地址,模型名称和数据库路径.
// API Key 允许为空,由确实需要调用 LLM 的入口决定是否强制要求.
// 流程:先校验启动配置(数据库前必须可用的)→ 再校验运行配置(调 LLM 的).
func (cfg Config) Validate() error {
	// 先校验启动配置:环境,端口,数据库路径,迁移目录
	if err := cfg.ValidateBootstrap(); err != nil {
		return err
	}
	// 再校验运行配置:LLM 地址和模型名
	return cfg.ValidateRuntime()
}

// ValidateBootstrap 校验打开数据库前必须可用的最小启动配置.
// 这些配置在加载阶段就必须合法,不合法连数据库都打不开.
func (cfg Config) ValidateBootstrap() error {
	// 运行环境只能是 development 或 production,其他值直接报错
	if cfg.App.Environment != "development" && cfg.App.Environment != "production" {
		return fmt.Errorf("app.environment must be development or production")
	}
	// 直接构造 Config 的调用方也允许省略日志配置，语义与 Load 的默认值一致。
	logLevel := strings.ToLower(strings.TrimSpace(cfg.Log.Level))
	if logLevel == "" {
		if cfg.App.Environment == "production" {
			logLevel = DefaultProductionLogLevel
		} else {
			logLevel = DefaultDevelopmentLogLevel
		}
	}
	switch logLevel {
	case LogLevelDebug, LogLevelInfo, LogLevelWarn, LogLevelError:
	default:
		return fmt.Errorf("log.level must be debug, info, warn or error")
	}
	if cfg.Log.MaxSizeMB < 0 {
		return fmt.Errorf("log.max_size_mb must not be negative")
	}
	if cfg.Log.MaxBackups < 0 {
		return fmt.Errorf("log.max_backups must not be negative")
	}
	if cfg.Log.MaxAgeDays < 0 {
		return fmt.Errorf("log.max_age_days must not be negative")
	}
	// 端口号必须在 1-65535 范围内,0 和负数,超过 65535 都不行
	if cfg.Server.Port < 1 || cfg.Server.Port > 65535 {
		return fmt.Errorf("server.port must be between 1 and 65535")
	}
	// 数据库路径不能为空,空了就不知道往哪存数据
	// strings.TrimSpace 去掉首尾空格后判断是不是空字符串
	if strings.TrimSpace(cfg.Database.Path) == "" {
		return fmt.Errorf("database.path must not be empty")
	}
	// 迁移目录不能为空,空了迁移器就找不到 SQL 文件
	if strings.TrimSpace(cfg.Database.MigrationsDir) == "" {
		return fmt.Errorf("database.migrations_dir must not be empty")
	}
	// owner token 不能为空, 空了所有需要认证的接口(chat/history/dashboard)都会返回 503.
	// 必须在启动阶段就报错, 不要让服务带着没配 token 的状态启动.
	if strings.TrimSpace(cfg.Auth.OwnerToken) == "" {
		return fmt.Errorf("auth.owner_token must not be empty")
	}
	return nil
}

// ValidateRuntime 校验从数据库加载后的模型运行配置.
// 这一步在配置写进数据库后调用,确保 LLM 地址和模型名都是合法的.
func (cfg Config) ValidateRuntime() error {
	// url.ParseRequestURI 解析 URL 字符串,检查是不是合法的 HTTP(S) 地址
	// 返回解析后的 URL 结构体和错误,地址不合法就报错
	parsed, err := url.ParseRequestURI(cfg.LLM.BaseURL)
	// 三个条件必须同时满足才算合法:
	// 1. 解析没出错(err == nil)
	// 2. 协议必须是 http 或 https(不能是 ftp,file 等)
	// 3. 主机名不能为空(比如 "http://" 就不合法)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("llm.base_url must be a valid HTTP(S) URL")
	}
	// 模型名不能为空,空了就不知道调哪个模型
	if strings.TrimSpace(cfg.LLM.Model) == "" {
		return fmt.Errorf("llm.model must not be empty")
	}
	return nil
}
