package observability

import (
	"log/slog"
	"os"
)

var Logger *slog.Logger

func InitLogger() {
	handler := slog.NewJSONHandler(os.Stdout, nil)
	Logger = slog.New(handler)
}

func Info(msg string, kv ...any) {
	Logger.Info(msg, kv...)
}

func Error(msg string, kv ...any) {
	Logger.Error(msg, kv...)
}
