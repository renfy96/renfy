package launcher

import (
	"fmt"
	"net/http"
	"net/url"
	"runtime/debug"
	"time"

	"github.com/gin-contrib/pprof"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	cors "github.com/rs/cors/wrapper/gin"
	ginSwagger "github.com/swaggo/gin-swagger"
	"go.uber.org/multierr"
	"golang.org/x/time/rate"

	"github.com/swaggo/gin-swagger/swaggerFiles"
	"go.uber.org/zap"

	"github.com/renfy96/renfy/code"
	"github.com/renfy96/renfy/conf"
	"github.com/renfy96/renfy/core"
	"github.com/renfy96/renfy/pkg/env"
	"github.com/renfy96/renfy/pkg/trace"
	"github.com/renfy96/renfy/proposal"
)

// withoutTracePaths 这些请求，默认不记录日志
var withoutTracePaths = map[string]bool{
	"/metrics": true,

	"/debug/pprof/":             true,
	"/debug/pprof/cmdline":      true,
	"/debug/pprof/profile":      true,
	"/debug/pprof/symbol":       true,
	"/debug/pprof/trace":        true,
	"/debug/pprof/allocs":       true,
	"/debug/pprof/block":        true,
	"/debug/pprof/goroutine":    true,
	"/debug/pprof/heap":         true,
	"/debug/pprof/mutex":        true,
	"/debug/pprof/threadcreate": true,

	"/favicon.ico": true,

	"/system/health": true,
}

type builderImpl struct {
	launcher launcherImpl
	mux      core.Mux
}

func (b builderImpl) Build() (Launcher, error) {
	return b.launcher, nil
}

