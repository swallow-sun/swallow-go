// service.go 放运行配置服务:管理数据库运行配置及敏感配置的加解密.
//
// 做的事情:
//  1. 加载主密钥:从配置的本地 key 文件读取 AES-256 主密钥,首次运行时自动生成.
//  2. LoadInto:把数据库里的配置加载到 config.Config,覆盖 TOML 里的初始值.
//  3. SetSetting/SetSecret:运行时修改普通配置和加密密钥,修改后做模型连通性测试,失败自动回滚.
//  4. encrypt/decrypt:用 AES-256-GCM 加解密敏感配置.
//  5. seedSetting/seedSecret:首次启动时写入默认配置值(仅在不存在的时写入).
//
// 安全设计:修改配置后用模型做连通性测试,测试失败自动回滚到修改前的值.
package settings

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/swallow-sun/swallow-go/internal/config"
	"github.com/swallow-sun/swallow-go/internal/data"
	"github.com/swallow-sun/swallow-go/internal/provider/llm"
	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
)

// New 创建运行配置服务.
// 主密钥从指定的本地 key 文件读取;首次运行时自动生成.
// 参数:
//   - repo: 数据库仓库,用来读写配置表
//   - masterKeyPath: 主密钥文件路径
//
// 返回值:
//   - *Service: 初始化好的配置服务
//   - error: 主密钥加载或生成失败时返回错误
func New(repo data.Repository, masterKeyPath string) (*Service, error) {
	// loadOrCreateMasterKey 尝试读取主密钥文件,文件不存在就生成一个新的
	// 返回三样东西:base64 编码的密钥字符串,是否是首次创建的,错误
	// PostgreSQL 没有本地数据库文件，因此密钥路径必须与数据库连接配置解耦。
	encodedKey, created, err := loadOrCreateMasterKey(masterKeyPath)
	if err != nil {
		return nil, err
	}
	// 根据是首次创建还是读取已有,打不同的日志
	if created {
		logger.Info("Generated local master key file", zap.String("key_file", masterKeyPath))
	} else {
		logger.Debug("Read local master key file", zap.String("key_file", masterKeyPath))
	}
	// base64.StdEncoding.DecodeString 把 base64 编码的密钥字符串解码成原始字节
	// 举例:encodedKey = "dGVzdA==" → masterKey = [116, 101, 115, 116]
	// strings.TrimSpace 去掉首尾的换行符和空格,防止文件末尾的换行导致解码失败
	masterKey, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedKey))
	if err != nil {
		return nil, fmt.Errorf("decode local master key as base64: %w", err)
	}
	// 主密钥必须是 32 字节(256 位),不够或超了都不行
	// AES-256 算法规定密钥长度必须是 32 字节
	if len(masterKey) != MasterKeySize {
		return nil, fmt.Errorf("local master key must decode to %d bytes", MasterKeySize)
	}
	// 构造 Service 结构体,初始化所有字段
	// originalSecrets 和 originalSettings 用 make 初始化成空 map,后面修改配置时往里存原始值
	return &Service{
		repo: repo, masterKey: masterKey,
		originalSecrets:  make(map[string]OriginalSecret),
		originalSettings: make(map[string]OriginalSetting),
	}, nil
}

