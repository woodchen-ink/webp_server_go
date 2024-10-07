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
		log.Errorln("Connection to remote error when downloadFile!")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != fiber.StatusOK {
		log.Errorf("remote returned %s when fetching remote image", resp.Status)
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
		log.Errorf("remote file %s is not image, remote content has MIME type of %s", url, mime)
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
		log.Infof("Using cached ETag for remote addr: %s", url)
	} else {
		log.Infof("Remote Addr is %s, pinging for info...", url)
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
		log.Info("Remote file not found in remote-raw, fetching...")
		needUpdate = true
	} else {
		localFileInfo, err := os.Stat(localRawImagePath)
		if err == nil {
			if size > 0 && size != localFileInfo.Size() {
				log.Info("File size changed, updating...")
				needUpdate = true
			} else if !lastModified.IsZero() && lastModified.After(localFileInfo.ModTime()) {
				log.Info("Remote file is newer, updating...")
				needUpdate = true
			} else if metadata.Checksum != helper.HashString(etag) {
				log.Info("ETag changed, updating...")
				needUpdate = true
			}
		} else {
			log.Warnf("Error checking local file: %v", err)
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
		log.Errorf("Connection to remote error when pingUrl: %v", err)
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
			log.Warn("Remote didn't return ETag in header, using Last-Modified if available")
			etag = lastModifiedStr
		}

		if etag == "" && lastModified.IsZero() {
			log.Warn("Neither ETag nor Last-Modified available, using Content-Length as fallback")
			etag = sizeStr
		}
	} else {
		log.Warnf("Unexpected status code: %d when pinging URL: %s", resp.StatusCode, url)
	}

	return etag, size, lastModified
}
