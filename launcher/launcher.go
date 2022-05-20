package launcher

import (
	"go.uber.org/zap"
)

type Launcher interface {
	// Name 应用名
	Name() string
	// Run 运行Launcher，该方法只可调用一次
	// 该方法将阻塞当前goroutine
	Run()
	// Stop 关闭服务
	Stop()
	// Logger 启动器使用的日志
	Logger() *zap.Logger
}
