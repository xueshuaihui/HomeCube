package obs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	gormlogger "gorm.io/gorm/logger"
)

// gormLog bridges GORM's logging onto this package's structured logger, so SQL warnings and slow
// queries carry the same code/service attributes as every other line (docs/p1-tech-plan.md §1.2
// 「结构化日志」, PRD 22.2 第 10 条「日志…必须带 family_id 与 code」).
//
// Without it GORM writes its own format to stderr and the two streams stop being one machine
// readable log.
type gormLog struct {
	log   *slog.Logger
	level gormlogger.LogLevel
}

// slowQueryThreshold matches gorm's own default (logger.DefaultSlowThreshold = 200ms); keeping the
// library's number means the card introduces no new tuning constant.
const slowQueryThreshold = 200 * time.Millisecond

// NewGormLogger returns a logger that emits Warn and Error only: statement-level SQL is not logged,
// because §10.3's latency histogram already covers 「接口延迟」, and full SQL in a family-private
// deployment would put row values into the log stream (PRD 二十章数据分级).
func NewGormLogger(log *slog.Logger) gormlogger.Interface {
	return &gormLog{log: log, level: gormlogger.Warn}
}

// LogMode implements logger.Interface.
func (l *gormLog) LogMode(level gormlogger.LogLevel) gormlogger.Interface {
	clone := *l
	clone.level = level
	return &clone
}

func (l *gormLog) enabled(min gormlogger.LogLevel) bool { return l.level >= min }

// Info implements logger.Interface. At this package's Warn level it is silent by design.
func (l *gormLog) Info(ctx context.Context, msg string, args ...any) {
	if !l.enabled(gormlogger.Info) {
		return
	}
	l.log.InfoContext(ctx, fmt.Sprintf(msg, args...))
}

// Warn implements logger.Interface.
func (l *gormLog) Warn(ctx context.Context, msg string, args ...any) {
	if !l.enabled(gormlogger.Warn) {
		return
	}
	l.log.WarnContext(ctx, fmt.Sprintf(msg, args...))
}

// Error implements logger.Interface.
func (l *gormLog) Error(ctx context.Context, msg string, args ...any) {
	if !l.enabled(gormlogger.Error) {
		return
	}
	l.log.ErrorContext(ctx, fmt.Sprintf(msg, args...))
}

// Trace implements logger.Interface: one line per executed statement that either failed or was
// slow. A miss that is simply 「no such row」 (ErrRecordNotFound) is not an error and is not logged --
// that is gorm's own convention and the alternative would flood the log on every 404 path.
func (l *gormLog) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	if !l.enabled(gormlogger.Warn) {
		return
	}
	elapsed := time.Since(begin)
	sql, rows := fc()

	switch {
	case err != nil && !errors.Is(err, gormlogger.ErrRecordNotFound) && l.enabled(gormlogger.Error):
		l.log.ErrorContext(ctx, "gorm_statement",
			"sql", sql, "rows", rows, "elapsed_ms", elapsed.Milliseconds(), "err", err.Error())
	case elapsed > slowQueryThreshold && l.enabled(gormlogger.Warn):
		l.log.WarnContext(ctx, "gorm_slow_query",
			"sql", sql, "rows", rows, "elapsed_ms", elapsed.Milliseconds())
	}
}
