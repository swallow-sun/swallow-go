// types.go 放 settings 包的类型定义和常量。
//
// 做的事情：
//  1. 定义配置键名常量：llm.base_url、llm.model、llm.api_key、auth.owner_token。
//  2. 定义加密相关常量：算法名（aes-256-gcm）、密钥版本、主密钥大小（32 字节）、密钥文件后缀（.key）。
//  3. 定义模型连通性测试常量：测试提示词和超时时间。
//  4. 定义 Service 结构体：持有 repo、主密钥、回滚用的原始配置快照。
//  5. 定义 OriginalSecret/OriginalSetting：保存修改前的状态，用于模型验证失败时回滚。
package settings

import (
	"time"

	"github.com/swallow-sun/swallow-go/internal/data"
)

const (
	SettingLLMBaseURL = "llm.base_url"
	SettingLLMModel   = "llm.model"
	SecretLLMAPIKey   = "llm.api_key"
	SecretOwnerToken  = "auth.owner_token"

	ValueTypeString     = "string"
	AlgorithmAESGCM     = "aes-256-gcm"
	CurrentKeyVersion   = 1
	MasterKeySize       = 32
	MasterKeyFileSuffix = ".key"
	ModelTestPrompt     = "这是启动配置连通性测试。请只回复 OK。"
	ModelTestTimeout    = 20 * time.Second
)

// Service 管理数据库运行配置及敏感配置的加解密。
type Service struct {
	repo             data.Repository
	masterKey        []byte
	runtimeChanged   bool
	originalSecrets  map[string]OriginalSecret
	originalSettings map[string]OriginalSetting
}

// OriginalSecret 保存本次启动修改敏感配置前的状态，用于模型验证失败时回滚。
type OriginalSecret struct {
	Value  data.EncryptedSecret
	Exists bool
}

// OriginalSetting 保存本次启动修改普通配置前的状态，用于模型验证失败时回滚。
type OriginalSetting struct {
	Value  data.AppSetting
	Exists bool
}
