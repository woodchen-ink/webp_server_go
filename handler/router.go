package handler

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
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
		filename              = path.Base(reqURI)
	)

	log.Debugf("Incoming connection from %s %s", c.IP(), reqURIwithQuery)

	// 首先检查是否为图片文件
	if !isImageFile(filename) {
		log.Infof("Non-image file requested: %s", reqURI)
		return handleNonImageFile(c, reqURI)
	}

	// 检查文件类型是否允许
	if !helper.CheckAllowedType(filename) {
		msg := "File extension not allowed! " + filename
		log.Warn(msg)
		return c.Status(fiber.StatusBadRequest).SendString(msg)
	}

	// 解析额外参数
	extraParams := parseExtraParams(c)

	// 检查路径是否匹配 IMG_MAP 中的任何前缀
	matchedPrefix, matchedTarget := findMatchingPrefix(reqURI)
	if matchedPrefix == "" {
		log.Warnf("请求的路径不匹配任何配置的 IMG_MAP: %s", c.Path())
		return c.SendStatus(fiber.StatusNotFound)
	}

	// 构建 EXHAUST_PATH 中的文件路径
	exhaustFilename := buildExhaustFilename(reqURI, extraParams)

	// 检查文件是否已经在 EXHAUST_PATH 中
	if helper.FileExists(exhaustFilename) {
		log.Infof("文件已存在于 EXHAUST_PATH，直接提供服务: %s", exhaustFilename)
		return c.SendFile(exhaustFilename)
	}

	// 处理图像
	isLocalPath := strings.HasPrefix(matchedTarget, "./") || strings.HasPrefix(matchedTarget, "/")
	if isLocalPath {
		return handleLocalImage(c, matchedTarget, reqURI, exhaustFilename, extraParams)
	} else {
		return handleRemoteImage(c, matchedTarget, matchedPrefix, reqURIwithQuery, exhaustFilename, extraParams)
	}
}

func handleNonImageFile(c *fiber.Ctx, reqURI string) error {
	var redirectURL string

	for prefix, target := range config.Config.ImageMap {
		if strings.HasPrefix(reqURI, prefix) {
			if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
				redirectURL = target + strings.TrimPrefix(reqURI, prefix)
			} else {
				return c.SendFile(path.Join(target, strings.TrimPrefix(reqURI, prefix)))
			}
			break
		}
	}

	if redirectURL == "" {
		localPath := path.Join(config.Config.ImgPath, reqURI)
		if helper.FileExists(localPath) {
			return c.SendFile(localPath)
		} else {
			return c.SendStatus(fiber.StatusNotFound)
		}
	}

	log.Infof("Redirecting to: %s", redirectURL)
	return c.Redirect(redirectURL, fiber.StatusFound)
}

func isImageFile(filename string) bool {
	ext := strings.ToLower(path.Ext(filename))
	if ext == "" {
		return false
	}
	ext = ext[1:] // 移除开头的点

	allowedTypes := config.Config.AllowedTypes
	if len(allowedTypes) == 1 && allowedTypes[0] == "*" {
		allowedTypes = config.NewWebPConfig().AllowedTypes
	}

	return slices.Contains(allowedTypes, ext)
}

func parseRequestURI(c *fiber.Ctx) (string, string) {
	reqURIRaw, _ := url.QueryUnescape(c.Path())
	reqURIwithQueryRaw, _ := url.QueryUnescape(c.OriginalURL())
	return path.Clean(reqURIRaw), path.Clean(reqURIwithQueryRaw)
}

func parseExtraParams(c *fiber.Ctx) config.ExtraParams {
	width, _ := strconv.Atoi(c.Query("width"))
	height, _ := strconv.Atoi(c.Query("height"))
	maxHeight, _ := strconv.Atoi(c.Query("max_height"))
	maxWidth, _ := strconv.Atoi(c.Query("max_width"))
	return config.ExtraParams{
		Width:     width,
		Height:    height,
		MaxWidth:  maxWidth,
		MaxHeight: maxHeight,
	}
}

func findMatchingPrefix(reqURI string) (string, string) {
	for prefix, target := range config.Config.ImageMap {
		if strings.HasPrefix(reqURI, prefix) {
			return prefix, target
		}
	}
	return "", ""
}