// LoadInto 首次运行时保存启动默认值,之后用数据库配置覆盖运行配置.
// 参数:
//   - ctx: 上下文,支持超时取消
//   - cfg: 从 TOML 加载的配置,函数会把数据库里的值覆盖进去
//
// 返回值:
//   - error: 如果加载或连通性测试失败就返回错误
//
// 流程:
//  1. 先 seed:把 TOML 里的初始值写进数据库(只在数据库没有的时候写)
//  2. 再读:从数据库把配置读出来,覆盖到 cfg 里
//  3. 如果期间改过配置(seed 写了新值),就做一次模型连通性测试,失败就回滚
func (s *Service) LoadInto(ctx context.Context, cfg *config.Config) (loadErr error) {
	// defer 延迟执行:如果函数要返回了(不管正常还是异常),检查一下要不要回滚
	// 只有 loadErr != nil(出错了)且 runtimeChanged == true(改过配置)才回滚
	// 用具名返回值 loadErr 就是给 defer 用的,defer 里能读到最终的返回值
	defer func() {
		if loadErr == nil || !s.runtimeChanged {
			return
		}
		// 回滚所有改过的配置,恢复到写入前的状态
		rollbackErr := s.rollbackChanges(ctx)
		if rollbackErr != nil {
			// 回滚也失败了,两个错误一起返回
			// errors.Join 把多个错误打包成一个返回
			logger.Error("Failed to load runtime config, rollback also failed", zap.Error(rollbackErr))
			loadErr = errors.Join(loadErr, rollbackErr)
			return
		}
		logger.Warn("Failed to load runtime config, restored to pre-write state")
	}()

	// 第一步:seed -- 把 TOML 里的初始值写进数据库(只在不存在时写入)
	// seedSetting 处理普通配置(明文),seedSecret 处理敏感配置(加密)
	// 比如 TOML 里 llm.base_url = "https://..." → 第一次启动写进数据库
	if err := s.seedSetting(ctx, SettingLLMBaseURL, cfg.LLM.BaseURL, "LLM service base URL"); err != nil {
		return err
	}
	if err := s.seedSetting(ctx, SettingLLMModel, cfg.LLM.Model, "Default LLM model name"); err != nil {
		return err
	}
	// owner token 仍按现有身份引导流程初始化；所有供应商 API Key 严禁从 TOML seed。
	if err := s.seedSecret(ctx, SecretOwnerToken, cfg.Auth.OwnerToken); err != nil {
		return err
	}

	// 第二步:从数据库把配置读出来,覆盖到 cfg 里
	// getSetting 读普通配置(明文存的那种)
	baseURL, err := s.getSetting(ctx, SettingLLMBaseURL)
	if err != nil {
		return err
	}
	model, err := s.getSetting(ctx, SettingLLMModel)
	if err != nil {
		return err
	}
	// getOptionalSecret 读敏感配置并解密,数据库没有的话返回空字符串(不报错)
	llmAPIKey, err := s.getOptionalSecret(ctx, SecretLLMAPIKey)
	if err != nil {
		return err
	}
	asrAPIKey, err := s.getOptionalSecret(ctx, SecretASRAPIKey)
	if err != nil {
		return err
	}
	asrAliyunAPIKey, err := s.getOptionalSecret(ctx, SecretASRAliyunAPIKey)
	if err != nil {
		return err
	}
	asrSiliconFlowAPIKey, err := s.getOptionalSecret(ctx, SecretASRSiliconFlowAPIKey)
	if err != nil {
		return err
	}
	ttsAPIKey, err := s.getOptionalSecret(ctx, SecretTTSAPIKey)
	if err != nil {
		return err
	}
	ttsAliyunAPIKey, err := s.getOptionalSecret(ctx, SecretTTSAliyunAPIKey)
	if err != nil {
		return err
	}
	ownerToken, err := s.getOptionalSecret(ctx, SecretOwnerToken)
	if err != nil {
		return err
	}

	// 把数据库读出来的值覆盖到 cfg 结构体里
	cfg.LLM.BaseURL = baseURL
	cfg.LLM.Model = model
	// API Key 的唯一来源是 encrypted_secrets；即使数据库缺失也绝不回退 TOML。
	cfg.LLM.APIKey = llmAPIKey
	cfg.ASR.APIKey = asrAPIKey
	cfg.ASR.Aliyun.APIKey = asrAliyunAPIKey
	cfg.ASR.SiliconFlow.APIKey = asrSiliconFlowAPIKey
	cfg.TTS.APIKey = ttsAPIKey
	cfg.TTS.Aliyun.APIKey = ttsAliyunAPIKey
	if ownerToken != "" {
		cfg.Auth.OwnerToken = ownerToken
	}
	if strings.TrimSpace(cfg.LLM.APIKey) == "" {
		return fmt.Errorf("database secret %s is required", SecretLLMAPIKey)
	}

	// 第三步:如果本次启动改过配置(seed 写了新值),做一次模型连通性测试
	if s.runtimeChanged {
		var testErr error
		if cfg.LLM.APIKey == "" {
			// API Key 是空的,没法调模型,直接报错
			testErr = fmt.Errorf("LLM API Key is empty after configuration write")
		} else {
			// 用最终配置发一个最小请求给 LLM,验证地址,密钥,模型都可用
			testErr = s.testModel(ctx, cfg)
		}
		if testErr != nil {
			// 测试失败,打日志后回滚到修改前的配置
			logger.Error("Model connectivity test failed after runtime config write", zap.Error(testErr))
			if rollbackErr := s.rollbackChanges(ctx); rollbackErr != nil {
				// 回滚也失败了,两个错误一起返回
				logger.Error("Failed to rollback runtime config after model test failure", zap.Error(rollbackErr))
				return fmt.Errorf("model test failed: %v; rollback failed: %w", testErr, rollbackErr)
			}
			// 回滚成功,告诉调用的人测试失败了
			logger.Warn("Model test failed, restored pre-write runtime config")
			return testErr
		}
		// 测试通过,清空原始配置快照和变更标记(回滚窗口关闭了)
		s.originalSecrets = make(map[string]OriginalSecret)
		s.originalSettings = make(map[string]OriginalSetting)
		s.runtimeChanged = false
	}
	return nil
}

