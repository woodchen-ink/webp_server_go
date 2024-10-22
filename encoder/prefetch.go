package encoder

import (
	"os"
	"path"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
	"webp_server_go/config"
	"webp_server_go/helper"

	"github.com/schollz/progressbar/v3"
	log "github.com/sirupsen/logrus"
)

func PrefetchImages() {
	sTime := time.Now()
	log.Infof("开始预取图像，使用 %d 个核心", config.Jobs)

	// 使用固定大小的工作池来限制并发
	workerPool := make(chan struct{}, config.Jobs)
	var wg sync.WaitGroup

	all := helper.FileCount(config.Config.ImgPath)
	log.Infof("总共需要处理 %d 个文件", all)
	bar := progressbar.Default(all, "预取进度")

	var processedCount int32 // 用于计数处理的文件数

	err := filepath.Walk(config.Config.ImgPath,
		func(picAbsPath string, info os.FileInfo, err error) error {
			if err != nil {
				log.Warnf("访问文件时出错: %s, 错误: %v", picAbsPath, err)
				return nil
			}
			if info.IsDir() {
				log.Debugf("跳过目录: %s", picAbsPath)
				return nil
			}
			if !helper.CheckAllowedType(picAbsPath) {
				log.Debugf("跳过不支持的文件类型: %s", picAbsPath)
				return nil
			}

			wg.Add(1)
			go func() {
				defer wg.Done()
				workerPool <- struct{}{}        // 获取工作槽
				defer func() { <-workerPool }() // 释放工作槽

				log.Debugf("开始处理文件: %s", picAbsPath)

				metadata := helper.ReadMetadata(picAbsPath, "", config.LocalHostAlias)
				avifAbsPath, webpAbsPath, jxlAbsPath := helper.GenOptimizedAbsPath(metadata, config.LocalHostAlias)

				if err := os.MkdirAll(path.Dir(avifAbsPath), 0755); err != nil {
					log.Warnf("创建目录失败: %s, 错误: %v", path.Dir(avifAbsPath), err)
					return
				}

				supported := map[string]bool{
					"raw": true, "webp": true, "avif": true, "jxl": true,
				}

				ConvertFilter(picAbsPath, jxlAbsPath, avifAbsPath, webpAbsPath, config.ExtraParams{Width: 0, Height: 0}, supported, nil)

				atomic.AddInt32(&processedCount, 1)
				log.Debugf("文件处理完成: %s (进度: %d/%d)", picAbsPath, atomic.LoadInt32(&processedCount), all)

				_ = bar.Add(1)
			}()

			return nil
		})

	wg.Wait() // 等待所有工作完成

	if err != nil {
		log.Errorf("遍历目录时发生错误: %v", err)
	}

	elapsed := time.Since(sTime)
	log.Infof("预取完成，共处理 %d 个文件，耗时 %s", atomic.LoadInt32(&processedCount), elapsed)
}
