package utils

import (
	"context"
	"github.com/go-logr/logr"
)

var (
	Log = &logStruct{}
)

type logStruct struct {
	rootLogger logr.Logger
}

func (l *logStruct) SetLogger(logger logr.Logger) {
	l.rootLogger = logger
}

// FromContext returns a logger from a context.Context.
func (l *logStruct) FromContext(ctx context.Context, keysAndValues ...interface{}) logr.Logger {
	if l.rootLogger.GetSink() == nil {
		panic("SetLogger must be called firstly")
	}
	logger := l.rootLogger
	if ctx != nil {
		if l, err := logr.FromContext(ctx); err == nil {
			logger = l
		}
	}
	return logger.WithValues(keysAndValues...)
}

// IntoContext takes a context and sets the logger as one of its values.
// Use FromContext function to retrieve the logger.
func (l *logStruct) IntoContext(ctx context.Context, log logr.Logger) context.Context {
	return logr.NewContext(ctx, log)
}
