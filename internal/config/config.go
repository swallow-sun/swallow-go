// Package config 负责加载、覆盖和校验 Swallow-Go 的运行配置。
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Load 从 toml 文件加载配置。
// 优先读 config.local.toml（个人配置，不提交 git），没有再读 config.toml。
func Load() (*Config, error) {
	var cfg Config

	// 显式配置文件适合部署环境；否则先读公共配置，再叠加本地配置。
	if f := os.Getenv("SWALLOW_CONFIG"); f != "" {
		if _, err := toml.DecodeFile(f, &cfg); err != nil {
			return nil, fmt.Errorf("decode %s: %w", f, err)
		}
	} else {
		if _, err := os.Stat("config.toml"); err != nil {
			return nil, fmt.Errorf("config.toml: %w", err)
		}
		if _, err := toml.DecodeFile("config.toml", &cfg); err != nil {
			return nil, fmt.Errorf("decode config.toml: %w", err)
		}
		if _, err := os.Stat("config.local.toml"); err == nil {
			if _, err := toml.DecodeFile("config.local.toml", &cfg); err != nil {
				return nil, fmt.Errorf("decode config.local.toml: %w", err)
			}
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("stat config.local.toml: %w", err)
		}
	}

	if err := applyEnvironment(&cfg); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// applyEnvironment 使用 SWALLOW_ 前缀的环境变量覆盖文件配置。
// 端口变量无法解析为整数时返回错误，其余字段的合法性由 Validate 统一检查。
func applyEnvironment(cfg *Config) error {
	if value := os.Getenv("SWALLOW_SERVER_PORT"); value != "" {
		port, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("SWALLOW_SERVER_PORT must be an integer: %w", err)
		}
		cfg.Server.Port = port
	}
	if value := os.Getenv("SWALLOW_LLM_BASE_URL"); value != "" {
		cfg.LLM.BaseURL = value
	}
	if value := os.Getenv("SWALLOW_LLM_API_KEY"); value != "" {
		cfg.LLM.APIKey = value
	}
	if value := os.Getenv("SWALLOW_LLM_MODEL"); value != "" {
		cfg.LLM.Model = value
	}
	if value := os.Getenv("SWALLOW_DATABASE_PATH"); value != "" {
		cfg.Database.Path = value
	}
	if value := os.Getenv("SWALLOW_DATABASE_MIGRATIONS_DIR"); value != "" {
		cfg.Database.MigrationsDir = value
	}
	return nil
}

// Validate 校验服务端口、LLM 地址、模型名称和数据库路径。
// API Key 允许为空，由确实需要调用 LLM 的入口决定是否强制要求。
func (cfg Config) Validate() error {
	if cfg.Server.Port < 1 || cfg.Server.Port > 65535 {
		return fmt.Errorf("server.port must be between 1 and 65535")
	}
	parsed, err := url.ParseRequestURI(cfg.LLM.BaseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("llm.base_url must be a valid HTTP(S) URL")
	}
	if strings.TrimSpace(cfg.LLM.Model) == "" {
		return fmt.Errorf("llm.model must not be empty")
	}
	if strings.TrimSpace(cfg.Database.Path) == "" {
		return fmt.Errorf("database.path must not be empty")
	}
	if strings.TrimSpace(cfg.Database.MigrationsDir) == "" {
		return fmt.Errorf("database.migrations_dir must not be empty")
	}
	return nil
}
