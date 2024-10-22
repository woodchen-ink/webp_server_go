package handler

import (
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"webp_server_go/config"
	"webp_server_go/encoder"
	"webp_server_go/helper"

	"github.com/gofiber/fiber/v2"
	log "github.com/sirupsen/logrus"
)

func Convert(c *fiber.Ctx) error {
	// this function need to do:
	// 1. get request path, query string
	// 2. generate rawImagePath, could be local path or remote url(possible with query string)
	// 3. pass it to encoder, get the result, send it back

	// normal http request will start with /
	// 检查路径是否以 "/" 开头
	if !strings.HasPrefix(c.Path(), "/") {
		return c.SendStatus(http.StatusBadRequest)
	}

	// 处理根路径请求
	if c.Path() == "/" {
		return c.SendString("Welcome to CZL WebP Server")
	}

	var (
		reqHostname = c.Hostname()
		reqHost     = c.Protocol() + "://" + reqHostname // http://www.example.com:8000
		reqHeader   = &c.Request().Header

		reqURIRaw, _          = url.QueryUnescape(c.Path())        // /mypic/123.jpg
		reqURIwithQueryRaw, _ = url.QueryUnescape(c.OriginalURL()) // /mypic/123.jpg?someother=200&somebugs=200
		reqURI                = path.Clean(reqURIRaw)              // delete ../ in reqURI to mitigate directory traversal
		reqURIwithQuery       = path.Clean(reqURIwithQueryRaw)     // Sometimes reqURIwithQuery can be https://example.tld/mypic/123.jpg?someother=200&somebugs=200, we need to extract it

		filename       = path.Base(reqURI)
		realRemoteAddr = ""
		targetHostName = config.LocalHostAlias
		targetHost     = config.Config.ImgPath
		proxyMode      = config.ProxyMode
		mapMode        = false

		width, _     = strconv.Atoi(c.Query("width"))      // Extra Params
		height, _    = strconv.Atoi(c.Query("height"))     // Extra Params
		maxHeight, _ = strconv.Atoi(c.Query("max_height")) // Extra Params
		maxWidth, _  = strconv.Atoi(c.Query("max_width"))  // Extra Params
		extraParams  = config.ExtraParams{
			Width:     width,
			Height:    height,
			MaxWidth:  maxWidth,
			MaxHeight: maxHeight,
		}
	)

	log.Debugf("Incoming connection from %s %s %s", c.IP(), reqHostname, reqURIwithQuery)

	var rawImageAbs string
	var metadata = config.MetaFile{}

	// 	非图片清况下302到源文件
	if !isImageFile(filename) {
		log.Infof("Non-image file requested: %s", reqURI)
		var redirectURL string

		// 检查是否存在匹配的 IMG_MAP
		for prefix, target := range config.Config.ImageMap {
			if strings.HasPrefix(reqURI, prefix) {
				// 检查目标是否为远程资源
				if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
					// 远程资源，构造重定向 URL
					redirectURL = target + strings.TrimPrefix(reqURI, prefix)
				} else {
					// 本地资源，按原逻辑处理
					return c.SendFile(path.Join(target, strings.TrimPrefix(reqURI, prefix)))
				}
				break
			}
		}

		// 如果没有找到匹配的 IMG_MAP，或者是本地资源，使用默认的处理方式
		if redirectURL == "" {
			if proxyMode {
				redirectURL = realRemoteAddr
			} else {
				// 本地资源，按原逻辑处理
				localPath := path.Join(config.Config.ImgPath, reqURI)
				if helper.FileExists(localPath) {
					return c.SendFile(localPath)
				} else {
					return c.SendStatus(fiber.StatusNotFound)
				}
			}
		}

		// 只有在确定需要重定向时才执行重定向
		if redirectURL != "" {
			log.Infof("Redirecting to: %s", redirectURL)
			return c.Redirect(redirectURL, fiber.StatusFound)
		}
	}

	// 新增：检查是否为WebP格式
	// if strings.ToLower(path.Ext(filename)) == ".webp" {
	// 	log.Infof("原始图像已经是WebP格式: %s", reqURI)
	// 	var webpImagePath string
	// 	if proxyMode {
	// 		// 对于代理模式，确保文件已经被下载
	// 		metadata = fetchRemoteImg(realRemoteAddr, targetHostName)
	// 		webpImagePath = path.Join(config.Config.RemoteRawPath, targetHostName, metadata.Id)
	// 	} else {
	// 		webpImagePath = path.Join(config.Config.ImgPath, reqURI)
	// 	}

	// 	// 检查文件是否存在
	// 	if helper.FileExists(webpImagePath) {
	// 		// 直接返回原WebP图片
	// 		return c.SendFile(webpImagePath)
	// 	}
	// }

	if !helper.CheckAllowedType(filename) {
		msg := "不允许的文件扩展名 " + filename
		log.Warn(msg)
		c.Status(http.StatusBadRequest)
		_ = c.Send([]byte(msg))
		return nil
	}

	// Rewrite the target backend if a mapping rule matches the hostname
	if hostMap, hostMapFound := config.Config.ImageMap[reqHost]; hostMapFound {
		log.Debugf("找到host映射 %s -> %s", reqHostname, hostMap)
		targetHostUrl, _ := url.Parse(hostMap)
		targetHostName = targetHostUrl.Host
		targetHost = targetHostUrl.Scheme + "://" + targetHostUrl.Host
		proxyMode = true
	} else {
		// There's not matching host mapping, now check for any URI map that apply
		httpRegexpMatcher := regexp.MustCompile(config.HttpRegexp)
		for uriMap, uriMapTarget := range config.Config.ImageMap {
			if strings.HasPrefix(reqURI, uriMap) {
				log.Debugf("找到 URI 映射 %s -> %s", uriMap, uriMapTarget)
				mapMode = true

				// if uriMapTarget we use the proxy mode to fetch the remote
				if httpRegexpMatcher.Match([]byte(uriMapTarget)) {
					targetHostUrl, _ := url.Parse(uriMapTarget)
					targetHostName = targetHostUrl.Host
					targetHost = targetHostUrl.Scheme + "://" + targetHostUrl.Host
					reqURI = strings.Replace(reqURI, uriMap, targetHostUrl.Path, 1)
					reqURIwithQuery = strings.Replace(reqURIwithQuery, uriMap, targetHostUrl.Path, 1)
					proxyMode = true
				} else {
					reqURI = strings.Replace(reqURI, uriMap, uriMapTarget, 1)
					reqURIwithQuery = strings.Replace(reqURIwithQuery, uriMap, uriMapTarget, 1)
				}
				break
			}
		}

	}

	if proxyMode {

		if !mapMode {
			// Don't deal with the encoding to avoid upstream compatibilities
			reqURI = c.Path()
			reqURIwithQuery = c.OriginalURL()
		}

		log.Tracef("reqURIwithQuery is %s", reqURIwithQuery)

		// Replace host in the URL
		// realRemoteAddr = strings.Replace(reqURIwithQuery, reqHost, targetHost, 1)
		realRemoteAddr = targetHost + reqURIwithQuery
		log.Debugf("realRemoteAddr is %s", realRemoteAddr)
	}

	if proxyMode {
		// 这是 proxyMode，我们必须使用这个 url 来下载并将其保存到本地路径，这也为我们提供了 rawImageAbs
		// https://test.webp.sh/mypic/123.jpg?someother=200&somebugs=200

		metadata = fetchRemoteImg(realRemoteAddr, targetHostName)
		rawImageAbs = path.Join(config.Config.RemoteRawPath, targetHostName, metadata.Id)
	} else {
		// not proxyMode, we'll use local path
		metadata = helper.ReadMetadata(reqURIwithQuery, "", targetHostName)
		if !mapMode {
			// by default images are hosted in ImgPath
			rawImageAbs = path.Join(config.Config.ImgPath, reqURI)
		} else {
			rawImageAbs = reqURI
		}
		// detect if source file has changed
		if metadata.Checksum != helper.HashFile(rawImageAbs) {
			log.Info("源文件已更改，重新编码...")
			helper.WriteMetadata(reqURIwithQuery, "", targetHostName)
			cleanProxyCache(path.Join(config.Config.ExhaustPath, targetHostName, metadata.Id))
		}
	}

	supportedFormats := helper.GuessSupportedFormat(reqHeader)
	// resize itself and return if only raw(original format) is supported
	if supportedFormats["raw"] == true &&
		supportedFormats["webp"] == false &&
		supportedFormats["avif"] == false &&
		supportedFormats["jxl"] == false {
		dest := path.Join(config.Config.ExhaustPath, targetHostName, metadata.Id)
		if !helper.ImageExists(dest) {
			encoder.ResizeItself(rawImageAbs, dest, extraParams)
		}
		return c.SendFile(dest)
	}

	// Check the original image for existence,
	if !helper.ImageExists(rawImageAbs) {
		helper.DeleteMetadata(reqURIwithQuery, targetHostName)
		msg := "Image not found!"
		_ = c.Send([]byte(msg))
		log.Warn(msg)
		_ = c.SendStatus(404)
		return nil
	}

	avifAbs, webpAbs, jxlAbs := helper.GenOptimizedAbsPath(metadata, targetHostName)
	// Do the convertion based on supported formats and config
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

	finalFilename := helper.FindSmallestFiles(availableFiles)
	contentType := helper.GetFileContentType(finalFilename)
	c.Set("Content-Type", contentType)

	c.Set("X-Compression-Rate", helper.GetCompressionRate(rawImageAbs, finalFilename))
	return c.SendFile(finalFilename)
}

// 新增：检查文件是否为图片的辅助函数
func isImageFile(filename string) bool {
	ext := strings.ToLower(path.Ext(filename))
	if ext == "" {
		return false
	}
	ext = ext[1:] // 移除开头的点

	allowedTypes := config.Config.AllowedTypes
	if len(allowedTypes) == 1 && allowedTypes[0] == "*" {
		// 如果允许所有类型，则使用默认的图片类型列表
		allowedTypes = config.NewWebPConfig().AllowedTypes
	}

	return slices.Contains(allowedTypes, ext)
}
