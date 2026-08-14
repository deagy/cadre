package production

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// LogLevel represents the severity level of a log message.
type LogLevel int

const (
	DebugLevel LogLevel = iota
	InfoLevel
	WarnLevel
	ErrorLevel
	FatalLevel
)

// LogEntry represents a structured log entry.
type LogEntry struct {
	Timestamp   time.Time              `json:"timestamp"`
	Level       string                 `json:"level"`
	Message     string                 `json:"message"`
	Component   string                 `json:"component"`
	TraceID     string                 `json:"trace_id,omitempty"`
	RequestID   string                 `json:"request_id,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
	Error       string                 `json:"error,omitempty"`
	StackTrace  string                 `json:"stack_trace,omitempty"`
}

// ProductionLogger handles structured logging for production environments.
type ProductionLogger struct {
	mu          sync.Mutex
	writer      io.WriteCloser
	level       LogLevel
	format      string // json or text
	component   string
	filePath    string
	maxSize     int64
	maxBackups  int
}

// NewProductionLogger creates a new production logger.
func NewProductionLogger(config *Config, component string) (*ProductionLogger, error) {
	var writer io.WriteCloser

	if config.LogOutput == "stdout" {
		writer = os.Stdout
	} else {
		dir := filepath.Dir(config.LogOutput)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create log directory: %w", err)
		}

		f, err := os.OpenFile(config.LogOutput, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			return nil, fmt.Errorf("failed to open log file: %w", err)
		}

		writer = f
	}

	level := InfoLevel
	switch config.LogLevel {
	case "debug":
		level = DebugLevel
	case "info":
		level = InfoLevel
	case "warn":
		level = WarnLevel
	case "error":
		level = ErrorLevel
	case "fatal":
		level = FatalLevel
	}

	return &ProductionLogger{
		writer:     writer,
		level:      level,
		format:     config.LogFormat,
		component:  component,
		filePath:   config.LogOutput,
		maxSize:    100 * 1024 * 1024, // 100MB
		maxBackups: 10,
	}, nil
}

// Debug logs a debug message.
func (pl *ProductionLogger) Debug(message string, metadata map[string]interface{}) {
	if pl.level <= DebugLevel {
		pl.log(DebugLevel, message, "", metadata)
	}
}

// Info logs an info message.
func (pl *ProductionLogger) Info(message string, metadata map[string]interface{}) {
	if pl.level <= InfoLevel {
		pl.log(InfoLevel, message, "", metadata)
	}
}

// Warn logs a warning message.
func (pl *ProductionLogger) Warn(message string, metadata map[string]interface{}) {
	if pl.level <= WarnLevel {
		pl.log(WarnLevel, message, "", metadata)
	}
}

// Error logs an error message.
func (pl *ProductionLogger) Error(message string, err error, metadata map[string]interface{}) {
	if pl.level <= ErrorLevel {
		errMsg := ""
		if err != nil {
			errMsg = err.Error()
		}

		pl.log(ErrorLevel, message, errMsg, metadata)
	}
}

// Fatal logs a fatal message and exits.
func (pl *ProductionLogger) Fatal(message string, err error) {
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}

	pl.log(FatalLevel, message, errMsg, nil)
	pl.Close()
	os.Exit(1)
}

func (pl *ProductionLogger) log(level LogLevel, message, errMsg string, metadata map[string]interface{}) {
	pl.mu.Lock()
	defer pl.mu.Unlock()

	levelStr := levelToString(level)

	entry := LogEntry{
		Timestamp:  time.Now(),
		Level:      levelStr,
		Message:    message,
		Component:  pl.component,
		Metadata:   metadata,
		Error:      errMsg,
	}

	var output string
	if pl.format == "json" {
		data, _ := json.Marshal(entry)
		output = string(data)
	} else {
		output = pl.formatText(&entry)
	}

	fmt.Fprintln(pl.writer, output)
}

func (pl *ProductionLogger) formatText(entry *LogEntry) string {
	timestamp := entry.Timestamp.Format("2006-01-02 15:04:05")

	msg := fmt.Sprintf("[%s] %s | %s | %s", timestamp, entry.Level, entry.Component, entry.Message)

	if entry.Error != "" {
		msg += fmt.Sprintf(" | Error: %s", entry.Error)
	}

	return msg
}

// Close closes the logger.
func (pl *ProductionLogger) Close() error {
	pl.mu.Lock()
	defer pl.mu.Unlock()

	if pl.filePath != "stdout" && pl.writer != nil {
		return pl.writer.Close()
	}

	return nil
}

// LogRotationManager handles log rotation based on file size.
type LogRotationManager struct {
	filePath   string
	maxSize    int64
	maxBackups int
	mu         sync.Mutex
}

// NewLogRotationManager creates a new log rotation manager.
func NewLogRotationManager(filePath string, maxSize int64, maxBackups int) *LogRotationManager {
	return &LogRotationManager{
		filePath:   filePath,
		maxSize:    maxSize,
		maxBackups: maxBackups,
	}
}

// Rotate performs log rotation if the file exceeds maxSize.
func (lrm *LogRotationManager) Rotate() error {
	lrm.mu.Lock()
	defer lrm.mu.Unlock()

	fileInfo, err := os.Stat(lrm.filePath)
	if err != nil {
		return nil // File doesn't exist yet
	}

	if fileInfo.Size() < lrm.maxSize {
		return nil
	}

	// Rotate existing backups
	for i := lrm.maxBackups - 1; i > 0; i-- {
		oldName := fmt.Sprintf("%s.%d", lrm.filePath, i)
		newName := fmt.Sprintf("%s.%d", lrm.filePath, i+1)

		if _, err := os.Stat(oldName); err == nil {
			os.Rename(oldName, newName)
		}
	}

	// Rename current log to .1
	newName := fmt.Sprintf("%s.1", lrm.filePath)
	if err := os.Rename(lrm.filePath, newName); err != nil {
		return fmt.Errorf("failed to rotate log file: %w", err)
	}

	return nil
}

// LogContextKey is used to track logging context through requests.
type LogContextKey struct {
	TraceID   string
	RequestID string
}

// StructuredLogger wraps ProductionLogger with context tracking.
type StructuredLogger struct {
	logger    *ProductionLogger
	context   *LogContextKey
	mu        sync.RWMutex
}

// NewStructuredLogger creates a new structured logger with context.
func NewStructuredLogger(logger *ProductionLogger) *StructuredLogger {
	return &StructuredLogger{
		logger:  logger,
		context: &LogContextKey{},
	}
}

// WithContext sets the logging context for this request.
func (sl *StructuredLogger) WithContext(ctx *LogContextKey) *StructuredLogger {
	sl.mu.Lock()
	defer sl.mu.Unlock()

	sl.context = ctx
	return sl
}

// LogRequest logs an incoming request.
func (sl *StructuredLogger) LogRequest(method, path string, metadata map[string]interface{}) {
	sl.mu.RLock()
	_ = *sl.context
	sl.mu.RUnlock()

	if metadata == nil {
		metadata = make(map[string]interface{})
	}

	metadata["method"] = method
	metadata["path"] = path

	sl.logger.Info(fmt.Sprintf("%s %s", method, path), metadata)
}

// LogResponse logs an outgoing response.
func (sl *StructuredLogger) LogResponse(statusCode int, duration time.Duration, metadata map[string]interface{}) {
	sl.mu.RLock()
	_ = *sl.context
	sl.mu.RUnlock()

	if metadata == nil {
		metadata = make(map[string]interface{})
	}

	metadata["status"] = statusCode
	metadata["duration_ms"] = duration.Milliseconds()

	sl.logger.Info(fmt.Sprintf("Response %d", statusCode), metadata)
}

// Helper functions

func levelToString(level LogLevel) string {
	switch level {
	case DebugLevel:
		return "DEBUG"
	case InfoLevel:
		return "INFO"
	case WarnLevel:
		return "WARN"
	case ErrorLevel:
		return "ERROR"
	case FatalLevel:
		return "FATAL"
	default:
		return "UNKNOWN"
	}
}
