package database

import (
	"gorm.io/gorm/logger"
)

// newGormLogger 按字符串 level 构造 GORM logger。当前直接复用 GORM 内置
// 默认 logger，只切换级别——本骨架对 SQL 日志没有特殊定制需求，等业务真
// 要做"慢查询采样 / 脱敏字段"等再换自定义实现。
func newGormLogger(level string) logger.Interface {
	return logger.Default.LogMode(parseLogLevel(level))
}
