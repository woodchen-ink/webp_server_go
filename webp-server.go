package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"time"
	"webp_server_go/config"
	"webp_server_go/encoder"
	"webp_server_go/handler"

	schedule "webp_server_go/schedule"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	log "github.com/sirupsen/logrus"
)

// https://docs.gofiber.io/api/fiber
var app = fiber.New(fiber.Config{
	ServerHeader:          "WebP Server Go",
	AppName:               "WebP Server Go",
	DisableStartupMessage: true,
	ProxyHeader:           "X-Real-IP",
	ReadBufferSize:        config.Config.ReadBufferSize, // 用于请求读取的每个连接缓冲区大小。这也限制了最大标头大小。如果您的客户端发送多 KB RequestURI 和/或多 KB 标头（例如，BIG cookies），请增加此缓冲区。
	WriteBufferSize:       1024 * 4,
	Concurrency:           config.Config.Concurrency,      // 最大并发连接数。
	DisableKeepalive:      config.Config.DisableKeepalive, // 禁用保持活动连接，服务器将在向客户端发送第一个响应后关闭传入连接
})

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

	// fiber logger format
	app.Use(logger.New(logger.Config{
		Format:     config.FiberLogFormat,
		TimeFormat: config.TimeDateFormat,
	}))
	app.Use(recover.New(recover.Config{}))
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

	app.Get("/healthz", handler.Healthz)
	app.Get("/*", handler.Convert)

	go monitorMemoryUsage()

	fmt.Println("WebP Server Go is Running on http://" + listenAddress)

	_ = app.Listen(listenAddress)
}
