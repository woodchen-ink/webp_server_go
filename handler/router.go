package handler

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"webp_server_go/config"
	"webp_server_go/encoder"
	"webp_server_go/helper"
	"webp_server_go/schedule"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// 文件锁映射
var fileLocks = sync.Map{}

// 获取文件锁
func getFileLock(filename string) *sync.Mutex {
	actual, _ := fileLocks.LoadOrStore(filename, &sync.Mutex{})
	return actual.(*sync.Mutex)
}

func Convert(c *gin.Context) {
	// 检查是否为根路径
	if c.Request.URL.Path == "/" {
		c.String(200, "Welcome to CZL WebP Server")
		return
	}

	var (
		reqURIRaw, _          = url.QueryUnescape(c.Request.URL.Path)
		reqURIwithQueryRaw, _ = url.QueryUnescape(c.Request.URL.RequestURI())
		reqURI                = path.Clean(reqURIRaw)
		reqURIwithQuery       = path.Clean(reqURIwithQueryRaw)
		filename              = path.Base(reqURI)
	)

	log.Debugf("Incoming connection from %s %s", c.ClientIP(), reqURIwithQuery)

	// 首先检查是否为图片文件
	if !isImageFile(filename) {
		log.Infof("Non-image file requested: %s", reqURI)
		handleNonImageFile(c, reqURI)
		return
	}

	// 检查文件类型是否允许
	if !helper.CheckAllowedType(filename) {
		msg := "File extension not allowed! " + filename
		log.Warn(msg)
		c.String(400, msg)
		return
	}

	// 解析额外参数
	extraParams := parseExtraParams(c)

	// 检查路径是否匹配 IMG_MAP 中的任何前缀
	matchedPrefix, matchedTarget := findMatchingPrefix(reqURI)
	if matchedPrefix == "" {
		log.Warnf("请求的路径不匹配任何配置的 IMG_MAP: %s", c.Request.URL.Path)
		c.Status(404)
		return
	}

	// 构建 EXHAUST_PATH 中的文件路径
	exhaustFilename := buildExhaustFilename(reqURI, extraParams)

	// 检查文件是否已经在 EXHAUST_PATH 中
	if helper.FileExists(exhaustFilename) {
		if info, err := os.Stat(exhaustFilename); err == nil && info.Size() > 0 {
			log.Infof("文件已存在于 EXHAUST_PATH，直接提供服务: %s", exhaustFilename)
			c.File(exhaustFilename)
			return
		}
		// 如果文件存在但大小为0，删除它并重新处理
		os.Remove(exhaustFilename)
	}

	// 处理图像
	isLocalPath := strings.HasPrefix(matchedTarget, "./") || strings.HasPrefix(matchedTarget, "/")
	if isLocalPath {
		handleLocalImage(c, matchedTarget, reqURI, exhaustFilename, extraParams)
	} else {
		handleRemoteImage(c, matchedTarget, matchedPrefix, reqURIwithQuery, exhaustFilename, extraParams)
	}
}

func handleNonImageFile(c *gin.Context, reqURI string) {
	var redirectURL string

	for prefix, target := range config.Config.ImageMap {
		if strings.HasPrefix(reqURI, prefix) {
			if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
				redirectURL = target + strings.TrimPrefix(reqURI, prefix)
			} else {
				localPath := path.Join(target, strings.TrimPrefix(reqURI, prefix))
				c.File(localPath)
				return
			}
			break
		}
	}

	if redirectURL == "" {
		localPath := path.Join(config.Config.ImgPath, reqURI)
		if helper.FileExists(localPath) {
			c.File(localPath)
			return
		} else {
			c.Status(404)
			return
		}
	}

	log.Infof("Redirecting to: %s", redirectURL)
	c.Redirect(302, redirectURL)
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

func parseRequestURI(c *gin.Context) (string, string) {
	reqURIRaw, _ := url.QueryUnescape(c.Request.URL.Path)
	reqURIwithQueryRaw, _ := url.QueryUnescape(c.Request.URL.RequestURI())
	return path.Clean(reqURIRaw), path.Clean(reqURIwithQueryRaw)
}

