// types.go 放 config 包的类型定义。
//
// 做的事情：
//  1. 定义 Config 根结构体：包含 App、Server、LLM、Database、Auth 五组子配置。
//  2. 定义各子配置结构体：AppConfig（运行环境）、ServerConfig（端口）、LLMConfig（模型连接）、DatabaseConfig（数据库路径和迁移目录）、AuthConfig（鉴权）。
//  3. 所有字段用 toml 标签映射配置文件，LoadedSources 记录实际加载的文件路径列表。
package config

// Config 是整个项目的配置根结构。
type Config struct {
	App           AppConfig      `toml:"app"`
	Server        ServerConfig   `toml:"server"`
	LLM           LLMConfig      `toml:"llm"`
	Database      DatabaseConfig `toml:"database"`
	Auth          AuthConfig     `toml:"auth"`
	LoadedSources []string       `toml:"-"`
}

// AppConfig 放应用级配置（运行环境等）。
type AppConfig struct {
	Environment string `toml:"environment"` // 运行环境：development 或 production
}

// ServerConfig 放 HTTP 服务相关配置。
type ServerConfig struct {
	Port int `toml:"port"` // HTTP 监听端口，1-65535
}

// LLMConfig 放 LLM 服务连接配置。
type LLMConfig struct {
	BaseURL string `toml:"base_url"` // LLM 服务的 API 基础地址，如 "https://api.openai.com/v1"
	APIKey  string `toml:"api_key"`  // 调用 LLM 用的密钥，允许在配置文件中为空（由数据库加密配置补充）
	Model   string `toml:"model"`    // 默认模型名，如 "gpt-4o"
}

// DatabaseConfig 放数据库连接配置。
type DatabaseConfig struct {
	Path          string `toml:"path"`           // SQLite 数据库文件路径
	MigrationsDir string `toml:"migrations_dir"` // 版本化 SQL 迁移文件所在目录
}

// AuthConfig 放身份认证配置。
type AuthConfig struct {
	OwnerToken string `toml:"owner_token"` // 主人令牌，用于鉴权（允许为空，由数据库加密配置补充）
}