// SetSetting 更新一项普通配置,给后续管理接口调.
// 参数:
//   - key: 配置键名,如 "llm.base_url"
//   - value: 配置值,如 "https://api.openai.com/v1"
//   - description: 配置描述,存进数据库方便人看
func (s *Service) SetSetting(ctx context.Context, key, value, description string) error {
	// key 去掉首尾空格后不能为空
	// strings.TrimSpace 把字符串首尾的空格,制表符,换行符都去掉
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("setting key must not be empty")
	}
	// 先记住这条配置修改前的原始值,万一后面要回滚就靠它
	if err := s.rememberOriginalSetting(ctx, key); err != nil {
		return err
	}
	// UpsertAppSetting:不存在就插入,存在就更新
	// 把配置写进 app_settings 表
	if err := s.repo.UpsertAppSetting(ctx, data.AppSetting{
		Key: key, Value: value, ValueType: ValueTypeString, Description: description,
	}); err != nil {
		return err
	}
	logger.Info("Runtime config updated", zap.String("setting_key", key))
	// 标记本次运行改过配置,后面要做模型连通性测试
	s.runtimeChanged = true
	return nil
}

// SetSecret 加密后更新敏感配置,明文永远不会传给 Repository.
// 参数:
//   - key: 配置键名,如 "llm.api_key"
//   - plaintext: 明文值,如 "sk-xxxxx"
//
// 流程:先记住原始值 → 加密明文 → 写进数据库 → 回读验证 → 失败就回滚
func (s *Service) SetSecret(ctx context.Context, key, plaintext string) error {
	// 先记住这条敏感配置修改前的原始值(加密密文),万一后面要回滚就靠它
	if err := s.rememberOriginalSecret(ctx, key); err != nil {
		return err
	}
	// encrypt 用 AES-256-GCM 加密明文,返回加密后的密文 + nonce + 算法信息
	// 明文经过这里之后就变成密文了,不会以明文形式进数据库
	secret, err := s.encrypt(key, plaintext)
	if err != nil {
		return err
	}
	// UpsertEncryptedSecret:不存在就插入,存在就更新
	// 把加密后的密文写进 encrypted_secrets 表
	if err := s.repo.UpsertEncryptedSecret(ctx, secret); err != nil {
		return err
	}
	// 写完后回读一次,解密后跟原始明文比对,确认写入的密文能正常解密回来
	// 这一步防止加密写入了但解密不出来(比如 nonce 没存对)
	if err := s.verifyPersistedSecret(ctx, key, plaintext); err != nil {
		// 回读验证失败,回滚到写入前的状态
		return s.rollbackAfterWriteFailure(ctx, key, err)
	}
	// 标记本次运行改过配置,后面要做模型连通性测试
	s.runtimeChanged = true
	return nil
}

// seedSetting 首次启动时把 TOML 里的普通配置写进数据库,只在数据库没有的时候写.
// 参数:
//   - key: 配置键名
//   - value: 配置值
//   - description: 配置描述
//
// 逻辑:值为空 → 检查数据库有没有 → 有就跳过,没有就报错
//
//	值非空 → 数据库有就跳过,没有就写入
func (s *Service) seedSetting(ctx context.Context, key, value, description string) error {
	// 值为空的情况:配置文件里没写这个配置
	if strings.TrimSpace(value) == "" {
		// 查一下数据库有没有这条配置
		if _, err := s.repo.GetAppSetting(ctx, key); err == nil {
			// 数据库里已经有了,不用写,直接跳过
			return nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			// 不是"没找到"而是别的数据库错误(连接断了等),报错
			// errors.Is 判断错误是不是某种特定类型,sql.ErrNoRows 表示"查不到记录"
			return fmt.Errorf("check existing setting %s: %w", key, err)
		}
		// 值为空且数据库里也没有,不行,必须得有初始值
		return fmt.Errorf("initial setting %s must not be empty", key)
	}
	// 值非空:尝试写入,CreateAppSettingIfAbsent 只在不存在时才写
	// 返回 created = true 表示写入成功,false 表示已存在跳过了
	created, err := s.repo.CreateAppSettingIfAbsent(ctx, data.AppSetting{
		Key: key, Value: value, ValueType: ValueTypeString, Description: description,
	})
	if err != nil {
		return fmt.Errorf("seed setting %s: %w", key, err)
	}
	if created {
		// 首次写入:记录原始状态为"不存在",回滚时要把这条记录删掉
		s.originalSettings[key] = OriginalSetting{Exists: false}
		// 标记本次运行改过配置
		s.runtimeChanged = true
		logger.Info("Runtime config seeded to database on first run", zap.String("setting_key", key))
	} else {
		// 数据库里已经有了,跳过,不覆盖已有的配置
		logger.Debug("Runtime config already exists in database, skipping initial seed", zap.String("setting_key", key))
	}
	return nil
}

