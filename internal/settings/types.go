// types.go 放 settings 包的类型定义和常量.
//
// 做的事情:
//  1. 定义配置键名常量:llm.base_url,llm.model,llm.api_key,auth.owner_token.
//  2. 定义加密相关常量:算法名(aes-256-gcm),密钥版本,主密钥大小(32 字节),密钥文件后缀(.key).
//  3. 定义模型连通性测试常量:测试提示词和超时时间.
//  4. 定义 Service 结构体:持有 repo,主密钥,回滚用的原始配置快照.
//  5. 定义 OriginalSecret/OriginalSetting:保存修改前的状态,用于模型验证失败时回滚.
package settings

import (
	"time"

	"github.com/swallow-sun/swallow-go/internal/data"
)

// 配置键名常量.
// 这些常量是数据库里 app_settings 表和 encrypted_secrets 表的 key 字段值,
// 代码里用常量而不是写死字符串,防止拼写不一致导致查不到配置.
const (
	// SettingLLMBaseURL 是 LLM 服务基础地址的键名,存明文配置
	SettingLLMBaseURL = "llm.base_url"
	// SettingLLMModel 是默认模型名的键名,存明文配置
	SettingLLMModel   = "llm.model"
	// SecretLLMAPIKey 是 LLM API 密钥的键名,存加密密文
	SecretLLMAPIKey   = "llm.api_key"
	// SecretOwnerToken 是主人令牌的键名,存加密密文
	SecretOwnerToken  = "auth.owner_token"

	// ValueTypeString 表示配置值的类型是字符串
	ValueTypeString     = "string"
	// AlgorithmAESGCM 表示加密算法用 AES-256-GCM
	// AES 是对称加密算法,GCM 是一种加密模式,能同时保证机密性和完整性
	AlgorithmAESGCM     = "aes-256-gcm"
	// CurrentKeyVersion 是当前主密钥的版本号,将来换密钥时可以用版本号区分新旧密文
	CurrentKeyVersion   = 1
	// MasterKeySize 是 AES-256 主密钥的字节长度,必须是 32 字节(256 位)
	MasterKeySize       = 32
	// MasterKeyFileSuffix 是主密钥文件的文件名后缀,比如 swallow.db.key
	MasterKeyFileSuffix = ".key"
	// ModelTestPrompt 是连通性测试时发给模型的提示词,要求模型只回复 OK
	ModelTestPrompt     = "Startup config connectivity test. Please reply with OK only."
	// ModelTestTimeout 是连通性测试的超时时间,超过 20 秒还没收到回复就算失败
	ModelTestTimeout    = 20 * time.Second
)

// Service 管理数据库运行配置及敏感配置的加解密.
// 它持有数据库仓库,主密钥和回滚用的原始配置快照.
type Service struct {
	// repo 是数据库仓库,用来读写 app_settings 和 encrypted_secrets 表
	repo             data.Repository
	// masterKey 是 AES-256 的主密钥,32 字节,用来加解密敏感配置
	masterKey        []byte
	// runtimeChanged 标记本次运行有没有改过配置,改过的话需要做模型连通性测试
	runtimeChanged   bool
	// originalSecrets 保存修改敏感配置前的原始状态,测试失败时用来回滚
	originalSecrets  map[string]OriginalSecret
	// originalSettings 保存修改普通配置前的原始状态,测试失败时用来回滚
	originalSettings map[string]OriginalSetting
}

// OriginalSecret 保存本次启动修改敏感配置前的状态,用于模型验证失败时回滚.
type OriginalSecret struct {
	// Value 是修改前的加密密文内容,回滚时把它写回数据库
	Value  data.EncryptedSecret
	// Exists 表示修改前数据库里有没有这条记录,false 表示原本不存在(回滚时要删掉)
	Exists bool
}

// OriginalSetting 保存本次启动修改普通配置前的状态,用于模型验证失败时回滚.
type OriginalSetting struct {
	// Value 是修改前的配置内容,回滚时把它写回数据库
	Value  data.AppSetting
	// Exists 表示修改前数据库里有没有这条记录,false 表示原本不存在(回滚时要删掉)
	Exists bool
}
