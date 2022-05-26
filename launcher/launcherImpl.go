package launcher

import (
	"context"
	"github.com/renfy96/renfy/pkg/shutdown"
	"go.uber.org/zap"
	"net/http"
	"sync"
	"time"

	"github.com/renfy96/renfy/runtime"
)

type launcherImpl struct {
	// 应用名，该名字应该为应用所在的包名
	name string
	// 服务启动环境 缺省值为Fat
	env string
	// 日志
	logger *zap.Logger
	// http服务
	server *http.Server
	// 启动一次
	runOnce *sync.Once
	// 关闭一次
	stopOnce *sync.Once
}

func (l launcherImpl) Name() string {
	return l.name
}

func (l launcherImpl) Run() {
	l.runOnce.Do(func() {
		err := l.server.ListenAndServe()
		runtime.Must(err)
	})
}

func (l launcherImpl) Stop() {
	l.stopOnce.Do(func() {
		// 优雅关闭
		shutdown.NewHook().Close(
			// 关闭服务
			func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
				defer cancel()
				err := l.server.Shutdown(ctx)
				runtime.Must(err)
			},
		)

	})
}

func (l launcherImpl) Logger() *zap.Logger {
	return l.logger
}
