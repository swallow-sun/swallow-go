// service.go 放运行配置服务：管理数据库运行配置及敏感配置的加解密。
//
// 做的事情：
//  1. 加载主密钥：从数据库旁的 .key 文件读取 AES-256 主密钥，首次运行时自动生成。
//  2. LoadInto：把数据库里的配置加载到 config.Config，覆盖 TOML 里的初始值。
//  3. SetSetting/SetSecret：运行时修改普通配置和加密密钥，修改后做模型连通性测试，失败自动回滚。
//  4. encrypt/decrypt：用 AES-256-GCM 加解密敏感配置。
//  5. seedSetting/seedSecret：首次启动时写入默认配置值（仅在不存在的时写入）。
//
// 安全设计：修改配置后用模型做连通性测试，测试失败自动回滚到修改前的值。
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

// New 创建运行配置服务。
// 主密钥固定从数据库旁的本地 key 文件读取；首次运行时自动生成。
func New(repo data.Repository, databasePath string) (*Service, error) {
	encodedKey, created, err := loadOrCreateMasterKey(databasePath + MasterKeyFileSuffix)
	if err != nil {
		return nil, err
	}
	if created {
		logger.Info("已生成本地主密钥文件", zap.String("key_file", databasePath+MasterKeyFileSuffix))
	} else {
		logger.Debug("已读取本地主密钥文件", zap.String("key_file", databasePath+MasterKeyFileSuffix))
	}
	masterKey, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedKey))
	if err != nil {
		return nil, fmt.Errorf("decode local master key as base64: %w", err)
	}
	if len(masterKey) != MasterKeySize {
		return nil, fmt.Errorf("local master key must decode to %d bytes", MasterKeySize)
	}
	return &Service{
		repo: repo, masterKey: masterKey,
		originalSecrets:  make(map[string]OriginalSecret),
		originalSettings: make(map[string]OriginalSetting),
	}, nil
}

// LoadInto 首次运行时保存启动默认值，之后用数据库配置覆盖运行配置。
func (s *Service) LoadInto(ctx context.Context, cfg *config.Config) (loadErr error) {
	defer func() {
		if loadErr == nil || !s.runtimeChanged {
			return
		}
		rollbackErr := s.rollbackChanges(ctx)
		if rollbackErr != nil {
			logger.Error("加载运行配置失败且回滚失败", zap.Error(rollbackErr))
			loadErr = errors.Join(loadErr, rollbackErr)
			return
		}
		logger.Warn("加载运行配置失败，已恢复写入前状态")
	}()
	if err := s.seedSetting(ctx, SettingLLMBaseURL, cfg.LLM.BaseURL, "LLM 服务基础地址"); err != nil {
		return err
	}
	if err := s.seedSetting(ctx, SettingLLMModel, cfg.LLM.Model, "默认 LLM 模型名称"); err != nil {
		return err
	}
	if err := s.seedSecret(ctx, SecretLLMAPIKey, cfg.LLM.APIKey); err != nil {
		return err
	}
	if err := s.seedSecret(ctx, SecretOwnerToken, cfg.Auth.OwnerToken); err != nil {
		return err
	}
	baseURL, err := s.getSetting(ctx, SettingLLMBaseURL)
	if err != nil {
		return err
	}
	model, err := s.getSetting(ctx, SettingLLMModel)
	if err != nil {
		return err
	}
	apiKey, err := s.getOptionalSecret(ctx, SecretLLMAPIKey)
	if err != nil {
		return err
	}
	ownerToken, err := s.getOptionalSecret(ctx, SecretOwnerToken)
	if err != nil {
		return err
	}

	cfg.LLM.BaseURL = baseURL
	cfg.LLM.Model = model
	// 数据库没有对应密文时保留配置文件或环境变量中的值。
	if apiKey != "" {
		cfg.LLM.APIKey = apiKey
	}
	if ownerToken != "" {
		cfg.Auth.OwnerToken = ownerToken
	}
	if s.runtimeChanged {
		var testErr error
		if cfg.LLM.APIKey == "" {
			testErr = fmt.Errorf("LLM API Key is empty after configuration write")
		} else {
			testErr = s.testModel(ctx, cfg)
		}
		if testErr != nil {
			logger.Error("运行配置写入后模型调用测试失败", zap.Error(testErr))
			if rollbackErr := s.rollbackChanges(ctx); rollbackErr != nil {
				logger.Error("模型测试失败后回滚运行配置失败", zap.Error(rollbackErr))
				return fmt.Errorf("model test failed: %v; rollback failed: %w", testErr, rollbackErr)
			}
			logger.Warn("模型测试失败，已恢复写入前的运行配置")
			return testErr
		}
		s.originalSecrets = make(map[string]OriginalSecret)
		s.originalSettings = make(map[string]OriginalSetting)
		s.runtimeChanged = false
	}
	return nil
}