func parseExtraParams(c *gin.Context) config.ExtraParams {
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

func buildRealRemoteAddr(targetUrl *url.URL, matchedPrefix, reqURIwithQuery string) string {
	targetHost := targetUrl.Scheme + "://" + targetUrl.Host
	reqURIwithQuery = strings.Replace(reqURIwithQuery, matchedPrefix, targetUrl.Path, 1)
	if strings.HasSuffix(targetUrl.Path, "/") {
		reqURIwithQuery = strings.TrimPrefix(reqURIwithQuery, "/")
	}
	return targetHost + reqURIwithQuery
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

func handleLocalImage(c *gin.Context, matchedTarget, reqURI, exhaustFilename string, extraParams config.ExtraParams) {
	rawImageAbs := path.Join(matchedTarget, reqURI)

	if !helper.FileExists(rawImageAbs) {
		c.String(404, "本地文件不存在")
		return
	}

	err := processAndSaveImage(c, rawImageAbs, exhaustFilename, extraParams)
	if err != nil {
		log.Error(err)
		c.String(500, "处理图像时出错")
		return
	}
}

func handleRemoteImage(c *gin.Context, matchedTarget, matchedPrefix, reqURIwithQuery, exhaustFilename string, extraParams config.ExtraParams) {
	targetUrl, err := url.Parse(matchedTarget)
	if err != nil {
		log.Errorf("解析目标 URL 失败: %v", err)
		c.String(500, "服务器配置错误")
		return
	}

	realRemoteAddr := buildRealRemoteAddr(targetUrl, matchedPrefix, reqURIwithQuery)

	rawImageAbs, isNewDownload, err := fetchRemoteImg(realRemoteAddr, targetUrl.Host)
	if err != nil {
		log.Errorf("获取远程图像失败: %v", err)
		c.String(500, "无法获取远程图像")
		return
	}

	err = processAndSaveImage(c, rawImageAbs, exhaustFilename, extraParams)
	if err != nil {
		log.Error(err)
		c.String(500, "处理图像时出错")
		return
	}

	if isNewDownload {
		go schedule.ScheduleCleanup(rawImageAbs)
	}
}

func processAndSaveImage(c *gin.Context, rawImageAbs, exhaustFilename string, extraParams config.ExtraParams) error {
	// 获取文件锁
	lock := getFileLock(exhaustFilename)
	lock.Lock()
	defer lock.Unlock()

	// 再次检查文件是否存在
	if helper.FileExists(exhaustFilename) {
		if info, err := os.Stat(exhaustFilename); err == nil && info.Size() > 0 {
			c.File(exhaustFilename)
			return nil
		}
		os.Remove(exhaustFilename)
	}

	isSmall, err := helper.IsFileSizeSmall(rawImageAbs, 30*1024)
	if err != nil {
		return fmt.Errorf("检查文件大小时出错: %v", err)
	}

	// 确保目标目录存在
	if err := os.MkdirAll(path.Dir(exhaustFilename), 0755); err != nil {
		return fmt.Errorf("创建目标目录失败: %v", err)
	}

	// 使用临时文件
	tempFile := exhaustFilename + ".tmp"
	defer os.Remove(tempFile)

	if isSmall {
		if err := helper.CopyFile(rawImageAbs, tempFile); err != nil {
			return fmt.Errorf("复制小文件失败: %v", err)
		}
	} else {
		err := encoder.ProcessAndSaveImage(rawImageAbs, tempFile, extraParams)
		if err != nil {
			log.Warnf("处理图片失败，将直接复制原图: %v", err)
			if copyErr := helper.CopyFile(rawImageAbs, tempFile); copyErr != nil {
				return fmt.Errorf("复制原图失败: %v", copyErr)
			}
		}
	}

	// 验证临时文件
	if info, err := os.Stat(tempFile); err != nil || info.Size() == 0 {
		return fmt.Errorf("处理后的文件无效")
	}

	// 原子性地将临时文件重命名为目标文件
	if err := os.Rename(tempFile, exhaustFilename); err != nil {
		return fmt.Errorf("重命名临时文件失败: %v", err)
	}

	c.File(exhaustFilename)
	return nil
}
