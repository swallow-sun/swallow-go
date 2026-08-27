// provider.go 根据配置显式选择唯一的 ASR 供应商。
// 本工厂不包含重试切换或自动降级：选中哪一家，请求就只会发给哪一家。
package asr

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	ProviderDisabled     = "disabled"
	ProviderAliyun       = "aliyun"
	ProviderSiliconFlow  = "siliconflow"
	ProviderGroq         = "groq"
	ProviderOpenAICompat = "openai_compatible"
)

// NewProvider 根据 name 创建一个且仅创建一个 ASR Provider。
// disabled 或空字符串表示服务端不启用远程 ASR；未知名称直接返回错误。
func NewProvider(name string, cfg Config) (Provider, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	switch name {
	case "", ProviderDisabled:
		return nil, nil
	case ProviderAliyun:
		if err := validateProviderConfig(name, cfg); err != nil {
			return nil, err
		}
		if !isAliyunSyncASRModel(cfg.Model) {
			return nil, fmt.Errorf("ASR provider %q requires a qwen3-asr-flash model", name)
		}
		return NewAliyun(cfg), nil
	case ProviderSiliconFlow, ProviderGroq, ProviderOpenAICompat:
		if err := validateProviderConfig(name, cfg); err != nil {
			return nil, err
		}
		return NewOpenAICompat(cfg), nil
	default:
		return nil, fmt.Errorf(
			"unsupported ASR provider %q; supported values: aliyun, siliconflow, groq, openai_compatible, disabled",
			name,
		)
	}
}

// isAliyunSyncASRModel 只允许 chat/completions 协议对应的同步模型。
// 日期快照形如 qwen3-asr-flash-2026-08-01；realtime、filetrans 等模型
// 使用不同协议，不能因为名称前缀相同就误放行。
func isAliyunSyncASRModel(model string) bool {
	const baseModel = "qwen3-asr-flash"
	model = strings.ToLower(strings.TrimSpace(model))
	if model == baseModel {
		return true
	}
	const dateLayout = "2006-01-02"
	dateSuffix := strings.TrimPrefix(model, baseModel+"-")
	if dateSuffix == model || len(dateSuffix) != len(dateLayout) {
		return false
	}
	_, err := time.Parse(dateLayout, dateSuffix)
	return err == nil
}

// configuredLanguage 合并单次请求和 Provider 默认语种。
// auto 明确表示自动检测，因此转换为空字符串，不发送 language 字段。
func configuredLanguage(requestLanguage, defaultLanguage string) string {
	language := strings.ToLower(strings.TrimSpace(requestLanguage))
	if language == "" {
		language = strings.ToLower(strings.TrimSpace(defaultLanguage))
	}
	if language == "auto" {
		return ""
	}
	return language
}

func validateProviderConfig(name string, cfg Config) error {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(cfg.BaseURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("ASR provider %q requires a valid HTTP(S) base_url", name)
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return fmt.Errorf("ASR provider %q requires api_key", name)
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return fmt.Errorf("ASR provider %q requires model", name)
	}
	return nil
}
