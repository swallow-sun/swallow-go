// settings_repo.go 放运行配置和加密密钥的 SQLite 数据访问方法.
//
// 做的事情:
//  1. 实现 AppSetting 的 CRUD:GetAppSetting,CreateAppSettingIfAbsent,UpsertAppSetting,DeleteAppSetting.
//  2. 实现 EncryptedSecret 的 CRUD:GetEncryptedSecret,CreateEncryptedSecretIfAbsent,UpsertEncryptedSecret,DeleteEncryptedSecret.
//  3. 提供 ORM 模型和业务实体之间的转换函数:appSettingToORM/FromORM,encryptedSecretToORM/FromORM.
package data

import (
	"context"
	"fmt"

	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm/clause"
)

// GetAppSetting 按稳定键读取一项普通配置.
// 参数 key 是配置项的键名.查不到返回 sql.ErrNoRows.
func (r *sqliteRepo) GetAppSetting(ctx context.Context, key string) (AppSetting, error) {
	// 空的 ORM 模型变量,准备接收查询结果
	var model ormAppSetting

	// .Select(appSettingColumns) 只查需要的列,不用 SELECT *
	// .First(&model, "setting_key = ?", key) 按主键查一条
	if err := r.db.WithContext(ctx).Select(appSettingColumns).First(&model, "setting_key = ?", key).Error; err != nil {
		return AppSetting{}, fmt.Errorf("get app setting %q: %w", key, repositoryError(err))
	}
	// 转成业务对象返回
	return appSettingFromORM(model), nil
}

// CreateAppSettingIfAbsent 只在配置尚不存在时写入启动默认值.
// 返回 bool:true 表示新建成功,false 表示已存在(跳过).
func (r *sqliteRepo) CreateAppSettingIfAbsent(ctx context.Context, setting AppSetting) (bool, error) {
	// 先把业务对象转成 ORM 模型
	model := appSettingToORM(setting)

	// .Clauses(clause.OnConflict{DoNothing: true}) 加冲突处理:主键冲突时什么都不做
	//   相当于 SQL 的 INSERT ... ON CONFLICT DO NOTHING
	// .Create(&model) 执行 INSERT
	// 效果:配置已存在就不插,配置不存在才插
	result := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&model)
	if result.Error != nil {
		logger.Error("app_settings insert failed",
			zap.String("key", setting.Key),
			zap.Error(result.Error),
		)
		return false, fmt.Errorf("create app setting %q: %w", setting.Key, result.Error)
	}

	// RowsAffected == 1 说明插成功了(新建),== 0 说明冲突了(已存在)
	logger.Debug("app_settings insert completed",
		zap.String("setting_key", model.Key),
		zap.Bool("created", result.RowsAffected == 1),
	)
	return result.RowsAffected == 1, nil
}

// UpsertAppSetting 创建或更新一项普通配置.
// 配置不存在就新建,已存在就更新指定的字段.
func (r *sqliteRepo) UpsertAppSetting(ctx context.Context, setting AppSetting) error {
	// 业务对象转 ORM 模型
	model := appSettingToORM(setting)

	// .Clauses(clause.OnConflict{...}) 加冲突处理:
	//   Columns: 指定唯一约束列(setting_key 主键)
	//   DoUpdates: clause.AssignmentColumns([...]) 冲突时更新这些列
	//     会把 setting_value,value_type,description,updated_at 这几个字段用新值覆盖
	// .Create(&model) 执行 INSERT,冲突时走 DoUpdates 分支
	// 整体效果:INSERT ... ON CONFLICT (setting_key) DO UPDATE SET ...
	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "setting_key"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"setting_value", "value_type", "description", "updated_at",
		}),
	}).Create(&model).Error; err != nil {
		logger.Error("app_settings upsert failed",
			zap.String("key", setting.Key),
			zap.Error(err),
		)
		return fmt.Errorf("upsert app setting %q: %w", setting.Key, err)
	}

	logger.Debug("app_settings upsert succeeded",
		zap.String("setting_key", model.Key),
	)
	return nil
}

// DeleteAppSetting 删除指定普通配置,仅用于验证失败时恢复旧状态.
// 参数 key 是配置项的键名.
func (r *sqliteRepo) DeleteAppSetting(ctx context.Context, key string) error {
	// .Where("setting_key = ?", key) 按键名找
	// .Delete(&ormAppSetting{}) 删除匹配的记录
	//   相当于 DELETE FROM app_settings WHERE setting_key = ?
	if err := r.db.WithContext(ctx).Where("setting_key = ?", key).Delete(&ormAppSetting{}).Error; err != nil {
		logger.Error("app_settings delete failed",
			zap.String("key", key),
			zap.Error(err),
		)
		return fmt.Errorf("delete app setting %q: %w", key, err)
	}

	return nil
}

// GetEncryptedSecret 按稳定键读取一项密文记录.
// 参数 key 是密钥的键名.查不到返回 sql.ErrNoRows.
func (r *sqliteRepo) GetEncryptedSecret(ctx context.Context, key string) (EncryptedSecret, error) {
	// 空的 ORM 模型变量
	var model ormEncryptedSecret

	// .Select(encryptedSecretColumns) 只查需要的列,不用 SELECT *
	// .First(&model, "secret_key = ?", key) 按主键查一条
	if err := r.db.WithContext(ctx).Select(encryptedSecretColumns).First(&model, "secret_key = ?", key).Error; err != nil {
		return EncryptedSecret{}, fmt.Errorf("get encrypted secret %q: %w", key, repositoryError(err))
	}
	// 转成业务对象返回
	return encryptedSecretFromORM(model), nil
}

