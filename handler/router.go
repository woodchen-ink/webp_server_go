package handler

import (
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
		reqHostname = c.Hostname()
		reqHeader   = &c.Request().Header

		reqURIRaw, _          = url.QueryUnescape(c.Path())
		reqURIwithQueryRaw, _ = url.QueryUnescape(c.OriginalURL())
		reqURI                = path.Clean(reqURIRaw)
		reqURIwithQuery       = path.Clean(reqURIwithQueryRaw)

		filename       = path.Base(reqURI)
		realRemoteAddr = ""
		targetHostName = ""
		targetHost     = ""

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

	log.Debugf("Incoming connection from %s %s %s", c.IP(), reqHostname, reqURIwithQuery)

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

	isLocalPath := strings.HasPrefix(matchedTarget, "./") || strings.HasPrefix(matchedTarget, "/")

	var rawImageAbs string
	var metadata config.MetaFile

	if isLocalPath {
		// 处理本地路径
		localPath := strings.TrimPrefix(reqURI, matchedPrefix)
		rawImageAbs = path.Join(matchedTarget, localPath)

		if !helper.FileExists(rawImageAbs) {
			log.Warnf("本地文件不存在: %s", rawImageAbs)
			return c.SendStatus(fiber.StatusNotFound)
		}

		// 为本地文件创建或获取元数据
		metadata = helper.ReadMetadata(reqURIwithQuery, "", reqHostname)
		if metadata.Checksum != helper.HashFile(rawImageAbs) {
			log.Info("本地文件已更改，更新元数据...")
			metadata = helper.WriteMetadata(reqURIwithQuery, "", reqHostname)
		}
	} else {
		// 处理远程URL
		targetUrl, _ := url.Parse(matchedTarget)
		targetHostName = targetUrl.Host
		targetHost = targetUrl.Scheme + "://" + targetUrl.Host
		reqURI = strings.Replace(reqURI, matchedPrefix, targetUrl.Path, 1)
		reqURIwithQuery = strings.Replace(reqURIwithQuery, matchedPrefix, targetUrl.Path, 1)
		realRemoteAddr = targetHost + reqURIwithQuery

		// 获取远程图像元数据
		metadata = fetchRemoteImg(realRemoteAddr, targetHostName)
		rawImageAbs = path.Join(config.Config.RemoteRawPath, targetHostName, metadata.Id)
	}

	// 检查是否为允许的图片文件
	if !helper.IsAllowedImageFile(filename) {
		log.Infof("不允许的文件类型或非图片文件: %s", reqURI)
		if isLocalPath {
			return c.SendFile(rawImageAbs)
		} else {
			log.Infof("Redirecting to: %s", realRemoteAddr)
			return c.Redirect(realRemoteAddr, fiber.StatusFound)
		}
	}

	// 检查原始图像是否存在
	if !helper.ImageExists(rawImageAbs) {
		helper.DeleteMetadata(reqURIwithQuery, targetHostName)
		msg := "Image not found!"
		log.Warn(msg)
		return c.Status(404).SendString(msg)
	}

	// 检查文件大小
	isSmall, err := helper.IsFileSizeSmall(rawImageAbs, 100*1024) // 100KB
	if err != nil {
		log.Errorf("检查文件大小时出错: %v", err)
		return c.SendStatus(fiber.StatusInternalServerError)
	}

	var finalFilename string
	if isSmall {
		log.Infof("文件 %s 小于100KB，直接缓存到 EXHAUST_PATH", rawImageAbs)
		finalFilename = path.Join(config.Config.ExhaustPath, targetHostName, metadata.Id)
		if err := helper.CopyFile(rawImageAbs, finalFilename); err != nil {
			log.Errorf("复制小文件到 EXHAUST_PATH 失败: %v", err)
			return c.SendStatus(fiber.StatusInternalServerError)
		}
	} else {
		avifAbs, webpAbs, jxlAbs := helper.GenOptimizedAbsPath(metadata, targetHostName)

		// 确定支持的格式
		supportedFormats := helper.GuessSupportedFormat(reqHeader)
		// 根据支持的格式和配置进行转换
		encoder.ConvertFilter(rawImageAbs, jxlAbs, avifAbs, webpAbs, extraParams, supportedFormats, nil)

		var availableFiles = []string{rawImageAbs}
		if supportedFormats["avif"] {
			availableFiles = append(availableFiles, avifAbs)
		}
		if supportedFormats["webp"] {
			availableFiles = append(availableFiles, webpAbs)
		}
		if supportedFormats["jxl"] {
			availableFiles = append(availableFiles, jxlAbs)
		}

		finalFilename = helper.FindSmallestFiles(availableFiles)
	}

	contentType := helper.GetFileContentType(finalFilename)
	c.Set("Content-Type", contentType)

	c.Set("X-Compression-Rate", helper.GetCompressionRate(rawImageAbs, finalFilename))
	return c.SendFile(finalFilename)
}