// seedSecret 首次启动时把 TOML 里的敏感配置加密后写进数据库,只在数据库没有的时候写.
// 参数:
//   - key: 配置键名
//   - plaintext: 明文值,如 "sk-xxxxx"
func (s *Service) seedSecret(ctx context.Context, key, plaintext string) error {
	// 明文为空 或者 没有主密钥 → 直接跳过,不写
	// 没主密钥说明加密功能用不了,不能加密就别写
	if plaintext == "" || !s.hasMasterKey() {
		return nil
	}
	// encrypt 用 AES-256-GCM 加密明文
	secret, err := s.encrypt(key, plaintext)
	if err != nil {
		return err
	}
	// CreateEncryptedSecretIfAbsent 只在不存在时才写,返回 created = true 表示写入了
	created, err := s.repo.CreateEncryptedSecretIfAbsent(ctx, secret)
	if err != nil {
		return fmt.Errorf("seed secret %s: %w", key, err)
	}
	if created {
		// 首次写入:记录原始状态为"不存在",回滚时要删掉
		s.originalSecrets[key] = OriginalSecret{Exists: false}
		// 写完后回读验证,确认密文能正常解密回来
		if err := s.verifyPersistedSecret(ctx, key, plaintext); err != nil {
			// 回读验证失败,回滚
			return s.rollbackAfterWriteFailure(ctx, key, err)
		}
		// 标记本次运行改过配置
		s.runtimeChanged = true
		logger.Info("Secret config encrypted and seeded on first run", zap.String("secret_key", key))
		// 警告:配置文件里的明文不会被自动删除,需要手动清理,免得明文一直留在配置文件里
		logger.Warn("Plaintext secrets in startup config will not be auto-removed, please remove after confirming successful startup", zap.String("secret_key", key))
	}
	return nil
}

// rememberOriginalSecret 在修改敏感配置前,先记住它当前的值.
// 这样万一后面模型测试失败要回滚,就知道改之前是什么样的.
func (s *Service) rememberOriginalSecret(ctx context.Context, key string) error {
	// 如果已经记过了就跳过,不用重复记
	// map 的 ok 语法:_, ok := map[key],ok=true 表示 key 存在
	if _, remembered := s.originalSecrets[key]; remembered {
		return nil
	}
	// 从数据库读这条敏感配置的密文
	secret, err := s.repo.GetEncryptedSecret(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		// 数据库里没有这条配置(首次写入),记录原始状态为"不存在"
		// 回滚时要把这条记录删掉
		s.originalSecrets[key] = OriginalSecret{Exists: false}
		return nil
	}
	if err != nil {
		// 不是"没找到"而是别的数据库错误,报错
		return fmt.Errorf("read original secret %s before update: %w", key, err)
	}
	// 数据库里有这条配置,记住它的原始值,回滚时把它写回去
	s.originalSecrets[key] = OriginalSecret{Value: secret, Exists: true}
	return nil
}

// rememberOriginalSetting 在修改普通配置前,先记住它当前的值.
// 和 rememberOriginalSecret 一样,只是处理的表不同.
func (s *Service) rememberOriginalSetting(ctx context.Context, key string) error {
	// 如果已经记过了就跳过
	if _, remembered := s.originalSettings[key]; remembered {
		return nil
	}
	// 从数据库读这条普通配置
	setting, err := s.repo.GetAppSetting(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		// 数据库里没有,记录原始状态为"不存在",回滚时删掉
		s.originalSettings[key] = OriginalSetting{Exists: false}
		return nil
	}
	if err != nil {
		// 别的数据库错误,报错
		return fmt.Errorf("read original setting %s before update: %w", key, err)
	}
	// 数据库里有,记住原始值,回滚时写回去
	s.originalSettings[key] = OriginalSetting{Value: setting, Exists: true}
	return nil
}