func buildExhaustFilename(reqURI string, extraParams config.ExtraParams) string {
	exhaustFilename := path.Join(config.Config.ExhaustPath, reqURI)
	if extraParams.Width > 0 || extraParams.Height > 0 || extraParams.MaxWidth > 0 || extraParams.MaxHeight > 0 {
		ext := path.Ext(exhaustFilename)
		extraParamsStr := fmt.Sprintf("_w%d_h%d_mw%d_mh%d", extraParams.Width, extraParams.Height, extraParams.MaxWidth, extraParams.MaxHeight)
		exhaustFilename = exhaustFilename[:len(exhaustFilename)-len(ext)] + extraParamsStr + ext
	}
	return exhaustFilename
}

func handleLocalImage(c *fiber.Ctx, matchedTarget, reqURI, exhaustFilename string, extraParams config.ExtraParams) error {
	rawImageAbs := path.Join(matchedTarget, reqURI)

	if !helper.FileExists(rawImageAbs) {
		return c.Status(fiber.StatusNotFound).SendString("本地文件不存在")
	}

	return processAndSaveImage(c, rawImageAbs, exhaustFilename, extraParams)
}

func handleRemoteImage(c *fiber.Ctx, matchedTarget, matchedPrefix, reqURIwithQuery, exhaustFilename string, extraParams config.ExtraParams) error {
	targetUrl, err := url.Parse(matchedTarget)
	if err != nil {
		log.Errorf("解析目标 URL 失败: %v", err)
		return c.Status(fiber.StatusInternalServerError).SendString("服务器配置错误")
	}

	realRemoteAddr := buildRealRemoteAddr(targetUrl, matchedPrefix, reqURIwithQuery)

	rawImageAbs, isNewDownload, err := fetchRemoteImg(realRemoteAddr, targetUrl.Host)
	if err != nil {
		log.Errorf("获取远程图像失败: %v", err)
		return c.Status(fiber.StatusInternalServerError).SendString("无法获取远程图像")
	}

	err = processAndSaveImage(c, rawImageAbs, exhaustFilename, extraParams)
	if err != nil {
		return err
	}

	if isNewDownload {
		go schedule.ScheduleCleanup(rawImageAbs)
	}

	return c.SendFile(exhaustFilename)
}

func buildRealRemoteAddr(targetUrl *url.URL, matchedPrefix, reqURIwithQuery string) string {
	targetHost := targetUrl.Scheme + "://" + targetUrl.Host
	reqURIwithQuery = strings.Replace(reqURIwithQuery, matchedPrefix, targetUrl.Path, 1)
	if strings.HasSuffix(targetUrl.Path, "/") {
		reqURIwithQuery = strings.TrimPrefix(reqURIwithQuery, "/")
	}
	return targetHost + reqURIwithQuery
}

func processAndSaveImage(c *fiber.Ctx, rawImageAbs, exhaustFilename string, extraParams config.ExtraParams) error {
	isSmall, err := helper.IsFileSizeSmall(rawImageAbs, 30*1024) // 30KB
	if err != nil {
		log.Errorf("检查文件大小时出错: %v", err)
		return c.Status(fiber.StatusInternalServerError).SendString("处理图像时出错")
	}

	if err := os.MkdirAll(path.Dir(exhaustFilename), 0755); err != nil {
		log.Errorf("创建目标目录失败: %v", err)
		return c.Status(fiber.StatusInternalServerError).SendString("服务器错误")
	}

	if isSmall {
		if err := helper.CopyFile(rawImageAbs, exhaustFilename); err != nil {
			log.Errorf("复制小文件到 EXHAUST_PATH 失败: %v", err)
			return c.Status(fiber.StatusInternalServerError).SendString("处理图像时出错")
		}
	} else {
		err := encoder.ProcessAndSaveImage(rawImageAbs, exhaustFilename, extraParams)
		if err != nil {
			log.Warnf("处理图片失败，将直接复制原图: %v", err)
			if copyErr := helper.CopyFile(rawImageAbs, exhaustFilename); copyErr != nil {
				log.Errorf("复制原图到 EXHAUST_PATH 失败: %v", copyErr)
				return c.Status(fiber.StatusInternalServerError).SendString("处理图像时出错")
			}
			log.Infof("已将原图复制到 EXHAUST_PATH: %s", exhaustFilename)
		}
	}

	return c.SendFile(exhaustFilename)
}
