// config.go 放配置加载和校验逻辑。
//
// 做的事情：
//  1. Load：从 TOML 文件加载配置（config.toml + config.local.toml），后者覆盖前者。
//  2. ValidateRuntime：校验运行时配置的合法性（端口范围、路径非空等）。
//
// 只从 TOML 文件加载，不读取环境变量。
package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// Load 从 toml 文件加载配置。
// 优先读 config.local.toml（个人配置，不提交 git），没有再读 config.toml。
func Load() (*Config, error) {
	var cfg Config

	if _, err := os.Stat("config.toml"); err != nil {
		return nil, fmt.Errorf("config.toml: %w", err)
	}
	if _, err := toml.DecodeFile("config.toml", &cfg); err != nil {
		return nil, fmt.Errorf("decode config.toml: %w", err)
	}
	cfg.LoadedSources = append(cfg.LoadedSources, "config.toml")
	if _, err := os.Stat("config.local.toml"); err == nil {
		if _, err := toml.DecodeFile("config.local.toml", &cfg); err != nil {
			return nil, fmt.Errorf("decode config.local.toml: %w", err)
		}
		cfg.LoadedSources = append(cfg.LoadedSources, "config.local.toml")
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("stat config.local.toml: %w", err)
	}
	if err := cfg.ValidateBootstrap(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate 校验服务端口、LLM 地址、模型名称和数据库路径。
// API Key 允许为空，由确实需要调用 LLM 的入口决定是否强制要求。
func (cfg Config) Validate() error {
	if err := cfg.ValidateBootstrap(); err != nil {
		return err
	}
	return cfg.ValidateRuntime()
}

// ValidateBootstrap 校验打开数据库前必须可用的最小启动配置。
func (cfg Config) ValidateBootstrap() error {
	if cfg.App.Environment != "development" && cfg.App.Environment != "production" {
		return fmt.Errorf("app.environment must be development or production")
	}
	if cfg.Server.Port < 1 || cfg.Server.Port > 65535 {
		return fmt.Errorf("server.port must be between 1 and 65535")
	}
	if strings.TrimSpace(cfg.Database.Path) == "" {
		return fmt.Errorf("database.path must not be empty")
	}
	if strings.TrimSpace(cfg.Database.MigrationsDir) == "" {
		return fmt.Errorf("database.migrations_dir must not be empty")
	}
	return nil
}

// ValidateRuntime 校验从数据库加载后的模型运行配置。
func (cfg Config) ValidateRuntime() error {
	parsed, err := url.ParseRequestURI(cfg.LLM.BaseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("llm.base_url must be a valid HTTP(S) URL")
	}
	if strings.TrimSpace(cfg.LLM.Model) == "" {
		return fmt.Errorf("llm.model must not be empty")
	}
	return nil
}