// rollbackChanges 把所有改过的配置恢复到修改前的状态.
// 普通配置和敏感配置都处理:
//   - 原来有的 → 把原始值写回去
//   - 原来没有的 → 把新增的记录删掉
func (s *Service) rollbackChanges(ctx context.Context) error {
	var rollbackErr error
	// 先处理普通配置:遍历所有记过的原始状态
	for key, original := range s.originalSettings {
		if original.Exists {
			// 原来数据库里有这条配置,把原始值写回去
			if err := s.repo.UpsertAppSetting(ctx, original.Value); err != nil {
				// 回滚也失败了,用 errors.Join 把多个错误打包在一起
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore app setting %s: %w", key, err))
			}
			continue
		}
		// 原来数据库里没有这条配置(是本次新写入的),删掉
		if err := s.repo.DeleteAppSetting(ctx, key); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("remove newly created app setting %s: %w", key, err))
		}
	}
	// 再处理敏感配置,逻辑一样
	for key, original := range s.originalSecrets {
		if original.Exists {
			// 原来有,写回原始密文
			if err := s.repo.UpsertEncryptedSecret(ctx, original.Value); err != nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore encrypted secret %s: %w", key, err))
			}
			continue
		}
		// 原来没有,删掉
		if err := s.repo.DeleteEncryptedSecret(ctx, key); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("remove newly created encrypted secret %s: %w", key, err))
		}
	}
	// 清空原始配置快照和变更标记
	s.originalSecrets = make(map[string]OriginalSecret)
	s.originalSettings = make(map[string]OriginalSetting)
	s.runtimeChanged = false
	// 返回回滚过程中收集的错误(nil 表示全部成功)
	return rollbackErr
}

// rollbackAfterWriteFailure 在加密配置写入后校验失败时,回滚到写入前状态.
// 参数:
//   - key: 哪个敏感配置出错了
//   - cause: 原始错误(校验失败的原因)
//
// 返回值:返回原始错误 cause(如果回滚也失败,两个错误一起返回)
func (s *Service) rollbackAfterWriteFailure(ctx context.Context, key string, cause error) error {
	// 调 rollbackChanges 恢复所有改过的配置
	rollbackErr := s.rollbackChanges(ctx)
	if rollbackErr != nil {
		// 回滚也失败了,把原始错误和回滚错误打包返回
		logger.Error("Secret config verification failed, rollback also failed", zap.String("secret_key", key), zap.Error(rollbackErr))
		return errors.Join(cause, rollbackErr)
	}
	// 回滚成功,告诉调用的人原始错误是什么
	logger.Warn("Secret config verification failed, restored to pre-write state", zap.String("secret_key", key))
	return cause
}

// testModel 使用最终配置发起最小非流式请求,验证地址,密钥和模型确实可用.
// 流程:先校验配置合法性 → 创建带超时的 context → 调 LLM → 检查响应非空.
func (s *Service) testModel(ctx context.Context, cfg *config.Config) error {
	// 先校验运行配置(LLM 地址和模型名)是否合法
	if err := cfg.ValidateRuntime(); err != nil {
		return fmt.Errorf("validate model configuration before test: %w", err)
	}
	// 创建一个带超时的 context,超时时间 ModelTestTimeout(20 秒)
	// context.WithTimeout 返回一个新的 context 和一个 cancel 函数
	// defer cancel() 确保函数退出时取消这个 context,释放资源
	testCtx, cancel := context.WithTimeout(ctx, ModelTestTimeout)
	defer cancel()
	// 用最终配置创建一个 LLM Provider(OpenAI 兼容接口的客户端)
	provider := llm.NewOpenAICompat(llm.Config{
		BaseURL: cfg.LLM.BaseURL,
		APIKey:  cfg.LLM.APIKey,
		Model:   cfg.LLM.Model,
	})
	// 记录开始时间,后面算耗时
	startedAt := time.Now()
	// 发一个最小的非流式请求:只发一条消息"请只回复 OK"
	// 如果地址,密钥,模型都正常,应该能收到"OK"之类的回复
	response, err := provider.Complete(testCtx, llm.ChatRequest{
		Model: cfg.LLM.Model,
		Messages: []llm.ChatMessage{
			{Role: llm.RoleUser, Content: ModelTestPrompt},
		},
	})
	if err != nil {
		// 调用失败(地址不通,密钥错误,模型不存在等)
		return fmt.Errorf("call model with persisted configuration: %w", err)
	}
	// 检查回复内容是不是空的,空回复也算失败
	// strings.TrimSpace 去掉首尾空白后判断是不是空字符串
	if strings.TrimSpace(response.Content) == "" {
		return fmt.Errorf("call model with persisted configuration: empty response")
	}
	// 测试通过,打日志记录模型名,耗时和 token 用量
	logger.Info("Model connectivity test passed after secret config write",
		zap.String("model", cfg.LLM.Model),
		zap.Int64("duration_ms", time.Since(startedAt).Milliseconds()),
		zap.Int("prompt_tokens", response.Usage.PromptTokens),
		zap.Int("completion_tokens", response.Usage.CompletionTokens),
		zap.Int("total_tokens", response.Usage.TotalTokens),
	)
	return nil
}

