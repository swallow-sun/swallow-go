package config

// Config 是整个项目的配置根结构。
type Config struct {
	Server   ServerConfig   `toml:"server"`
	LLM      LLMConfig      `toml:"llm"`
	Database DatabaseConfig `toml:"database"`
}

type ServerConfig struct {
	Port int `toml:"port"`
}

type LLMConfig struct {
	BaseURL string `toml:"base_url"`
	APIKey  string `toml:"api_key"`
	Model   string `toml:"model"`
}

type DatabaseConfig struct {
	Path          string `toml:"path"`
	MigrationsDir string `toml:"migrations_dir"`
}
