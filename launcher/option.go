package launcher

import (
	"github.com/renfy96/renfy/proposal"
	"go.uber.org/zap"
)

type Option func(*option)

type option struct {
	// 启动 pprof 性能分析
	enablePProf bool
	// 启动 swagger 文档
	enableSwagger bool
	// 启动 Prometheus 指标监控
	enablePrometheus bool
	// 启动 cors 跨域
	enableCors bool
	// 启动 rate 限流
	enableRate bool
	// 每秒最大请求
	maxRequestsPerSecond int

	alertNotify   proposal.NotifyHandler
	recordHandler proposal.RecordHandler

	name   string
	logger *zap.Logger
}

func WithEnablePProf() Option {
	return func(o *option) {
		o.enablePProf = true
	}
}

func WithEnableSwagger() Option {
	return func(o *option) {
		o.enableSwagger = true
	}
}

func WithEnablePrometheus() Option {
	return func(o *option) {
		o.enablePrometheus = true
	}
}

func WithEnableCors() Option {
	return func(o *option) {
		o.enableCors = true
	}
}

func WithEnableRate() Option {
	return func(o *option) {
		o.enableRate = true
	}
}

func WithName(name string) Option {
	return func(o *option) {
		o.name = name
	}
}

func WithLogger(logger *zap.Logger) Option {
	return func(o *option) {
		o.logger = logger
	}
}

func WithMaxRequestsPerSecond(max int) Option {
	return func(o *option) {
		o.maxRequestsPerSecond = max
	}
}