// verifyPersistedSecret 重新读取并解密刚写入的密文,确认数据库内容可以正常恢复.
// 参数:
//   - key: 配置键名
//   - expectedPlaintext: 期望的明文值(写入前的原始明文)
//
// 流程:从数据库读密文 → 解密 → 跟原始明文比对
func (s *Service) verifyPersistedSecret(ctx context.Context, key, expectedPlaintext string) error {
	// 从数据库重新读出刚写入的密文
	persisted, err := s.repo.GetEncryptedSecret(ctx, key)
	if err != nil {
		// 读取失败(数据库连接问题等)
		verifyErr := fmt.Errorf("read encrypted secret after write: %w", err)
		logger.Error("Secret config verification failed after write", zap.String("secret_key", key), zap.Error(verifyErr))
		return verifyErr
	}
	// 解密刚读出来的密文,拿到明文
	actualPlaintext, err := s.decrypt(persisted)
	if err != nil {
		// 解密失败(密文损坏,nonce 不对,认证失败等)
		verifyErr := fmt.Errorf("decrypt encrypted secret after write: %w", err)
		logger.Error("Secret config verification failed after write", zap.String("secret_key", key), zap.Error(verifyErr))
		return verifyErr
	}
	// 解密出来的明文必须跟写入前的明文一模一样,不然就是出问题了
	if actualPlaintext != expectedPlaintext {
		verifyErr := fmt.Errorf("decrypted value does not match source value")
		logger.Error("Secret config verification failed after write", zap.String("secret_key", key), zap.Error(verifyErr))
		return verifyErr
	}
	// 校验通过
	logger.Debug("Secret config read-back verification passed after write", zap.String("secret_key", key))
	return nil
}

// getSetting 从数据库读一项普通配置(明文存的).
// 参数:
//   - key: 配置键名
//
// 返回值:配置值,错误
func (s *Service) getSetting(ctx context.Context, key string) (string, error) {
	// 从 app_settings 表读
	setting, err := s.repo.GetAppSetting(ctx, key)
	if err != nil {
		return "", fmt.Errorf("load setting %s: %w", key, err)
	}
	return setting.Value, nil
}

// getOptionalSecret 从数据库读一项敏感配置并解密,数据库没有就返回空字符串(不报错).
// 参数:
//   - key: 配置键名
//
// 返回值:解密后的明文(数据库没有就返回空),错误
func (s *Service) getOptionalSecret(ctx context.Context, key string) (string, error) {
	// 从 encrypted_secrets 表读密文
	secret, err := s.repo.GetEncryptedSecret(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		// 数据库里没有这条配置,不是错误,返回空字符串
		// 举例:首次启动还没往数据库存 API Key 时就会走到这里
		logger.Debug("Secret config not found in database", zap.String("secret_key", key))
		return "", nil
	}
	if err != nil {
		// 别的数据库错误,报错
		return "", fmt.Errorf("load secret %s: %w", key, err)
	}
	// 解密密文,拿到明文
	plaintext, err := s.decrypt(secret)
	if err != nil {
		return "", err
	}
	logger.Debug("Secret config read and decrypted from database", zap.String("secret_key", key))
	return plaintext, nil
}

