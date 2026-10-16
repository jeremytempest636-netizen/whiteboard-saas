// Package logger builds the structured Zap logger used across the service.
package logger

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// New returns a JSON logger in production and a readable console logger in
// development. nodeID is attached to every line so multi-node logs can be
// told apart.
func New(production bool, level, nodeID string) (*zap.Logger, error) {
	var cfg zap.Config
	if production {
		cfg = zap.NewProductionConfig()
		cfg.EncoderConfig.TimeKey = "ts"
		cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	} else {
		cfg = zap.NewDevelopmentConfig()
		cfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	}

	lvl := zap.NewAtomicLevel()
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl.SetLevel(zapcore.InfoLevel)
	}
	cfg.Level = lvl

	l, err := cfg.Build(zap.Fields(zap.String("node", nodeID)))
	if err != nil {
		return nil, err
	}
	return l, nil
}
