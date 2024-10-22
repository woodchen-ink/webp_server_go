package handler

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"time"
	"webp_server_go/config"
	"webp_server_go/helper"

	"github.com/gofiber/fiber/v2"
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

func downloadFile(filepath string, url string) error {
	resp, err := http.Get(url)
	if err != nil {
		log.Errorf("下载文件时连接到远程错误！上游链接: %s, 错误: %v", url, err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != fiber.StatusOK {
		log.Errorf("获取远程图像失败。上游链接: %s, 状态码: %s", url, resp.Status)
		return fmt.Errorf("意外的状态: %s, 上游链接: %s", resp.Status, url)
	}

	// 创建目标文件
	err = os.MkdirAll(path.Dir(filepath), 0755)
	if err != nil {
		log.Errorf("创建目标目录失败。路径: %s, 错误: %v", path.Dir(filepath), err)
		return err
	}

	out, err := os.Create(filepath)
	if err != nil {
		log.Errorf("创建目标文件失败。文件路径: %s, 错误: %v", filepath, err)
		return err
	}
	defer out.Close()

	// 使用小缓冲区流式写入文件
	buf := make([]byte, 32*1024)
	_, err = io.CopyBuffer(out, resp.Body, buf)
	if err != nil {
		log.Errorf("写入文件失败。文件路径: %s, 上游链接: %s, 错误: %v", filepath, url, err)
		return err
	}

	log.Infof("文件下载成功。上游链接: %s, 保存路径: %s", url, filepath)
	return nil
}

func fetchRemoteImg(url, subdir string) (string, bool, error) {
	log.Infof("正在获取远程图像: %s", url)

	fileName := helper.HashString(url)
	localRawImagePath := path.Join(config.Config.RemoteRawPath, subdir, fileName)

	if helper.FileExists(localRawImagePath) {
		log.Infof("远程图像已存在于本地: %s", localRawImagePath)
		return localRawImagePath, false, nil
	}

	err := downloadFile(localRawImagePath, url)
	if err != nil {
		log.Errorf("下载远程图像失败: %v", err)
		return "", false, err
	}

	log.Infof("成功获取远程图像: %s", localRawImagePath)
	return localRawImagePath, true, nil
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

func streamFile(c *fiber.Ctx, filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		log.Errorf("无法打开文件: %s, 错误: %v", filePath, err)
		return c.Status(fiber.StatusInternalServerError).SendString("无法打开文件")
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		log.Errorf("无法获取文件信息: %s, 错误: %v", filePath, err)
		return c.Status(fiber.StatusInternalServerError).SendString("无法获取文件信息")
	}

	contentType := mime.TypeByExtension(path.Ext(filePath))
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	c.Set(fiber.HeaderContentType, contentType)
	c.Set(fiber.HeaderContentLength, strconv.FormatInt(stat.Size(), 10))

	log.Infof("开始流式传输文件: %s, 大小: %d bytes", filePath, stat.Size())

	err = c.SendStream(file)
	if err != nil {
		log.Errorf("文件流式传输失败: %s, 错误: %v", filePath, err)
		return err
	}

	log.Infof("文件流式传输完成: %s", filePath)
	return nil
}
