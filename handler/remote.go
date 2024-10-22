package handler

import (
	"bytes"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"webp_server_go/config"
	"webp_server_go/helper"

	"github.com/gofiber/fiber/v2"
	"github.com/h2non/filetype"
	"github.com/patrickmn/go-cache"
	log "github.com/sirupsen/logrus"
)

// Given /path/to/node.png
// Delete /path/to/node.png*
func cleanProxyCache(cacheImagePath string) {
	// Delete /node.png*
	files, err := filepath.Glob(cacheImagePath + "*")
	if err != nil {
		log.Infoln(err)
	}
	for _, f := range files {
		if err := os.Remove(f); err != nil {
			log.Info(err)
		}
	}
}

func downloadFile(filepath string, url string) {
	resp, err := http.Get(url)
	if err != nil {
		log.Errorln("下载文件时连接到远程错误！")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != fiber.StatusOK {
		log.Errorf("获取远程图像时远程返回 %s", resp.Status)
		return
	}

	// Copy bytes here
	bodyBytes := new(bytes.Buffer)
	_, err = bodyBytes.ReadFrom(resp.Body)
	if err != nil {
		return
	}

	// Check if remote content-type is image using check by filetype instead of content-type returned by origin
	kind, _ := filetype.Match(bodyBytes.Bytes())
	mime := kind.MIME.Value
	if !strings.Contains(mime, "image") {
		log.Errorf("远程文件 %s 不是图像，远程内容的 MIME 类型为 %s", url, mime)
		return
	}

	_ = os.MkdirAll(path.Dir(filepath), 0755)

	// Create Cache here as a lock, so we can prevent incomplete file from being read
	// Key: filepath, Value: true
	config.WriteLock.Set(filepath, true, -1)

	err = os.WriteFile(filepath, bodyBytes.Bytes(), 0600)
	if err != nil {
		// not likely to happen
		return
	}

	// Delete lock here
	config.WriteLock.Delete(filepath)

}

func fetchRemoteImg(url string, subdir string) config.MetaFile {
	cacheKey := subdir + ":" + helper.HashString(url)

	var metadata config.MetaFile
	var etag string
	var size int64
	var lastModified time.Time

	if cachedETag, found := config.RemoteCache.Get(cacheKey); found {
		etag = cachedETag.(string)
		log.Infof("使用缓存的 ETag 进行远程地址: %s", url)
	} else {
		log.Infof("远程地址是 %s，正在 ping 获取信息...", url)
		etag, size, lastModified = pingURL(url)
		if etag != "" {
			config.RemoteCache.Set(cacheKey, etag, cache.DefaultExpiration)
		}
	}

	metadata = helper.ReadMetadata(url, etag, subdir)
	localRawImagePath := path.Join(config.Config.RemoteRawPath, subdir, metadata.Id)
	localExhaustImagePath := path.Join(config.Config.ExhaustPath, subdir, metadata.Id)

	needUpdate := false
	if !helper.ImageExists(localRawImagePath) {
		log.Info("在远程原始文件中找不到远程文件，正在获取...")
		needUpdate = true
	} else {
		localFileInfo, err := os.Stat(localRawImagePath)
		if err == nil {
			if size > 0 && size != localFileInfo.Size() {
				log.Info("文件大小已更改，正在更新...")
				needUpdate = true
			} else if !lastModified.IsZero() && lastModified.After(localFileInfo.ModTime()) {
				log.Info("远程文件较新，正在更新...")
				needUpdate = true
			} else if metadata.Checksum != helper.HashString(etag) {
				log.Info("ETag 已更改，正在更新...")
				needUpdate = true
			}
		} else {
			log.Warnf("检查本地文件时出错: %v", err)
			needUpdate = true
		}
	}

	if needUpdate {
		cleanProxyCache(localExhaustImagePath)
		helper.DeleteMetadata(url, subdir)
		helper.WriteMetadata(url, etag, subdir)
		downloadFile(localRawImagePath, url)
		// 重新读取更新后的元数据
		metadata = helper.ReadMetadata(url, etag, subdir)
	}

	return metadata
}

func pingURL(url string) (string, int64, time.Time) {
	var etag string
	var size int64
	var lastModified time.Time

	resp, err := http.Head(url)
	if err != nil {
		log.Errorf("pingUrl 时连接到远程错误: %v", err)
		return "", 0, time.Time{}
	}
	defer resp.Body.Close()

	if resp.StatusCode == fiber.StatusOK {
		etag = resp.Header.Get("ETag")
		sizeStr := resp.Header.Get("Content-Length")
		size, _ = strconv.ParseInt(sizeStr, 10, 64)
		lastModifiedStr := resp.Header.Get("Last-Modified")
		lastModified, _ = time.Parse(time.RFC1123, lastModifiedStr)

		if etag == "" {
			log.Warn("远程未在标头中返回 ETag，使用 Last-Modified（如果可用）")
			etag = lastModifiedStr
		}

		if etag == "" && lastModified.IsZero() {
			log.Warn("ETag 和 Last-Modified 都不可用，使用 Content-Length 作为后备")
			etag = sizeStr
		}
	} else {
		log.Warnf("意外的状态代码: %d 当 ping URL 时: %s", resp.StatusCode, url)
	}

	return etag, size, lastModified
}
