// sqlite.go 放 SQLite 数据库的初始化逻辑.
//
// 做的事情:
//  1. 创建数据库文件目录(不存在则自动建).
//  2. 用 GORM 打开 SQLite 连接(WAL 模式 + busy_timeout),用自定义日志适配器替换 GORM 默认日志.
//  3. Ping 数据库验证连接可用.
//  4. 执行版本化迁移(migrateSQLite).
//  5. 返回 sqliteRepo 实例给上层使用.
package data

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/swallow-sun/swallow-go/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// NewSQLite 创建基于 GORM 的 SQLite Repository.
// dbPath 是数据库文件路径,migrationsDir 是版本化 SQL 所在目录.
// 返回值是 Repository 接口类型,上层拿到后直接调接口方法,不用关心底层是 SQLite 还是 MySQL.
func NewSQLite(dbPath, migrationsDir string) (Repository, error) {
	// filepath.Dir 把文件路径里的目录部分抽出来
	// 举个例子,dbPath 是 "data/swallow.db",dir 就是 "data"
	// dbPath 是 "swallow.db",dir 就是 ".",表示当前目录
	dir := filepath.Dir(dbPath)

	// dir 不是空字符串,也不是当前目录,说明用户指定了一个子目录
	// 这种情况下要先把这个目录建出来,不然 gorm.Open 创建文件会失败
	if dir != "" && dir != "." {
		// os.MkdirAll 递归创建目录,如果目录已经存在不会报错
		// 0755 是权限:所有者可读写执行,其他人可读可执行
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("create db dir: %w", err)
		}
	}

	// 拼接 SQLite 的 DSN(Data Source Name,数据源名称),就是告诉 SQLite 驱动怎么连数据库
	// file:xxx.db?_journal_mode=WAL&_busy_timeout=5000
	//   - _journal_mode=WAL:WAL 模式让读写可以并发,性能比默认的 rollback journal 好很多
	//   - _busy_timeout=5000:写冲突时最多等 5 秒(5000 毫秒),超时再报 "database is locked"
	// 项目只保存关联 ID,不在数据库里建外键约束,所以这里没开 foreign_keys
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=5000", dbPath)

	// gorm.Open 打开数据库连接,第一个参数是驱动(这里是 SQLite),第二个参数是配置
	// sqlite.Open(dsn) 返回一个 GORM 能用的 SQLite 驱动实例
	// &gorm.Config{} 里配了两样东西:
	//   - Logger: 用自定义适配器 logger.NewGORMLogger() 替换 GORM 默认日志.
	//     每条 SQL 语句转发到 logger.Debug,开发环境能看到实际执行的 SQL.
	//   - DisableForeignKeyConstraintWhenMigrating: 关掉 GORM 建表时自动生成外键约束
	//     我们项目不用数据库外键,免得迁移的时候外键检查报错
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger:                                   logger.NewGORMLogger(),
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		return nil, fmt.Errorf("open sqlite with gorm: %w", err)
	}

	// db.DB() 从 GORM 里取出底层的 *sql.DB(Go 标准库 database/sql 的连接池对象)
	// 取出来是为了做 Ping 验证连接和后面 Close 用
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sqlite connection pool: %w", err)
	}

	// sqlDB.Ping() 给数据库发一个最简单的请求,验证连接是不是通的
	// 不通就关掉连接再报错返回
	if err := sqlDB.Ping(); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	// 连接通了,执行版本化迁移,建表,加列等
	// 这里用 context.Background() 而不是传进来的 ctx,因为这是启动阶段,
	// 迁移必须跑完,不想被上层 ctx 超时打断
	if err := migrateSQLite(context.Background(), db, migrationsDir); err != nil {
		// 迁移失败先关连接再报错
		_ = sqlDB.Close()
		return nil, fmt.Errorf("migrate sqlite schema: %w", err)
	}

	// 打一条日志,记录数据库路径,方便排查"连的是哪个库"
	logger.Info("SQLite database migration completed", zap.String("path", dbPath))

	// 返回 sqliteRepo 实例,指针包在 Repository 接口里
	// sqliteRepo 里就一个字段 db,所以后面所有方法都靠它来操作数据库
	return &sqliteRepo{db: db}, nil
}