// SetSetting 更新一项普通配置，给后续管理接口调。
func (s *Service) SetSetting(ctx context.Context, key, value, description string) error {
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("setting key must not be empty")
	}
	if err := s.rememberOriginalSetting(ctx, key); err != nil {
		return err
	}
	if err := s.repo.UpsertAppSetting(ctx, data.AppSetting{
		Key: key, Value: value, ValueType: ValueTypeString, Description: description,
	}); err != nil {
		return err
	}
	logger.Info("运行配置已更新", zap.String("setting_key", key))
	s.runtimeChanged = true
	return nil
}

// SetSecret 加密后更新敏感配置，明文永远不会传给 Repository。
func (s *Service) SetSecret(ctx context.Context, key, plaintext string) error {
	if err := s.rememberOriginalSecret(ctx, key); err != nil {
		return err
	}
	secret, err := s.encrypt(key, plaintext)
	if err != nil {
		return err
	}
	if err := s.repo.UpsertEncryptedSecret(ctx, secret); err != nil {
		return err
	}
	if err := s.verifyPersistedSecret(ctx, key, plaintext); err != nil {
		return s.rollbackAfterWriteFailure(ctx, key, err)
	}
	s.runtimeChanged = true
	return nil
}

func (s *Service) seedSetting(ctx context.Context, key, value, description string) error {
	if strings.TrimSpace(value) == "" {
		if _, err := s.repo.GetAppSetting(ctx, key); err == nil {
			return nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("check existing setting %s: %w", key, err)
		}
		return fmt.Errorf("initial setting %s must not be empty", key)
	}
	created, err := s.repo.CreateAppSettingIfAbsent(ctx, data.AppSetting{
		Key: key, Value: value, ValueType: ValueTypeString, Description: description,
	})
	if err != nil {
		return fmt.Errorf("seed setting %s: %w", key, err)
	}
	if created {
		s.originalSettings[key] = OriginalSetting{Exists: false}
		s.runtimeChanged = true
		logger.Info("运行配置已完成首次入库", zap.String("setting_key", key))
	} else {
		logger.Debug("数据库运行配置已存在，跳过首次写入", zap.String("setting_key", key))
	}
	return nil
}

func (s *Service) seedSecret(ctx context.Context, key, plaintext string) error {
	if plaintext == "" || !s.hasMasterKey() {
		return nil
	}
	secret, err := s.encrypt(key, plaintext)
	if err != nil {
		return err
	}
	created, err := s.repo.CreateEncryptedSecretIfAbsent(ctx, secret)
	if err != nil {
		return fmt.Errorf("seed secret %s: %w", key, err)
	}
	if created {
		s.originalSecrets[key] = OriginalSecret{Exists: false}
		if err := s.verifyPersistedSecret(ctx, key, plaintext); err != nil {
			return s.rollbackAfterWriteFailure(ctx, key, err)
		}
		s.runtimeChanged = true
		logger.Info("敏感配置已完成首次加密入库", zap.String("secret_key", key))
		logger.Warn("启动配置中的敏感明文不会自动删除，请在确认启动成功后移除", zap.String("secret_key", key))
	}
	return nil
}

func (s *Service) rememberOriginalSecret(ctx context.Context, key string) error {
	if _, remembered := s.originalSecrets[key]; remembered {
		return nil
	}
	secret, err := s.repo.GetEncryptedSecret(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		s.originalSecrets[key] = OriginalSecret{Exists: false}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read original secret %s before update: %w", key, err)
	}
	s.originalSecrets[key] = OriginalSecret{Value: secret, Exists: true}
	return nil
}

