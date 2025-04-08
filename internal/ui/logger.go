package ui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mum4k/termdash/widgets/text"
)

type Logger interface {
	// Default log functions
	Debug(args ...any)
	Info(args ...any)
	Warn(args ...any)
	Error(args ...any)
	Fatal(args ...any)
	Panic(args ...any)

	// Log functions with format
	Printf(format string, args ...any)
	Debugf(format string, args ...any)
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
	Panicf(format string, args ...any)
}

// LoggerWriter defines a simple interface for writing logs.
type LoggerWriter interface {
	Write(text string, wOpts ...text.WriteOption) error
}

// LoggerLevel represents supported logging levels.
type LoggerLevel int

const (
	LevelDebug LoggerLevel = iota
	LevelInfo
	LevelWarn
	LevelError
	LevelFatal
	LevelPanic
	LevelNone
)

// UILogger is the main logger implementation for UI output.
type UILogger struct {
	LoggerWriter
}

// NewUILogger creates a new UILogger with the given writer.
func NewUILogger(writer LoggerWriter) *UILogger {
	return &UILogger{writer}
}

func (u *UILogger) Debugf(format string, args ...any) { u.logf(LevelDebug, format, args...) }
func (u *UILogger) Infof(format string, args ...any)  { u.logf(LevelInfo, format, args...) }
func (u *UILogger) Warnf(format string, args ...any)  { u.logf(LevelWarn, format, args...) }
func (u *UILogger) Errorf(format string, args ...any) { u.logf(LevelError, format, args...) }
func (u *UILogger) Fatalf(format string, args ...any) { u.logf(LevelFatal, format, args...) }
func (u *UILogger) Panicf(format string, args ...any) { u.logf(LevelPanic, format, args...) }
func (u *UILogger) Printf(format string, args ...any) { u.logf(LevelNone, format, args...) }

// logf is the internal function to handle formatted log messages.
func (u *UILogger) logf(level LoggerLevel, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	u.output(level, msg)
}

func (u *UILogger) Debug(args ...any) { u.log(LevelDebug, args...) }
func (u *UILogger) Info(args ...any)  { u.log(LevelInfo, args...) }
func (u *UILogger) Warn(args ...any)  { u.log(LevelWarn, args...) }
func (u *UILogger) Error(args ...any) { u.log(LevelError, args...) }
func (u *UILogger) Fatal(args ...any) { u.log(LevelFatal, args...) }
func (u *UILogger) Panic(args ...any) { u.log(LevelPanic, args...) }

// log is the internal function to handle plain messages.
func (u *UILogger) log(level LoggerLevel, args ...any) {
	msg := fmt.Sprint(args...)
	u.output(level, msg)
}

// output handles final message formatting and dispatching based on log level.
func (u *UILogger) output(level LoggerLevel, msg string) {
	switch level {
	case LevelFatal:
		u.Write(formatLog(msg, level))
		os.Exit(1)
	case LevelPanic:
		panic(formatLog(msg, level))
	case LevelNone:
		u.Write(msg)
	default:
		u.Write(formatLog(msg, level))
	}
}

// --- Formatting utilities ---

// formatLog combines timestamp, level, and message into a complete log line.
func formatLog(message string, level LoggerLevel) string {
	timestamp := formatTimestamp(time.Now(), "2006-01-02 15:04:05")
	levelStr := getLevelColor(level)
	msg := formatMessage(message)
	return fmt.Sprintf("%s %s %s\n", timestamp, levelStr, msg)
}

// getLevelColor returns a color-coded label for the given log level.
func getLevelColor(level LoggerLevel) string {
	switch level {
	case LevelDebug:
		return "[DBG]"
	case LevelInfo:
		return "[INF]"
	case LevelWarn:
		return "[WAR]"
	case LevelError:
		return "[ERR]"
	case LevelFatal:
		return "[FTL]"
	case LevelPanic:
		return "[PAN]"
	default:
		return ""
	}
}

// formatMessage trims or pads the message and returns it in white.
func formatMessage(msg string) string {
	const maxSize = 80

	if msg == "" {
		return ">"
	}
	if len(msg) > maxSize {
		msg = msg[:maxSize]
	} else {
		msg += strings.Repeat(" ", maxSize-len(msg))
	}

	return fmt.Sprintf("> %s", msg)
}

// formatTimestamp formats the given time using the specified layout.
func formatTimestamp(t time.Time, layout string) string {
	return fmt.Sprintf("[%s]", t.Format(layout))
}
