package logger

import (
	"os"
	"time"

	"github.com/sirupsen/logrus"
)

// New creates a new structured logger with enterprise-ready configuration
func New() *logrus.Logger {
	log := logrus.New()

	// Set output to stdout
	log.SetOutput(os.Stdout)

	// Use JSON formatter for production-ready structured logging
	log.SetFormatter(&logrus.JSONFormatter{
		TimestampFormat: time.RFC3339,
		FieldMap: logrus.FieldMap{
			logrus.FieldKeyTime:  "timestamp",
			logrus.FieldKeyLevel: "level",
			logrus.FieldKeyMsg:   "message",
			logrus.FieldKeyFunc:  "function",
		},
	})

	// Set default log level
	log.SetLevel(logrus.InfoLevel)

	// Add hooks for additional processing if needed
	// log.AddHook(&ContextHook{})

	return log
}

// NewWithFields creates a logger with predefined fields
func NewWithFields(fields logrus.Fields) *logrus.Entry {
	return New().WithFields(fields)
}

// NewForComponent creates a logger for a specific component
func NewForComponent(component string) *logrus.Entry {
	return New().WithField("component", component)
}

// ContextHook adds context information to log entries
type ContextHook struct{}

func (hook *ContextHook) Fire(entry *logrus.Entry) error {
	// Add additional context like request ID, user ID, etc.
	// This can be enhanced based on requirements
	return nil
}

func (hook *ContextHook) Levels() []logrus.Level {
	return logrus.AllLevels
}
