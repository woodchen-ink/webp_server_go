package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"time"
	"webp_server_go/config"
	"webp_server_go/encoder"
	"webp_server_go/handler"
	schedule "webp_server_go/schedule"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

var router *gin.Engine

func setupLogger() {
	log.SetOutput(os.Stdout)
	log.SetReportCaller(true)
	formatter := &log.TextFormatter{
		EnvironmentOverrideColors: true,
		FullTimestamp:             true,
		TimestampFormat:           config.TimeDateFormat,
		CallerPrettyfier: func(f *runtime.Frame) (string, string) {
			return fmt.Sprintf("[%d:%s]", f.Line, f.Function), ""
		},
	}
	log.SetFormatter(formatter)
	log.SetLevel(log.InfoLevel)

	// 设置 Gin 的日志格式
	gin.SetMode(gin.ReleaseMode)
	router.Use(gin.LoggerWithFormatter(func(param gin.LogFormatterParams) string {
		return fmt.Sprintf("%s - [%s] \"%s %s %s\" %d %s \"%s\" %s\n",
			param.ClientIP,
			param.TimeStamp.Format(config.TimeDateFormat),
			param.Method,
			param.Path,
			param.Request.Proto,
			param.StatusCode,
			param.Latency,
			param.Request.UserAgent(),
			param.ErrorMessage,
		)
	}))
	router.Use(gin.Recovery())

	fmt.Println("Allowed file types as source:", config.Config.AllowedTypes)
	fmt.Println("Convert to WebP Enabled:", config.Config.EnableWebP)
	fmt.Println("Convert to AVIF Enabled:", config.Config.EnableAVIF)
	fmt.Println("Convert to JXL Enabled:", config.Config.EnableJXL)
}

func init() {
	// Our banner
	banner := fmt.Sprintf(`
		▌ ▌   ▌  ▛▀▖ ▞▀▖                ▞▀▖
		▌▖▌▞▀▖▛▀▖▙▄▘ ▚▄ ▞▀▖▙▀▖▌ ▌▞▀▖▙▀▖ ▌▄▖▞▀▖
		▙▚▌▛▀ ▌ ▌▌   ▖ ▌▛▀ ▌  ▐▐ ▛▀ ▌   ▌ ▌▌ ▌
		▘ ▘▝▀▘▀▀ ▘   ▝▀ ▝▀▘▘   ▘ ▝▀▘▘   ▝▀ ▝▀
		
		WebP Server Go - v%s
		Developed by WebP Server team. https://github.com/webp-sh`, config.Version)

	// main init is the last one to be called
	flag.Parse()
	// process cli params
	if config.DumpConfig {
		fmt.Println(config.SampleConfig)
		os.Exit(0)
	}
	if config.ShowVersion {
		fmt.Printf("\n %c[1;32m%s%c[0m\n\n", 0x1B, banner+"", 0x1B)
		os.Exit(0)
	}
	config.LoadConfig()
	fmt.Printf("\n %c[1;32m%s%c[0m\n\n", 0x1B, banner, 0x1B)

	// 设置 Gin 为发布模式
	gin.SetMode(gin.ReleaseMode)

	// 初始化 Gin 路由
	router = gin.New()
	setupLogger()
}

func monitorMemoryUsage() {
	ticker := time.NewTicker(1 * time.Minute)
	for range ticker.C {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		log.Infof("Alloc = %v MiB, TotalAlloc = %v MiB, Sys = %v MiB, NumGC = %v",
			bToMb(m.Alloc), bToMb(m.TotalAlloc), bToMb(m.Sys), m.NumGC)
	}
}

func bToMb(b uint64) uint64 {
	return b / 1024 / 1024
}

func main() {
	if config.Config.MaxCacheSize != 0 {
		go schedule.CleanCache()
	}
	if config.Prefetch {
		go encoder.PrefetchImages()
	}

	listenAddress := config.Config.Host + ":" + config.Config.Port

	// 设置路由 - 注意顺序
	router.GET("/healthz", handler.Healthz) // 具体路由放在前面
	router.NoRoute(handler.Convert)         // 使用 NoRoute 替代 /*path

	// 设置服务器参数
	server := &http.Server{
		Addr:              listenAddress,
		Handler:           router,
		ReadTimeout:       time.Second * 30,
		WriteTimeout:      time.Second * 30,
		ReadHeaderTimeout: time.Second * 10,
		MaxHeaderBytes:    config.Config.ReadBufferSize,
	}

	go monitorMemoryUsage()

	fmt.Println("WebP Server Go is Running on http://" + listenAddress)

	// 启动服务器
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Failed to start server: %v", err)
	}
}
