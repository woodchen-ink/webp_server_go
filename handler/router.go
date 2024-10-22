package handler

import (
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"
	"webp_server_go/config"
	"webp_server_go/encoder"
	"webp_server_go/helper"

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
	exhaustFilename := path.Join(config.Config.ExhaustPath, reqURI)
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

	// 文件不在 EXHAUST_PATH 中，需要处理
	isLocalPath := strings.HasPrefix(matchedTarget, "./") || strings.HasPrefix(matchedTarget, "/")
	var rawImageAbs string
	if isLocalPath {
		// 处理本地路径
		localPath := strings.TrimPrefix(reqURI, matchedPrefix)
		rawImageAbs = path.Join(matchedTarget, localPath)
	} else {
		// 处理远程URL
		targetUrl, _ := url.Parse(matchedTarget)
		remoteAddr := targetUrl.Scheme + "://" + targetUrl.Host + strings.Replace(reqURI, matchedPrefix, targetUrl.Path, 1)
		metadata := fetchRemoteImg(remoteAddr, targetUrl.Host)
		rawImageAbs = path.Join(config.Config.RemoteRawPath, targetUrl.Host, metadata.Id)
	}

	// 检查是否为允许的图片文件
	if !helper.IsAllowedImageFile(filename) {
		log.Infof("不允许的文件类型或非图片文件: %s", reqURI)
		return c.SendFile(rawImageAbs)
	}

	// 处理图片
	err := encoder.ProcessAndSaveImage(rawImageAbs, exhaustFilename, extraParams)
	if err != nil {
		log.Errorf("处理图片失败: %v", err)
		return c.SendStatus(fiber.StatusInternalServerError)
	}

	return c.SendFile(exhaustFilename)
}
