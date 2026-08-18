package data

// migrator.go 是数据库版本化迁移的执行器.
//
// 做的事情:
//  1. 从 script/migrations/ 目录加载所有 NNNN_name.sql 迁移文件,按版本号排序.
//  2. 建 schema_migrations 元数据表(记录每个版本是否执行过,执行状态,文件指纹).
//  3. 逐个执行迁移文件:没执行过的 → 插 running 记录 → 执行 SQL → 标记 completed;
//     已执行过的 → 比对文件指纹防止篡改;执行失败的 → 标记 failed 并写入错误信息.
//
// 铁律:已执行的迁移文件不可修改,要改表结构必须新写更高版本的迁移文件.

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// TableName 指定迁移记录表名,不靠 GORM 的复数命名规则.
func (MigrationRecord) TableName() string {
	return "schema_migrations"
}

// migrateSQLite 按版本顺序执行尚未完成的 SQL 迁移.
// 这是数据库初始化的核心流程,由 NewSQLite 调用.
// 流程:加载磁盘迁移文件 → 建元数据表 → 逐个执行迁移.
func migrateSQLite(ctx context.Context, db *gorm.DB, migrationsDir string) error {
	// 第一步:从磁盘加载所有合法的迁移文件,按版本号排序
	migrations, err := loadMigrations(migrationsDir)
	if err != nil {
		return err
	}

	// 第二步:建 schema_migrations 元数据表(首次启动时建,之后跳过)
	if err := createMigrationTable(ctx, db); err != nil {
		return err
	}

	// 第三步:按版本号从小到大逐个执行迁移
	for _, migration := range migrations {
		if err := applyMigration(ctx, db, migration); err != nil {
			return err
		}
	}

	// 所有迁移执行完毕,打一条汇总日志
	logger.Info("all database migrations completed",
		zap.Int("migration_count", len(migrations)),
	)
	return nil
}

// loadMigrations 加载 NNNN_name.sql 文件,并为内容计算 SHA-256 校验值.
func loadMigrations(dir string) ([]Migration, error) {
	// 打开 script/migrations/ 目录,拿到里面所有文件和子目录的列表
	// 目录不存在或没权限就报错返回
	entries, err := os.ReadDir(dir)

	if err != nil {
		return nil, fmt.Errorf("read migrations dir %s: %w", dir, err)
	}

	// 规定迁移文件必须长这样:NNNN_name.sql,比如 0001_init.sql,0012_add_chat_requests.sql
	// 捕获组 1 (\d{4}) 四位数字版本号
	// 捕获组 2 ([a-z0-9][a-z0-9_-]*) 名称,必须以小写字母或数字开头,后面可以有下划线和短横线
	// 不符合命名规则的文件会报错,等于强制你按规矩命名
	// regexp.MustCompile 把一段正则文本编译成可用的正则对象(*regexp.Regexp),之后可以用它去匹配字符串
	pattern := regexp.MustCompile(`^(\d{4})_([a-z0-9][a-z0-9_-]*)\.sql$`)

	// 预分配一个切片,长度 0,容量等于目录里的条目数
	migrations := make([]Migration, 0, len(entries))

	// 跳过子目录和非 .sql 文件.然后用正则校验文件名,不合法直接报错返回——不只是跳过,而是直接 fail,防止你的迁移文件因为打错名字被静默忽略
	for _, entry := range entries {
		// 如果是文件夹 或者 文件后缀不是 .sql,就跳过,不处理
		// entry 是 os.DirEntry 类型的变量,.Name() 作用是返回文件名(不含目录路径)
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}

		// 拿正则去匹配文件名字符串.返回一个 []string 切片,内容取决于是否匹配上
		// 合法时 matches = ["0001_init.sql", "0001", "init"]
		// 不合法时 matches = nil
		matches := pattern.FindStringSubmatch(entry.Name())
		if matches == nil {
			return nil, fmt.Errorf("invalid migration filename %q: want NNNN_name.sql", entry.Name())
		}

		// 正则已经保证是四位数字,这里转成 int,后面排序和比对都靠它
		// strconv.Atoi — Go 标准库里"字符串转整数"的函数,全名 ASCII to Integer
		version, err := strconv.Atoi(matches[1])
		if err != nil {
			return nil, fmt.Errorf("parse migration version %q: %w", entry.Name(), err)
		}

		// 拼出文件的完整路径,后面读文件用
		// 举个例子,假设 dir 是 "script/migrations",entry.Name() 是 "0001_init.sql"
		// path = "script/migrations/0001_init.sql"
		// filepath.Join 会自动处理路径分隔符.Windows 上拼出来是 script\migrations\0001_init.sql,Linux 上是 script/migrations/0001_init.sql,跨平台不用管
		path := filepath.Join(dir, entry.Name())

		// 用这个 path 去读文件内容
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", path, err)
		}

		// 对文件内容算一个 SHA-256 指纹,用来检测迁移文件有没有被偷偷改过
		// Go 标准库 crypto/sha256 包里的函数,对任意内容算出一个固定 32 字节的哈希值
		sum := sha256.Sum256(content)

		// 把解析好的一个迁移文件信息打包成一个 Migration 结构体,塞进切片里
		migrations = append(migrations,
			Migration{
				Version: version,
				Name:    matches[2],
				Path:    path,
				// 因为 sum 是 32 个字节的数组,不能直接赋值给 string,需要转成十六进制字符串
				Checksum: fmt.Sprintf("%x", sum),
				SQL:      string(content),
			},
		)
	}
	// 按版本号从小到大排序
	// sort.Slice 需要两个参数,一个是要比较的切片,一个是比较的逻辑
	// 要排序的切片 migrations
	// 比较的逻辑 func
	sort.Slice(migrations,
		func(i, j int) bool {
			return migrations[i].Version < migrations[j].Version
		})

	// 检查有没有版本号重复的迁移文件
	for i := 1; i < len(migrations); i++ {
		// 如果两个相邻的版本号一样就报错
		if migrations[i-1].Version == migrations[i].Version {
			return nil, fmt.Errorf("duplicate migration version %04d", migrations[i].Version)
		}
	}

	// 目录存在但里面一个合法的迁移文件都没有,就报错
	if len(migrations) == 0 {
		return nil, fmt.Errorf("no migration files found in %s", dir)
	}

	// 返回切片
	return migrations, nil
}