// encrypt 用 AES-256-GCM 加密明文,返回包含密文,nonce 和算法信息的结构体.
// 参数:
//   - key: 配置键名(同时作为 GCM 的附加数据,防篡改)
//   - plaintext: 要加密的明文
//
// AES-256-GCM 加密原理:
//   - AES-256 是对称加密,同一把密钥既能加密也能解密
//   - GCM 模式在加密的同时还会生成一个认证标签(authentication tag)
//     解密时会验证这个标签,如果密文被篡改过,解密就会失败
//   - nonce 是一次性随机数,每次加密都要不一样,防止相同明文加密出相同密文
func (s *Service) encrypt(key, plaintext string) (data.EncryptedSecret, error) {
	// gcm() 方法创建一个 AES-GCM 加密器(cipher.AEAD 接口)
	// AEAD = Authenticated Encryption with Associated Data,带认证的加密
	gcm, err := s.gcm()
	if err != nil {
		return data.EncryptedSecret{}, err
	}
	// 生成一个随机的 nonce,长度由 GCM 算法决定(通常 12 字节)
	// nonce = Number used ONCE,一次性随机数,同一个密钥下 nonce 不能重复
	// make([]byte, gcm.NonceSize()) 创建一个指定长度的零值字节切片
	nonce := make([]byte, gcm.NonceSize())
	// io.ReadFull 从 rand.Reader 读随机字节填满 nonce
	// crypto/rand.Reader 是密码学安全的随机数生成器(比 math/rand 更安全)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return data.EncryptedSecret{}, fmt.Errorf("generate nonce for %s: %w", key, err)
	}
	// gcm.Seal 加密明文,返回"密文+认证标签"拼在一起的字节切片
	// 参数说明:
	//   - nil: 目标切片,nil 表示新建一个
	//   - nonce: 一次性随机数
	//   - []byte(plaintext): 要加密的明文,转成字节切片
	//   - []byte(key): 附加数据(additionalData),这里用配置键名
	//     解密时也要传同样的附加数据,如果不一致解密就会失败
	//     这样就算有人把 llm.api_key 的密文搬到 auth.owner_token 字段,解密也会失败
	ciphertext := gcm.Seal(nil, nonce, []byte(plaintext), []byte(key))
	// 打包成 EncryptedSecret 结构体返回
	return data.EncryptedSecret{
		Key:        key,               // 配置键名
		Ciphertext: ciphertext,        // 加密后的密文(含认证标签)
		Nonce:      nonce,             // 一次性随机数,解密时要用
		Algorithm:  AlgorithmAESGCM,   // 加密算法名,将来换算法时能区分
		KeyVersion: CurrentKeyVersion, // 密钥版本,将来换密钥时能区分
	}, nil
}

// decrypt 解密 EncryptedSecret,返回明文.
// 参数:
//   - secret: 从数据库读出的加密结构体(包含密文,nonce,算法名等)
//
// 返回值:解密后的明文,错误
//
// 解密时会验证认证标签,密文被篡改过就解不开.
func (s *Service) decrypt(secret data.EncryptedSecret) (string, error) {
	// 检查算法是不是 AES-256-GCM,不是的话拒绝解密
	// 举例:将来如果加了 AES-128-GCM,这里能挡住不兼容的旧密文
	if secret.Algorithm != AlgorithmAESGCM {
		return "", fmt.Errorf("unsupported secret algorithm %q", secret.Algorithm)
	}
	// 检查密钥版本,不是当前版本就拒绝
	// 将来换主密钥后,旧版本密文需要迁移到新版本
	if secret.KeyVersion != CurrentKeyVersion {
		return "", fmt.Errorf("unsupported secret key version %d", secret.KeyVersion)
	}
	// 创建 AES-GCM 解密器
	gcm, err := s.gcm()
	if err != nil {
		return "", err
	}
	// gcm.Open 解密密文,验证认证标签
	// 参数说明:
	//   - nil: 目标切片,nil 表示新建一个
	//   - secret.Nonce: 加密时用的 nonce,必须跟加密时的一致
	//   - secret.Ciphertext: 要解密的密文
	//   - []byte(secret.Key): 附加数据,必须跟加密时传的一致(加密时传的是 key 名)
	// 如果密文被篡改,nonce 不对,附加数据不一致,Open 都会返回错误
	plaintext, err := gcm.Open(nil, secret.Nonce, secret.Ciphertext, []byte(secret.Key))
	if err != nil {
		// 认证失败:密文被篡改,nonce 不对,或附加数据不匹配
		return "", fmt.Errorf("decrypt secret %s: authentication failed", secret.Key)
	}
	// 把字节切片转成字符串返回
	return string(plaintext), nil
}

// gcm 创建一个 AES-256-GCM 加密器.
// 返回 cipher.AEAD 接口,encrypt 和 decrypt 都用它.
//
// 原理:主密钥 → AES 分组加密器 → GCM 模式包装
func (s *Service) gcm() (cipher.AEAD, error) {
	// 没有主密钥就不能加解密
	if !s.hasMasterKey() {
		return nil, fmt.Errorf("local master key is required to read or write encrypted database settings")
	}
	// aes.NewCipher 用 32 字节主密钥创建一个 AES 分组加密器(block cipher)
	// AES 是分组加密算法,把数据分成 16 字节一组来加密
	// 密钥长度决定算法:16 字节=AES-128,24 字节=AES-192,32 字节=AES-256
	block, err := aes.NewCipher(s.masterKey)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	// cipher.NewGCM 把 AES 分组加密器包装成 GCM 模式
	// GCM = Galois/Counter Mode,能同时加密和认证
	// 返回的 cipher.AEAD 接口有 Seal(加密)和 Open(解密)两个方法
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create AES-GCM: %w", err)
	}
	return gcm, nil
}

