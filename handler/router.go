package handler

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"webp_server_go/config"
	"webp_server_go/encoder"
	"webp_server_go/helper"
	"webp_server_go/schedule"

	"github.com/gofiber/fiber/v2"
	log "github.com/sirupsen/logrus"
)

func Convert(c *fiber.Ctx) error {
	// 检查是否为根路径
	if c.Path() == "/" {
		return c.SendString("Welcome to CZL WebP Server")
	}

	var (
		reqURIRaw, _          = url.QueryUnescape(c.Path())
		reqURIwithQueryRaw, _ = url.QueryUnescape(c.OriginalURL())
		reqURI                = path.Clean(reqURIRaw)
		reqURIwithQuery       = path.Clean(reqURIwithQueryRaw)

		filename = path.Base(reqURI)

		width, _     = strconv.Atoi(c.Query("width"))
		height, _    = strconv.Atoi(c.Query("height"))
		maxHeight, _ = strconv.Atoi(c.Query("max_height"))
		maxWidth, _  = strconv.Atoi(c.Query("max_width"))
		extraParams  = config.ExtraParams{
			Width:     width,
			Height:    height,
			MaxWidth:  maxWidth,
			MaxHeight: maxHeight,
		}
	)

	log.Debugf("Incoming connection from %s %s", c.IP(), reqURIwithQuery)

	// 检查路径是否匹配 IMG_MAP 中的任何前缀
	var matchedPrefix string
	var matchedTarget string
	for prefix, target := range config.Config.ImageMap {
		if strings.HasPrefix(reqURI, prefix) {
			matchedPrefix = prefix
			matchedTarget = target
			break
		}
	}

	// 如果不匹配任何 IMG_MAP 前缀，直接返回 404
	if matchedPrefix == "" {
		log.Warnf("请求的路径不匹配任何配置的 IMG_MAP: %s", c.Path())
		return c.SendStatus(fiber.StatusNotFound)
	}

	// 构建 EXHAUST_PATH 中的文件路径
	exhaustFilename := path.Join(config.Config.ExhaustPath, strings.TrimPrefix(reqURI, matchedPrefix))
	if extraParams.Width > 0 || extraParams.Height > 0 || extraParams.MaxWidth > 0 || extraParams.MaxHeight > 0 {
		ext := path.Ext(exhaustFilename)
		extraParamsStr := fmt.Sprintf("_w%d_h%d_mw%d_mh%d", extraParams.Width, extraParams.Height, extraParams.MaxWidth, extraParams.MaxHeight)
		exhaustFilename = exhaustFilename[:len(exhaustFilename)-len(ext)] + extraParamsStr + ext
	}

	// 检查文件是否已经在 EXHAUST_PATH 中
	if helper.FileExists(exhaustFilename) {
		log.Infof("文件已存在于 EXHAUST_PATH，直接提供服务: %s", exhaustFilename)
		return c.SendFile(exhaustFilename)
	}

	// 使用 sync.Once 确保并发安全
	var once sync.Once
	var processErr error
	processImage := func() {
		once.Do(func() {
			// 文件不在 EXHAUST_PATH 中，需要处理
			isLocalPath := strings.HasPrefix(matchedTarget, "./") || strings.HasPrefix(matchedTarget, "/")
			var rawImageAbs string
			var isNewDownload bool

			if isLocalPath {
				// 处理本地路径
				localPath := strings.TrimPrefix(reqURI, matchedPrefix)
				rawImageAbs = path.Join(matchedTarget, localPath)

				// 检查本地文件是否存在
				if !helper.FileExists(rawImageAbs) {
					processErr = fmt.Errorf("本地文件不存在: %s", rawImageAbs)
					return
				}
				isNewDownload = false // 本地文件不需要清理
			} else {
				// 处理远程URL
				targetUrl, err := url.Parse(matchedTarget)
				if err != nil {
					processErr = fmt.Errorf("解析目标 URL 失败")
					log.Errorf("%s: %v", processErr, err)
					return
				}

				// 构建正确的远程地址
				remoteAddr := targetUrl.String()
				if !strings.HasSuffix(remoteAddr, "/") {
					remoteAddr += "/"
				}
				remoteAddr += strings.TrimPrefix(reqURI, matchedPrefix)

				rawImageAbs, isNewDownload, err = fetchRemoteImg(remoteAddr, targetUrl.Host)
				if err != nil {
					processErr = fmt.Errorf("获取远程图像失败")
					log.Errorf("%s: %v", processErr, err)
					return
				}
			}

			// 检查是否为允许的图片文件
			if !helper.IsAllowedImageFile(filename) {
				log.Infof("不允许的文件类型或非图片文件: %s", reqURI)
				// 直接复制文件到 EXHAUST_PATH
				if err := helper.CopyFile(rawImageAbs, exhaustFilename); err != nil {
					processErr = fmt.Errorf("复制不允许处理的文件失败: %v", err)
				}
				return
			}

			// 处理图片
			isSmall, err := helper.IsFileSizeSmall(rawImageAbs, 100*1024) // 100KB
			if err != nil {
				processErr = fmt.Errorf("检查文件大小时出错: %v", err)
				return
			}

			// 确保目标目录存在
			if err := os.MkdirAll(path.Dir(exhaustFilename), 0755); err != nil {
				processErr = fmt.Errorf("创建目标目录失败: %v", err)
				return
			}

			if isSmall {
				if err := helper.CopyFile(rawImageAbs, exhaustFilename); err != nil {
					processErr = fmt.Errorf("复制小文件到 EXHAUST_PATH 失败: %v", err)
					return
				}
			} else {
				if err := encoder.ProcessAndSaveImage(rawImageAbs, exhaustFilename, extraParams); err != nil {
					processErr = fmt.Errorf("处理图片失败: %v", err)
					return
				}
			}

			// 如果是新下载的远程文件，安排清理任务
			if !isLocalPath && isNewDownload {
				go schedule.ScheduleCleanup(rawImageAbs)
			}
		})
	}

	// 处理图片
	processImage()
	if processErr != nil {
		log.Error(processErr)
		return c.Status(fiber.StatusInternalServerError).SendString(processErr.Error())
	}

	// 再次检查文件是否存在（以防并发情况下的竞态条件）
	if !helper.FileExists(exhaustFilename) {
		return c.Status(fiber.StatusInternalServerError).SendString("处理后的文件未找到")
	}

	// 发送文件
	return c.SendFile(exhaustFilename)
}
