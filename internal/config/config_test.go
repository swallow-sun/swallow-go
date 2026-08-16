package config

import (
	"strings"
	"testing"
)

func validConfig() Config {
	return Config{
		Server:   ServerConfig{Port: 8888},
		LLM:      LLMConfig{BaseURL: "https://api.example.com/v1", Model: "chat"},
		Database: DatabaseConfig{Path: "data/swallow.db", MigrationsDir: "script/migrations"},
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Config)
		want string
	}{
		{name: "valid"},
		{name: "bad port", edit: func(c *Config) { c.Server.Port = 0 }, want: "server.port"},
		{name: "bad URL", edit: func(c *Config) { c.LLM.BaseURL = "ftp://example.com" }, want: "llm.base_url"},
		{name: "empty model", edit: func(c *Config) { c.LLM.Model = " " }, want: "llm.model"},
		{name: "empty database", edit: func(c *Config) { c.Database.Path = "" }, want: "database.path"},
		{name: "empty migrations directory", edit: func(c *Config) { c.Database.MigrationsDir = "" }, want: "database.migrations_dir"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			if tt.edit != nil {
				tt.edit(&cfg)
			}
			err := cfg.Validate()
			if tt.want == "" && err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)) {
				t.Fatalf("Validate() error = %v, want field %q", err, tt.want)
			}
		})
	}
}

func TestApplyEnvironment(t *testing.T) {
	t.Setenv("SWALLOW_SERVER_PORT", "9999")
	t.Setenv("SWALLOW_LLM_API_KEY", "secret-from-env")
	t.Setenv("SWALLOW_DATABASE_MIGRATIONS_DIR", "custom/migrations")
	cfg := validConfig()
	if err := applyEnvironment(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != 9999 {
		t.Fatalf("port = %d", cfg.Server.Port)
	}
	if cfg.LLM.APIKey != "secret-from-env" {
		t.Fatal("API key override was not applied")
	}
	if cfg.Database.MigrationsDir != "custom/migrations" {
		t.Fatal("migrations directory override was not applied")
	}
}