// hasMasterKey 检查主密钥是否存在且长度正确.
// 返回 true 表示有可用的主密钥.
func (s *Service) hasMasterKey() bool {
	// 主密钥长度必须是 32 字节(MasterKeySize),不等于就是没有或不对
	return len(s.masterKey) == MasterKeySize
}

// loadOrCreateMasterKey 读取本地主密钥;首次启动时生成并以仅当前用户可读写的权限保存.
// 参数:
//   - path: 主密钥文件路径,如 "data/swallow.db.key"
//
// 返回值:
//   - string: base64 编码的主密钥
//   - bool: 是否是首次创建(true = 本次生成的,false = 读取已有的)
//   - error: 错误
//
// 流程:
//  1. 先尝试读文件,读到就返回
//  2. 文件不存在 → 生成 32 字节随机密钥 → base64 编码 → 写入文件(权限 0600,只有当前用户能读写)
//  3. 写入时用 O_EXCL 防止覆盖,如果文件已存在(并发创建)就读取已有的
func loadOrCreateMasterKey(path string) (string, bool, error) {
	// 第一步:尝试读文件
	// os.ReadFile 读取整个文件内容,返回字节切片和错误
	content, err := os.ReadFile(path)
	if err == nil {
		// 读取成功,文件已存在
		// strings.TrimSpace 去掉首尾的换行和空格(文件里可能有尾部换行符)
		// string(content) 把字节切片转成字符串
		return strings.TrimSpace(string(content)), false, nil
	}
	// 读取失败了,判断原因
	if !errors.Is(err, os.ErrNotExist) {
		// 不是"文件不存在"而是别的错误(比如没权限),报错
		return "", false, fmt.Errorf("read local master key %s: %w", path, err)
	}
	// 到这里说明文件不存在,需要生成一个新的主密钥

	// 第二步:生成 32 字节的随机主密钥
	// make([]byte, MasterKeySize) 创建一个 32 字节的零值切片
	key := make([]byte, MasterKeySize)
	// io.ReadFull 从 crypto/rand.Reader 读 32 个随机字节填满 key
	// crypto/rand.Reader 是密码学安全的随机数生成器,比 math/rand 更安全
	// 为什么不用 math/rand?因为 math/rand 的随机性不够,可预测,不适合生成密钥
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return "", false, fmt.Errorf("generate local master key: %w", err)
	}
	// 第三步:base64 编码
	// base64.StdEncoding.EncodeToString 把 32 字节转成 base64 字符串
	// 举例:32 字节的二进制 → "dGVzdA..." 这种可打印字符
	// 用 base64 是因为密钥文件要存成文本,二进制内容不适合直接写进文件
	encodedKey := base64.StdEncoding.EncodeToString(key)
	// 第四步:创建目录(如果不存在)
	// filepath.Dir(path) 取出路径里的目录部分
	// 举例:path = "data/swallow.db.key" → dir = "data"
	// os.MkdirAll 递归创建目录,0700 表示只有当前用户有读写执行权限
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", false, fmt.Errorf("create master key directory: %w", err)
	}
	// 第五步:创建文件并写入
	// os.OpenFile 打开或创建文件,参数说明:
	//   - O_WRONLY: 只写模式
	//   - O_CREATE: 文件不存在就创建
	//   - O_EXCL: 必须是新建的,如果文件已存在就报错(防止覆盖已有的密钥)
	//   - 0600: 文件权限,只有当前用户能读写(其他人不能访问)
	// 为什么要 O_EXCL?防止并发启动时两个进程都试图创建密钥文件,后一个覆盖前一个
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		// 文件已存在(可能是另一个进程刚创建的),读取已有的密钥
		// 这是一个并发保护:如果两个进程同时启动,第一个创建了文件,第二个走到这里
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return "", false, fmt.Errorf("read concurrently created master key %s: %w", path, readErr)
		}
		// 返回已有的密钥,created = false(不是本次创建的)
		return strings.TrimSpace(string(content)), false, nil
	}
	if err != nil {
		// 别的错误(比如没权限创建文件)
		return "", false, fmt.Errorf("create local master key %s: %w", path, err)
	}
	// 第六步:把 base64 编码的密钥写进文件
	if _, err := file.WriteString(encodedKey); err != nil {
		// 写入失败,先关闭文件再返回错误
		// _ = file.Close() 忽略关闭时的错误,因为写入已经失败了
		_ = file.Close()
		return "", false, fmt.Errorf("write local master key %s: %w", path, err)
	}
	// 关闭文件,确保数据写入磁盘
	if err := file.Close(); err != nil {
		return "", false, fmt.Errorf("close local master key %s: %w", path, err)
	}
	// 返回 base64 编码的密钥,created = true(本次首次创建的)
	return encodedKey, true, nil
}
