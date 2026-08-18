// settings_repo.go 放运行配置和加密密钥的 SQLite 数据访问方法。
//
// 做的事情：
//  1. 实现 AppSetting 的 CRUD：GetAppSetting、CreateAppSettingIfAbsent、UpsertAppSetting、DeleteAppSetting。
//  2. 实现 EncryptedSecret 的 CRUD：GetEncryptedSecret、CreateEncryptedSecretIfAbsent、UpsertEncryptedSecret、DeleteEncryptedSecret。
//  3. 提供 ORM 模型和业务实体之间的转换函数：appSettingToORM/FromORM、encryptedSecretToORM/FromORM。
package data

import (
	"context"
	"fmt"

	"gorm.io/gorm/clause"
)

// GetAppSetting 按稳定键读取一项普通配置。
func (r *sqliteRepo) GetAppSetting(ctx context.Context, key string) (AppSetting, error) {
	var model ormAppSetting
	if err := r.db.WithContext(ctx).First(&model, "setting_key = ?", key).Error; err != nil {
		return AppSetting{}, fmt.Errorf("get app setting %q: %w", key, repositoryError(err))
	}
	return appSettingFromORM(model), nil
}

// CreateAppSettingIfAbsent 只在配置尚不存在时写入启动默认值。
func (r *sqliteRepo) CreateAppSettingIfAbsent(ctx context.Context, setting AppSetting) (bool, error) {
	model := appSettingToORM(setting)
	result := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&model)
	if result.Error != nil {
		return false, fmt.Errorf("create app setting %q: %w", setting.Key, result.Error)
	}
	return result.RowsAffected == 1, nil
}

// UpsertAppSetting 创建或更新一项普通配置。
func (r *sqliteRepo) UpsertAppSetting(ctx context.Context, setting AppSetting) error {
	model := appSettingToORM(setting)
	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "setting_key"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"setting_value", "value_type", "description", "updated_at",
		}),
	}).Create(&model).Error; err != nil {
		return fmt.Errorf("upsert app setting %q: %w", setting.Key, err)
	}
	return nil
}

// DeleteAppSetting 删除指定普通配置，仅用于验证失败时恢复旧状态。
func (r *sqliteRepo) DeleteAppSetting(ctx context.Context, key string) error {
	if err := r.db.WithContext(ctx).Where("setting_key = ?", key).Delete(&ormAppSetting{}).Error; err != nil {
		return fmt.Errorf("delete app setting %q: %w", key, err)
	}
	return nil
}

// GetEncryptedSecret 按稳定键读取一项密文记录。
func (r *sqliteRepo) GetEncryptedSecret(ctx context.Context, key string) (EncryptedSecret, error) {
	var model ormEncryptedSecret
	if err := r.db.WithContext(ctx).First(&model, "secret_key = ?", key).Error; err != nil {
		return EncryptedSecret{}, fmt.Errorf("get encrypted secret %q: %w", key, repositoryError(err))
	}
	return encryptedSecretFromORM(model), nil
}

// CreateEncryptedSecretIfAbsent 只在密钥尚不存在时保存首次加密结果。
func (r *sqliteRepo) CreateEncryptedSecretIfAbsent(ctx context.Context, secret EncryptedSecret) (bool, error) {
	model := encryptedSecretToORM(secret)
	result := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&model)
	if result.Error != nil {
		return false, fmt.Errorf("create encrypted secret %q: %w", secret.Key, result.Error)
	}
	return result.RowsAffected == 1, nil
}

// UpsertEncryptedSecret 创建或更新一项密文；调用的人必须先完成加密。
func (r *sqliteRepo) UpsertEncryptedSecret(ctx context.Context, secret EncryptedSecret) error {
	model := encryptedSecretToORM(secret)
	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "secret_key"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"ciphertext", "nonce", "algorithm", "key_version", "updated_at",
		}),
	}).Create(&model).Error; err != nil {
		return fmt.Errorf("upsert encrypted secret %q: %w", secret.Key, err)
	}
	return nil
}

// DeleteEncryptedSecret 删除指定密文，仅用于写入后验证失败时恢复旧状态。
func (r *sqliteRepo) DeleteEncryptedSecret(ctx context.Context, key string) error {
	if err := r.db.WithContext(ctx).Where("secret_key = ?", key).Delete(&ormEncryptedSecret{}).Error; err != nil {
		return fmt.Errorf("delete encrypted secret %q: %w", key, err)
	}
	return nil
}

func appSettingToORM(setting AppSetting) ormAppSetting {
	return ormAppSetting{
		Key: setting.Key, Value: setting.Value, ValueType: setting.ValueType,
		Description: setting.Description, UpdatedAt: setting.UpdatedAt,
	}
}

func appSettingFromORM(model ormAppSetting) AppSetting {
	return AppSetting{
		Key: model.Key, Value: model.Value, ValueType: model.ValueType,
		Description: model.Description, UpdatedAt: model.UpdatedAt,
	}
}

func encryptedSecretToORM(secret EncryptedSecret) ormEncryptedSecret {
	return ormEncryptedSecret{
		Key: secret.Key, Ciphertext: secret.Ciphertext, Nonce: secret.Nonce,
		Algorithm: secret.Algorithm, KeyVersion: secret.KeyVersion, UpdatedAt: secret.UpdatedAt,
	}
}

func encryptedSecretFromORM(model ormEncryptedSecret) EncryptedSecret {
	return EncryptedSecret{
		Key: model.Key, Ciphertext: model.Ciphertext, Nonce: model.Nonce,
		Algorithm: model.Algorithm, KeyVersion: model.KeyVersion, UpdatedAt: model.UpdatedAt,
	}
}