// CreateEncryptedSecretIfAbsent 只在密钥尚不存在时保存首次加密结果.
// 返回 bool:true 表示新建成功,false 表示已存在(跳过).
// 这样可以保证首次加密的结果不会被覆盖,除非显式调 Upsert.
func (r *sqliteRepo) CreateEncryptedSecretIfAbsent(ctx context.Context, secret EncryptedSecret) (bool, error) {
	// 业务对象转 ORM 模型
	model := encryptedSecretToORM(secret)

	// .Clauses(clause.OnConflict{DoNothing: true}) 冲突时什么都不做
	// .Create(&model) 执行 INSERT
	result := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&model)
	if result.Error != nil {
		logger.Error("encrypted_secrets insert failed",
			zap.String("key", secret.Key),
			zap.String("algorithm", secret.Algorithm),
			zap.Int("key_version", secret.KeyVersion),
			zap.Error(result.Error),
		)
		return false, fmt.Errorf("create encrypted secret %q: %w", secret.Key, result.Error)
	}

	// RowsAffected == 1 新建成功,== 0 已存在
	return result.RowsAffected == 1, nil
}

// UpsertEncryptedSecret 创建或更新一项密文;调用的人必须先完成加密.
// 参数 secret 是加密后的密文,调用前必须把 Ciphertext,Nonce 等字段填好.
func (r *sqliteRepo) UpsertEncryptedSecret(ctx context.Context, secret EncryptedSecret) error {
	// 业务对象转 ORM 模型
	model := encryptedSecretToORM(secret)

	// .Clauses(clause.OnConflict{...}) 冲突处理:
	//   Columns: 唯一约束列(secret_key 主键)
	//   DoUpdates: 冲突时更新 ciphertext,nonce,algorithm,key_version,updated_at
	// .Create(&model) 执行 INSERT,冲突走 UPDATE 分支
	// 效果:密钥不存在就新建,已存在就更新密文内容
	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "secret_key"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"ciphertext", "nonce", "algorithm", "key_version", "updated_at",
		}),
	}).Create(&model).Error; err != nil {
		logger.Error("encrypted_secrets upsert failed",
			zap.String("key", secret.Key),
			zap.String("algorithm", secret.Algorithm),
			zap.Int("key_version", secret.KeyVersion),
			zap.Error(err),
		)
		return fmt.Errorf("upsert encrypted secret %q: %w", secret.Key, err)
	}

	return nil
}

// DeleteEncryptedSecret 删除指定密文,仅用于写入后验证失败时恢复旧状态.
// 参数 key 是密钥的键名.
func (r *sqliteRepo) DeleteEncryptedSecret(ctx context.Context, key string) error {
	// .Where("secret_key = ?", key) 按键名找
	// .Delete(&ormEncryptedSecret{}) 删除匹配的记录
	//   相当于 DELETE FROM encrypted_secrets WHERE secret_key = ?
	if err := r.db.WithContext(ctx).Where("secret_key = ?", key).Delete(&ormEncryptedSecret{}).Error; err != nil {
		logger.Error("encrypted_secrets delete failed",
			zap.String("key", key),
			zap.Error(err),
		)
		return fmt.Errorf("delete encrypted secret %q: %w", key, err)
	}

	return nil
}

// appSettingToORM 把业务对象 AppSetting 转成 ORM 模型.
// 字段一个个搬过去,没有特殊处理.
func appSettingToORM(setting AppSetting) ormAppSetting {
	return ormAppSetting{
		Key: setting.Key, Value: setting.Value, ValueType: setting.ValueType,
		Description: setting.Description, UpdatedAt: setting.UpdatedAt,
	}
}

// appSettingFromORM 把 ORM 模型转回业务对象.
func appSettingFromORM(model ormAppSetting) AppSetting {
	return AppSetting{
		Key: model.Key, Value: model.Value, ValueType: model.ValueType,
		Description: model.Description, UpdatedAt: model.UpdatedAt,
	}
}

// encryptedSecretToORM 把业务对象 EncryptedSecret 转成 ORM 模型.
func encryptedSecretToORM(secret EncryptedSecret) ormEncryptedSecret {
	return ormEncryptedSecret{
		Key: secret.Key, Ciphertext: secret.Ciphertext, Nonce: secret.Nonce,
		Algorithm: secret.Algorithm, KeyVersion: secret.KeyVersion, UpdatedAt: secret.UpdatedAt,
	}
}

// encryptedSecretFromORM 把 ORM 模型转回业务对象.
func encryptedSecretFromORM(model ormEncryptedSecret) EncryptedSecret {
	return EncryptedSecret{
		Key: model.Key, Ciphertext: model.Ciphertext, Nonce: model.Nonce,
		Algorithm: model.Algorithm, KeyVersion: model.KeyVersion, UpdatedAt: model.UpdatedAt,
	}
}
