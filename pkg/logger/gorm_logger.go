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
//
//	GORM 的 LogLevel 只有 Silent(1)/Error(2)/Warn(3)/Info(4),没有 Debug.
//	Info 是最高级别,GORM 会在 level=Info 时对每条 SQL 调 Trace 方法.
//	Trace 里统一转发到 logger,由项目日志级别决定最终出不输出.
package logger

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

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
//   - fc:延迟求值函数；开发环境会读取 SQL 并把字面量统一替换成问号
//   - err:执行错误,nil 表示成功
//
// 三种情况分别转发到不同日志级别:
//   - 成功(无错误) → Debug,前缀 [GORM]
//   - ErrRecordNotFound → Debug,前缀 [GORM]  (查不到记录是正常业务,不算错误)
//   - 其他真实错误(连接失败/约束冲突/语法错误等) → Error,前缀 [GORM][ERR]
func (l *gormLogger) Trace(ctx context.Context, begin time.Time, fc func() (sql string, rowsAffected int64), err error) {
	// GORM 返回的 SQL 已经插入真实参数，可能包含对话、记忆和密钥相关数据。
	// 开发环境只记录脱敏后的 SQL 结构；生产环境完全不记录 SQL。
	sqlText, rows := fc()
	safeSQL := ""
	if !IsProduction() {
		safeSQL = sanitizeSQL(sqlText)
	}

	// elapsed := time.Since(begin) 筗出这条 SQL 的执行耗时
	elapsed := time.Since(begin)

	// 三种情况分别处理:
	//   1. err == nil: SQL 执行成功,打 Debug
	//   2. err == gorm.ErrRecordNotFound: First()/Last() 查不到记录,正常业务情况,打 Debug
	//   3. 其他错误: 真实 SQL 错误(连接失败/约束冲突/语法错误),打 Error
	switch {
	case err == nil:
		Debug("[GORM] SQL executed",
			zap.String("sql", safeSQL),
			zap.Duration("duration", elapsed),
			zap.Int64("rows", rows),
		)
	case errors.Is(err, gorm.ErrRecordNotFound):
		Debug("[GORM] record not found",
			zap.String("sql", safeSQL),
			zap.Duration("duration", elapsed),
			zap.Int64("rows", rows),
		)
	default:
		fields := []zap.Field{
			zap.Duration("duration", elapsed),
			zap.Int64("rows", rows),
			zap.Error(err),
		}
		if !IsProduction() {
			fields = append(fields, zap.String("sql", safeSQL))
		}
		Error("[GORM] SQL execution failed", fields...)
	}
}

// sanitizeSQL 把 GORM 已插值 SQL 中的字符串、数字和二进制字面量替换为问号。
// 它保留表名、列名、操作符和 SQL 结构，便于开发调试，但不记录真实业务参数。
func sanitizeSQL(statement string) string {
	var out strings.Builder
	out.Grow(len(statement))
	for i := 0; i < len(statement); {
		ch := statement[i]
		if ch == '\'' {
			out.WriteByte('?')
			i++
			for i < len(statement) {
				if statement[i] != '\'' {
					i++
					continue
				}
				if i+1 < len(statement) && statement[i+1] == '\'' {
					i += 2
					continue
				}
				i++
				break
			}
			continue
		}
		if isSQLNumberStart(statement, i) {
			out.WriteByte('?')
			i = skipSQLNumber(statement, i)
			continue
		}
		out.WriteByte(ch)
		i++
	}
	safe := strings.Join(strings.Fields(out.String()), " ")
	if len(safe) > MaxDebugSQLLength {
		return safe[:MaxDebugSQLLength] + "..."
	}
	return safe
}

// isSQLNumberStart 判断当前位置是否是独立数字字面量的起点。
func isSQLNumberStart(statement string, index int) bool {
	if statement[index] < '0' || statement[index] > '9' {
		return false
	}
	if index == 0 {
		return true
	}
	previous := statement[index-1]
	return !isSQLIdentifierByte(previous)
}

// skipSQLNumber 跳过整数、小数、科学计数法和十六进制数字字面量。
func skipSQLNumber(statement string, index int) int {
	for index < len(statement) {
		ch := statement[index]
		if (ch >= '0' && ch <= '9') || ch == '.' || ch == 'e' || ch == 'E' || ch == '+' || ch == '-' || ch == 'x' || ch == 'X' || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F') {
			index++
			continue
		}
		break
	}
	return index
}

// isSQLIdentifierByte 判断字符是否属于未加引号的 SQL 标识符。
func isSQLIdentifierByte(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_'
}