func NewBuilder(logger *zap.Logger, options ...Option) Builder {
	builder := &builderImpl{
		mux: core.NewMux(),
		launcher: launcherImpl{
			name:   conf.ProjectName,
			env:    env.Active().Value(),
			logger: logger,
			server: nil,
		},
	}

	option := new(option)
	for _, opt := range options {
		opt(option)
	}
	// 配置设置
	optionSetting(builder, option)

	// recover两次，防止处理时发生panic，尤其是在OnPanicNotify中。
	builder.mux.Engine().Use(func(ctx *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				logger.Error("got panic", zap.String("panic", fmt.Sprintf("%+v", err)), zap.String("stack", string(debug.Stack())))
			}
		}()

		ctx.Next()
	})

	builder.mux.Engine().Use(func(ctx *gin.Context) {
		if ctx.Writer.Status() == http.StatusNotFound {
			ctx.HTML(http.StatusNotFound, "", "404 not found")
			return
		}

		ts := time.Now()

		context := core.NewContext(ctx)
		defer core.ReleaseContext(context)

		context.Init()
		context.SetLogger(logger)
		context.AbleRecordMetrics()

		if !withoutTracePaths[ctx.Request.URL.Path] {
			if traceId := context.GetHeader(trace.Header); traceId != "" {
				context.SetTrace(trace.New(traceId))
			} else {
				context.SetTrace(trace.New(""))
			}
		}

		defer func() {
			var (
				response        interface{}
				businessCode    int
				businessCodeMsg string
				abortErr        error
				traceId         string
				graphResponse   interface{}
			)

			if ct := context.Trace(); ct != nil {
				context.SetHeader(trace.Header, ct.ID())
				traceId = ct.ID()
			}

			// region 发生 Panic 异常发送告警提醒
			if err := recover(); err != nil {
				stackInfo := string(debug.Stack())
				logger.Error("got panic", zap.String("panic", fmt.Sprintf("%+v", err)), zap.String("stack", stackInfo))
				context.AbortWithError(core.Error(
					http.StatusInternalServerError,
					code.ServerError,
					code.Text(code.ServerError)),
				)

				if notifyHandler := option.alertNotify; notifyHandler != nil {
					notifyHandler(&proposal.AlertMessage{
						ProjectName:  conf.ProjectName,
						Env:          env.Active().Value(),
						TraceID:      traceId,
						HOST:         context.Host(),
						URI:          context.URI(),
						Method:       context.Method(),
						ErrorMessage: err,
						ErrorStack:   stackInfo,
						Timestamp:    time.Now(),
					})
				}
			}

			// region 发生错误，进行返回
			if ctx.IsAborted() {
				for i := range ctx.Errors {
					multierr.AppendInto(&abortErr, ctx.Errors[i])
				}

				if err := context.AbortError(); err != nil { // customer err
					// 判断是否需要发送告警通知
					if err.IsAlert() {
						if notifyHandler := option.alertNotify; notifyHandler != nil {
							notifyHandler(&proposal.AlertMessage{
								ProjectName:  conf.ProjectName,
								Env:          env.Active().Value(),
								TraceID:      traceId,
								HOST:         context.Host(),
								URI:          context.URI(),
								Method:       context.Method(),
								ErrorMessage: err.Message(),
								ErrorStack:   fmt.Sprintf("%+v", err.StackError()),
								Timestamp:    time.Now(),
							})
						}
					}

					multierr.AppendInto(&abortErr, err.StackError())
					businessCode = err.BusinessCode()
					businessCodeMsg = err.Message()
					response = &code.Failure{
						Code:    businessCode,
						Message: businessCodeMsg,
					}
					ctx.JSON(err.HTTPCode(), response)
				}
			}

			// endregion

			// region 正确返回
			response = context.GetPayload()
			if response != nil {
				ctx.JSON(http.StatusOK, response)
			}
			// endregion

			// region 记录指标
			if option.recordHandler != nil && context.IsRecordMetrics() {
				path := context.Path()
				if alias := context.Alias(); alias != "" {
					path = alias
				}

				option.recordHandler(&proposal.MetricsMessage{
					ProjectName:  conf.ProjectName,
					Env:          env.Active().Value(),
					TraceID:      traceId,
					HOST:         context.Host(),
					Path:         path,
					Method:       context.Method(),
					HTTPCode:     ctx.Writer.Status(),
					BusinessCode: businessCode,
					CostSeconds:  time.Since(ts).Seconds(),
					IsSuccess:    !ctx.IsAborted() && (ctx.Writer.Status() == http.StatusOK),
				})
			}
			// endregion

			// region 记录日志
			var t *trace.Trace
			if x := context.Trace(); x != nil {
				t = x.(*trace.Trace)
			} else {
				return
			}

			decodedURL, _ := url.QueryUnescape(ctx.Request.URL.RequestURI())

			// ctx.Request.Header，精简 Header 参数
			traceHeader := map[string]string{
				"Content-Type": ctx.GetHeader("Content-Type"),
			}

			t.WithRequest(&trace.Request{
				TTL:        "un-limit",
				Method:     ctx.Request.Method,
				DecodedURL: decodedURL,
				Header:     traceHeader,
				Body:       string(context.RawData()),
			})

			var responseBody interface{}

			if response != nil {
				responseBody = response
			}

			graphResponse = context.GetGraphPayload()
			if graphResponse != nil {
				responseBody = graphResponse
			}

			t.WithResponse(&trace.Response{
				Header:          ctx.Writer.Header(),
				HttpCode:        ctx.Writer.Status(),
				HttpCodeMsg:     http.StatusText(ctx.Writer.Status()),
				BusinessCode:    businessCode,
				BusinessCodeMsg: businessCodeMsg,
				Body:            responseBody,
				CostSeconds:     time.Since(ts).Seconds(),
			})

			t.Success = !ctx.IsAborted() && (ctx.Writer.Status() == http.StatusOK)
			t.CostSeconds = time.Since(ts).Seconds()

			logger.Info("trace-log",
				zap.Any("method", ctx.Request.Method),
				zap.Any("path", decodedURL),
				zap.Any("http_code", ctx.Writer.Status()),
				zap.Any("business_code", businessCode),
				zap.Any("success", t.Success),
				zap.Any("cost_seconds", t.CostSeconds),
				zap.Any("trace_id", t.Identifier),
				zap.Any("trace_info", t),
				zap.Error(abortErr),
			)
			// endregion
		}()

		ctx.Next()
	})

	system := builder.mux.Group("/system")
	{
		// 健康检查
		system.GET("/health", func(ctx core.Context) {
			resp := &struct {
				Timestamp   time.Time `json:"timestamp"`
				Environment string    `json:"environment"`
				Host        string    `json:"host"`
				Status      string    `json:"status"`
			}{
				Timestamp:   time.Now(),
				Environment: env.Active().Value(),
				Host:        ctx.Host(),
				Status:      "ok",
			}
			ctx.Payload(resp)
		})
	}

	return builder
}

func optionSetting(builder *builderImpl, option *option) {
	// 开启性能分析
	if option.enablePProf {
		pprof.Register(builder.mux.Engine())
	}
	// 开启文档
	if option.enableSwagger {
		builder.mux.Engine().GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	}
	// 开启指标监控
	if option.enablePrometheus {
		builder.mux.Engine().GET("/metrics", gin.WrapH(promhttp.Handler()))
	}
	// 开启跨域
	if option.enableCors {
		builder.mux.Engine().Use(cors.New(cors.Options{
			AllowedOrigins: []string{"*"},
			AllowedMethods: []string{
				http.MethodHead,
				http.MethodGet,
				http.MethodPost,
				http.MethodPut,
				http.MethodPatch,
				http.MethodDelete,
			},
			AllowedHeaders:     []string{"*"},
			AllowCredentials:   true,
			OptionsPassthrough: true,
		}))
	}
	// 开启限流
	if option.enableRate {
		limiter := rate.NewLimiter(rate.Every(time.Second*1), conf.MaxRequestsPerSecond)
		builder.mux.Engine().Use(func(ctx *gin.Context) {
			context := core.NewContext(ctx)
			defer core.ReleaseContext(context)

			if !limiter.Allow() {
				context.AbortWithError(core.Error(
					http.StatusTooManyRequests,
					code.TooManyRequests,
					code.Text(code.TooManyRequests)),
				)
				return
			}

			ctx.Next()
		})
	}
}
