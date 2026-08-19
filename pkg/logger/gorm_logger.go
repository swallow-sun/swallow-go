// gorm_logger.go 放 GORM 的日志适配器,把 GORM 内部的 SQL 日志转发到项目全局 logger.
//
// 做的事情:
//  1. NewGORMLogger 创建适配器,GORM LogLevel 固定设 Info(最高级别,每条 SQL 都触发 Trace).
//  2. 实现 gorm.io/gorm/logger.Interface 接口的 LogMode/Info/Error/Trace 四个方法.
//  3. Trace 方法是核心:GORM 每执行一条 SQL 都会调它,按结果分三级转发:
//     - 成功 → logger.Debug("[GORM] ...")
//     - ErrRecordNotFound → logger.Debug("[GORM] ...")  (查不到记录是正常业务情况,不算错误)
//     - 其他真实错误 → logger.Error("[GORM][ERR] ...")  (连接失败/约束冲突/语法错误等)
//
// 为什么 GORM LogLevel 固定 Info:
//   GORM 的 LogLevel 只有 Silent(1)/Error(2)/Warn(3)/Info(4),没有 Debug.
//   Info 是最高级别,GORM 会在 level=Info 时对每条 SQL 调 Trace 方法.
//   Trace 里统一转发到 logger,由项目日志级别决定最终出不输出.
package logger

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// gormLogger 把 GORM 的 SQL 日志转发到项目全局 logger.
type gormLogger struct{}

// NewGORMLogger 创建一个 GORM 日志适配器.
// GORM LogLevel 固定设 Info(最高级别),每条 SQL 都会调 Trace.
// Trace 里统一转发到 logger.Debug,由项目日志级别决定最终出不输出.
func NewGORMLogger() gormlogger.Interface {
	return &gormLogger{}
}

// LogMode 返回适配器的副本.
// GORM 内部有时会调这个方法临时切换日志级别(比如 db.Debug()).
// 我们不希望被 GORM 切换,所以返回原适配器的副本,忽略传入的 level.
// gormLogger 没有级别字段,所有 SQL 统一走 Trace → logger.Debug,由项目日志级别决定最终出不输出.
func (l *gormLogger) LogMode(level gormlogger.LogLevel) gormlogger.Interface {
	newLogger := *l
	return &newLogger
}

// Info 打 GORM 内部的 Info 级别日志(比如迁移过程中的提示信息).
func (l *gormLogger) Info(ctx context.Context, msg string, data ...interface{}) {
	Info(fmt.Sprintf(msg, data...))
}

// Warn 打 GORM 内部的 Warn 级别日志.
func (l *gormLogger) Warn(ctx context.Context, msg string, data ...interface{}) {
	Warn(fmt.Sprintf(msg, data...))
}

// Error 打 GORM 内部的 Error 级别日志.
func (l *gormLogger) Error(ctx context.Context, msg string, data ...interface{}) {
	Error(fmt.Sprintf(msg, data...))
}

// Trace 是 GORM SQL 日志的核心方法.
// GORM 每执行一条 SQL(Query/Create/Update/Delete)都会调这个方法,传入:
//   - ctx:当前请求的 context
//   - begin:SQL 开始执行的时间
//   - fc:延迟求值函数,调它才能拿到 SQL 语句和行数
//   - err:执行错误,nil 表示成功
//
// 三种情况分别转发到不同日志级别:
//   - 成功(无错误) → Debug,前缀 [GORM]
//   - ErrRecordNotFound → Debug,前缀 [GORM]  (查不到记录是正常业务,不算错误)
//   - 其他真实错误(连接失败/约束冲突/语法错误等) → Error,前缀 [GORM][ERR]
func (l *gormLogger) Trace(ctx context.Context, begin time.Time, fc func() (sql string, rowsAffected int64), err error) {
	// fc() 是 GORM 传进来的延迟求值函数,调它才能拿到 SQL 语句和行数.
	// GORM 用延迟求值是为了在日志级别不够时不执行格式化(省性能).
	sql, rows := fc()

	// elapsed := time.Since(begin) 筗出这条 SQL 的执行耗时
	elapsed := time.Since(begin)

	// 三种情况分别处理:
	//   1. err == nil: SQL 执行成功,打 Debug
	//   2. err == gorm.ErrRecordNotFound: First()/Last() 查不到记录,正常业务情况,打 Debug
	//   3. 其他错误: 真实 SQL 错误(连接失败/约束冲突/语法错误),打 Error
	var msg string
	switch {
	case err == nil:
		// 成功,不带错误信息
		msg = fmt.Sprintf("[GORM] %s %s | rows=%d", elapsed.String(), sql, rows)
		Debug(msg)
	case errors.Is(err, gorm.ErrRecordNotFound):
		// 查不到记录,不是错误,前缀不带 [ERR]
		msg = fmt.Sprintf("[GORM] %s %s | rows=%d | err=%v", elapsed.String(), sql, rows, err)
		Debug(msg)
	default:
		// 真实错误,走 Error 级别
		msg = fmt.Sprintf("[GORM][ERR] %s %s | rows=%d | err=%v", elapsed.String(), sql, rows, err)
		Error(msg)
	}
}