// createMigrationTable 创建迁移系统的元数据表 schema_migrations.
// 首次启动时由 GORM 自动建表;表已存在则跳过.
//
// 鸡生蛋问题:迁移器依赖 schema_migrations 记录状态,
// 但 schema_migrations 本身不能用迁移文件建(迁移文件还没开始跑),
// 所以这张表由 GORM 直接创建.users/sessions 等业务表则只能走迁移文件.
func createMigrationTable(ctx context.Context, db *gorm.DB) error {
	// GORM 自带一个表结构管理工具叫 Migrator(),能建表,查表是否存在,加列加索引等
	migrator := db.WithContext(ctx).Migrator()

	// 表已存在就跳过(非首次启动)
	if migrator.HasTable(&MigrationRecord{}) {
		return nil
	}

	// 用 GORM 自动建表.
	// schema_migrations 是迁移器运行所依赖的元数据表,因此由 GORM 创建.
	// users,sessions 等业务表仍然只能通过 script/migrations 中的 SQL 修改.
	if err := migrator.CreateTable(&MigrationRecord{}); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	// 首次建表打一条日志,方便排查"表是什么时候建的"
	logger.Info("created migration metadata table schema_migrations")
	return nil
}

// applyMigration 执行单个迁移文件:先查 schema_migrations 表是否已记录该版本.
// 已完成则校验文件名和内容校验值是否被篡改;未执行则标记 running → 执行 SQL → 标记 completed.
// SQL 执行失败时标记 failed 并记录错误信息,事务自动回滚.
func applyMigration(ctx context.Context, db *gorm.DB, migration Migration) error {
	// 把结构体实例放到一个变量里,准备接收数据库查出来的记录
	var existing MigrationRecord

	// 查 schema_migrations 表里有没有这个版本号的记录
	// .Select(migrationRecordColumns) 只查需要的列,不用 SELECT *
	// .Where("version = ?", migration.Version) 按 version 查
	// .Take(&existing) 查一条记录,找不到不报错(返回空结构体)
	err := db.WithContext(ctx).Select(migrationRecordColumns).Where("version = ?", migration.Version).Take(&existing).Error

	// 查到了,而且已经执行完了
	if err == nil && existing.Status == MigrationStatusCompleted {
		// existing — 读数据库 schema_migrations 表,上次执行时存进去的记录
		// migration — 读磁盘迁移文件,这次刚从 script/migrations/0001_init.sql 里加载出来的
		// 比对文件名和 SHA-256 指纹,防止有人偷偷改了已执行的迁移文件
		if existing.Name != migration.Name || existing.Checksum != migration.Checksum {
			// 迁移文件被篡改,铁律:已执行的迁移不可改,要改结构需新写更高版本的迁移文件
			logger.Error("migration file changed after execution, refusing to run",
				zap.Int("version", migration.Version),
				zap.String("saved_name", existing.Name),
				zap.String("current_name", migration.Name),
				zap.String("saved_checksum", existing.Checksum),
				zap.String("current_checksum", migration.Checksum),
			)
			return fmt.Errorf("migration %04d changed after completion", migration.Version)
		}
		// 文件名和指纹都一致,说明这个版本已执行且未被篡改,跳过
		logger.Debug("migration already executed, skipping",
			zap.Int("version", migration.Version),
			zap.String("name", migration.Name),
		)
		return nil
	}

	// 不是"没找到",是真正的数据库错误(连接断了等)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("read migration %04d record: %w", migration.Version, err)
	}

	// 到这里说明 schema_migrations 表里没有这个版本的记录,是首次执行

	// 记录开始时间,后面要存到 StartedAt 字段
	startedAt := time.Now()

	// 构造一条 running 状态的记录,先插进数据库.
	// 防崩保护:先插 running 再执行 SQL,程序中途崩了重启后能识别未完成的迁移.
	record := MigrationRecord{
		// 版本号,如 1
		Version: migration.Version,
		// 迁移名称,如 "init"
		Name: migration.Name,
		// 文件指纹,如 "a1b2c3..."
		Checksum: migration.Checksum,
		// 状态:正在执行
		Status: MigrationStatusRunning,
		// 开始时间
		StartedAt: startedAt,
	}
	// Save 插入一条记录到 schema_migrations 表
	if err := db.WithContext(ctx).Save(&record).Error; err != nil {
		return fmt.Errorf("mark migration %04d running: %w", migration.Version, err)
	}

	// 记录完成时间,在事务提交成功后写入
	completedAt := time.Now()

	// 用事务执行迁移 SQL,同时更新状态为 completed.
	// 两步放同一事务里,保证"SQL 执行成功"和"状态标记完成"要么都成功要么都回滚.
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 执行迁移文件里的 SQL(建表,加列等)
		if err := tx.Exec(migration.SQL).Error; err != nil {
			return err
		}
		// SQL 执行成功,把状态从 running 更新为 completed
		return tx.Model(&MigrationRecord{}).
			Where("version = ?", migration.Version).
			Updates(map[string]any{
				"status": MigrationStatusCompleted, "completed_at": completedAt,
				"error_message": nil,
			}).Error
	}); err != nil {
		// 事务失败了(SQL 语法错误,表已存在等),数据库自动回滚.

		// 1. 取出事务失败的原因,比如 "near THIS: syntax error"
		message := err.Error()
		// 2. context.WithoutCancel(ctx) 创建一个新的 context
		//    它继承了 ctx 里的值(比如 trace ID),但不会被取消
		//    为什么要这样？因为 ctx 可能带 60 秒超时,事务执行了很久,
		//    ctx 可能已经快到期或已经取消了.
		//    如果直接用 ctx,这个 UPDATE 也会失败,failed 状态就写不进去.
		//    用 WithoutCancel 保证"标记失败"这个操作一定能执行.
		updateErr := db.WithContext(context.WithoutCancel(ctx)).
			// 3. 指定要操作的表(MigrationRecord → schema_migrations)
			Model(&MigrationRecord{}).
			// 4. WHERE version = ?,按版本号找到刚才那条 running 记录
			Where("version = ?", migration.Version).
			// 5. UPDATE SET status='failed', error_message='near THIS: syntax error'
			Updates(map[string]any{
				"status":        MigrationStatusFailed, // 状态改成 failed
				"error_message": message,               // 记录失败原因,方便排查
			}).Error
		if updateErr != nil {
			// 标记失败本身也失败了,两个错误一起返回
			logger.Error("migration execution failed and marking failure status also failed",
				zap.Int("version", migration.Version),
				zap.String("migration_path", migration.Path),
				zap.Error(err),
				zap.NamedError("update_error", updateErr),
			)
			return fmt.Errorf("execute migration %04d: %v; record failure: %w", migration.Version, err, updateErr)
		}
		// 标记失败成功了,打 Error 日志
		logger.Error("migration execution failed, marked as failed",
			zap.Int("version", migration.Version),
			zap.String("name", migration.Name),
			zap.String("migration_path", migration.Path),
			zap.Error(err),
		)
		return fmt.Errorf("execute migration %04d (%s): %w", migration.Version, migration.Path, err)
	}

	// 迁移执行成功,打 Info 日志
	logger.Info("migration executed successfully",
		zap.Int("version", migration.Version),
		zap.String("name", migration.Name),
		zap.Int64("duration_ms", time.Since(startedAt).Milliseconds()),
	)
	return nil
}
