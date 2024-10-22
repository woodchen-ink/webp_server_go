package encoder

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sync"
	"time"
	"webp_server_go/config"
	"webp_server_go/helper"

	"github.com/schollz/progressbar/v3"
	log "github.com/sirupsen/logrus"
)

func PrefetchImages() {
	sTime := time.Now()
	log.Infof("Prefetching using %d cores", config.Jobs)

	// 使用固定大小的工作池来限制并发
	workerPool := make(chan struct{}, config.Jobs)
	var wg sync.WaitGroup

	all := helper.FileCount(config.Config.ImgPath)
	bar := progressbar.Default(all, "Prefetching...")

	err := filepath.Walk(config.Config.ImgPath,
		func(picAbsPath string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !helper.CheckAllowedType(picAbsPath) {
				return nil
			}

			wg.Add(1)
			go func() {
				defer wg.Done()
				workerPool <- struct{}{}        // 获取工作槽
				defer func() { <-workerPool }() // 释放工作槽

				metadata := helper.ReadMetadata(picAbsPath, "", config.LocalHostAlias)
				avifAbsPath, webpAbsPath, jxlAbsPath := helper.GenOptimizedAbsPath(metadata, config.LocalHostAlias)

				_ = os.MkdirAll(path.Dir(avifAbsPath), 0755)

				log.Infof("Prefetching %s", picAbsPath)

				supported := map[string]bool{
					"raw": true, "webp": true, "avif": true, "jxl": true,
				}

				ConvertFilter(picAbsPath, jxlAbsPath, avifAbsPath, webpAbsPath, config.ExtraParams{Width: 0, Height: 0}, supported, nil)
				_ = bar.Add(1)
			}()

			return nil
		})

	wg.Wait() // 等待所有工作完成

	if err != nil {
		log.Errorln(err)
	}
	elapsed := time.Since(sTime)
	_, _ = fmt.Fprintf(os.Stdout, "Prefetch complete in %s\n\n", elapsed)
}