func (s *Service) rememberOriginalSetting(ctx context.Context, key string) error {
	if _, remembered := s.originalSettings[key]; remembered {
		return nil
	}
	setting, err := s.repo.GetAppSetting(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		s.originalSettings[key] = OriginalSetting{Exists: false}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read original setting %s before update: %w", key, err)
	}
	s.originalSettings[key] = OriginalSetting{Value: setting, Exists: true}
	return nil
}

func (s *Service) rollbackChanges(ctx context.Context) error {
	var rollbackErr error
	for key, original := range s.originalSettings {
		if original.Exists {
			if err := s.repo.UpsertAppSetting(ctx, original.Value); err != nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore app setting %s: %w", key, err))
			}
			continue
		}
		if err := s.repo.DeleteAppSetting(ctx, key); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("remove newly created app setting %s: %w", key, err))
		}
	}
	for key, original := range s.originalSecrets {
		if original.Exists {
			if err := s.repo.UpsertEncryptedSecret(ctx, original.Value); err != nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore encrypted secret %s: %w", key, err))
			}
			continue
		}
		if err := s.repo.DeleteEncryptedSecret(ctx, key); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("remove newly created encrypted secret %s: %w", key, err))
		}
	}
	s.originalSecrets = make(map[string]OriginalSecret)
	s.originalSettings = make(map[string]OriginalSetting)
	s.runtimeChanged = false
	return rollbackErr
}

func (s *Service) rollbackAfterWriteFailure(ctx context.Context, key string, cause error) error {
	rollbackErr := s.rollbackChanges(ctx)
	if rollbackErr != nil {
		logger.Error("加密配置校验失败且回滚失败", zap.String("secret_key", key), zap.Error(rollbackErr))
		return errors.Join(cause, rollbackErr)
	}
	logger.Warn("加密配置校验失败，已恢复写入前状态", zap.String("secret_key", key))
	return cause
}

// testModel 使用最终配置发起最小非流式请求，验证地址、密钥和模型确实可用。
func (s *Service) testModel(ctx context.Context, cfg *config.Config) error {
	if err := cfg.ValidateRuntime(); err != nil {
		return fmt.Errorf("validate model configuration before test: %w", err)
	}
	testCtx, cancel := context.WithTimeout(ctx, ModelTestTimeout)
	defer cancel()
	provider := llm.NewOpenAICompat(llm.Config{
		BaseURL: cfg.LLM.BaseURL,
		APIKey:  cfg.LLM.APIKey,
		Model:   cfg.LLM.Model,
	})
	startedAt := time.Now()
	response, err := provider.Complete(testCtx, llm.ChatRequest{
		Model: cfg.LLM.Model,
		Messages: []llm.ChatMessage{
			{Role: llm.RoleUser, Content: ModelTestPrompt},
		},
	})
	if err != nil {
		return fmt.Errorf("call model with persisted configuration: %w", err)
	}
	if strings.TrimSpace(response.Content) == "" {
		return fmt.Errorf("call model with persisted configuration: empty response")
	}
	logger.Info("加密配置写入后模型调用测试通过",
		zap.String("model", cfg.LLM.Model),
		zap.Int64("duration_ms", time.Since(startedAt).Milliseconds()),
		zap.Int("prompt_tokens", response.Usage.PromptTokens),
		zap.Int("completion_tokens", response.Usage.CompletionTokens),
		zap.Int("total_tokens", response.Usage.TotalTokens),
	)
	return nil
}

// verifyPersistedSecret 重新读取并解密刚写入的密文，确认数据库内容可以正常恢复。
func (s *Service) verifyPersistedSecret(ctx context.Context, key, expectedPlaintext string) error {
	persisted, err := s.repo.GetEncryptedSecret(ctx, key)
	if err != nil {
		verifyErr := fmt.Errorf("read encrypted secret after write: %w", err)
		logger.Error("加密配置写入后校验失败", zap.String("secret_key", key), zap.Error(verifyErr))
		return verifyErr
	}
	actualPlaintext, err := s.decrypt(persisted)
	if err != nil {
		verifyErr := fmt.Errorf("decrypt encrypted secret after write: %w", err)
		logger.Error("加密配置写入后校验失败", zap.String("secret_key", key), zap.Error(verifyErr))
		return verifyErr
	}
	if actualPlaintext != expectedPlaintext {
		verifyErr := fmt.Errorf("decrypted value does not match source value")
		logger.Error("加密配置写入后校验失败", zap.String("secret_key", key), zap.Error(verifyErr))
		return verifyErr
	}
	logger.Debug("加密配置写入后回读校验通过", zap.String("secret_key", key))
	return nil
}

func (s *Service) getSetting(ctx context.Context, key string) (string, error) {
	setting, err := s.repo.GetAppSetting(ctx, key)
	if err != nil {
		return "", fmt.Errorf("load setting %s: %w", key, err)
	}
	return setting.Value, nil
}

func (s *Service) getOptionalSecret(ctx context.Context, key string) (string, error) {
	secret, err := s.repo.GetEncryptedSecret(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		logger.Debug("数据库中没有敏感配置", zap.String("secret_key", key))
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("load secret %s: %w", key, err)
	}
	plaintext, err := s.decrypt(secret)
	if err != nil {
		return "", err
	}
	logger.Debug("已从数据库读取并解密敏感配置", zap.String("secret_key", key))
	return plaintext, nil
}

func (s *Service) encrypt(key, plaintext string) (data.EncryptedSecret, error) {
	gcm, err := s.gcm()
	if err != nil {
		return data.EncryptedSecret{}, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return data.EncryptedSecret{}, fmt.Errorf("generate nonce for %s: %w", key, err)
	}
	ciphertext := gcm.Seal(nil, nonce, []byte(plaintext), []byte(key))
	return data.EncryptedSecret{
		Key: key, Ciphertext: ciphertext, Nonce: nonce,
		Algorithm: AlgorithmAESGCM, KeyVersion: CurrentKeyVersion,
	}, nil
}

func (s *Service) decrypt(secret data.EncryptedSecret) (string, error) {
	if secret.Algorithm != AlgorithmAESGCM {
		return "", fmt.Errorf("unsupported secret algorithm %q", secret.Algorithm)
	}
	if secret.KeyVersion != CurrentKeyVersion {
		return "", fmt.Errorf("unsupported secret key version %d", secret.KeyVersion)
	}
	gcm, err := s.gcm()
	if err != nil {
		return "", err
	}
	plaintext, err := gcm.Open(nil, secret.Nonce, secret.Ciphertext, []byte(secret.Key))
	if err != nil {
		return "", fmt.Errorf("decrypt secret %s: authentication failed", secret.Key)
	}
	return string(plaintext), nil
}

func (s *Service) gcm() (cipher.AEAD, error) {
	if !s.hasMasterKey() {
		return nil, fmt.Errorf("local master key is required to read or write encrypted database settings")
	}
	block, err := aes.NewCipher(s.masterKey)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create AES-GCM: %w", err)
	}
	return gcm, nil
}

func (s *Service) hasMasterKey() bool {
	return len(s.masterKey) == MasterKeySize
}

// loadOrCreateMasterKey 读取本地主密钥；首次启动时生成并以仅当前用户可读写的权限保存。
func loadOrCreateMasterKey(path string) (string, bool, error) {
	content, err := os.ReadFile(path)
	if err == nil {
		return strings.TrimSpace(string(content)), false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", false, fmt.Errorf("read local master key %s: %w", path, err)
	}

	key := make([]byte, MasterKeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return "", false, fmt.Errorf("generate local master key: %w", err)
	}
	encodedKey := base64.StdEncoding.EncodeToString(key)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", false, fmt.Errorf("create master key directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return "", false, fmt.Errorf("read concurrently created master key %s: %w", path, readErr)
		}
		return strings.TrimSpace(string(content)), false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("create local master key %s: %w", path, err)
	}
	if _, err := file.WriteString(encodedKey); err != nil {
		_ = file.Close()
		return "", false, fmt.Errorf("write local master key %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return "", false, fmt.Errorf("close local master key %s: %w", path, err)
	}
	return encodedKey, true, nil
}
