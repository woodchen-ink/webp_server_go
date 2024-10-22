package encoder

import (
	"os"
	"path"
	"runtime"
	"strings"
	"sync"
	"time"
	"webp_server_go/config"
	"webp_server_go/helper"

	"github.com/davidbyttow/govips/v2/vips"
	log "github.com/sirupsen/logrus"
)

var (
	boolFalse   vips.BoolParameter
	intMinusOne vips.IntParameter
	// Source image encoder ignore list for WebP and AVIF
	// We shouldn't convert Unknown and AVIF to WebP
	webpIgnore = []vips.ImageType{vips.ImageTypeUnknown, vips.ImageTypeAVIF}
	// We shouldn't convert Unknown,AVIF and GIF to AVIF
	avifIgnore = append(webpIgnore, vips.ImageTypeGIF)
)

func init() {
	vips.LoggingSettings(nil, vips.LogLevelError)
	vips.Startup(&vips.Config{
		ConcurrencyLevel: runtime.NumCPU(),
	})
	boolFalse.Set(false)
	intMinusOne.Set(-1)
}

func ConvertFilter(rawPath, jxlPath, avifPath, webpPath string, extraParams config.ExtraParams, supportedFormats map[string]bool, c chan int) {
	// Wait for the conversion to complete and return the converted image
	retryDelay := 100 * time.Millisecond // Initial retry delay

	for {
		if _, found := config.ConvertLock.Get(rawPath); found {
			log.Debugf("文件 %s 在转换过程中被锁定，请在 %s 后重试", rawPath, retryDelay)
			time.Sleep(retryDelay)
		} else {
			// The lock is released, indicating that the conversion is complete
			break
		}
	}

	// If there is a lock here, it means that another thread is converting the same image
	// Lock rawPath to prevent concurrent conversion
	config.ConvertLock.Set(rawPath, true, -1)
	defer config.ConvertLock.Delete(rawPath)

	var wg sync.WaitGroup
	wg.Add(3)
	if !helper.ImageExists(avifPath) && config.Config.EnableAVIF && supportedFormats["avif"] {
		go func() {
			err := convertImage(rawPath, avifPath, "avif", extraParams)
			if err != nil {
				log.Errorln(err)
			}
			defer wg.Done()
		}()
	} else {
		wg.Done()
	}

	if !helper.ImageExists(webpPath) && config.Config.EnableWebP && supportedFormats["webp"] {
		go func() {
			err := convertImage(rawPath, webpPath, "webp", extraParams)
			if err != nil {
				log.Errorln(err)
			}
			defer wg.Done()
		}()
	} else {
		wg.Done()
	}

	if !helper.ImageExists(jxlPath) && config.Config.EnableJXL && supportedFormats["jxl"] {
		go func() {
			err := convertImage(rawPath, jxlPath, "jxl", extraParams)
			if err != nil {
				log.Errorln(err)
			}
			defer wg.Done()
		}()
	} else {
		wg.Done()
	}

	wg.Wait()

	if c != nil {
		c <- 1
	}
}

func convertImage(rawPath, optimizedPath, imageType string, extraParams config.ExtraParams) error {
	// 创建目标目录
	err := os.MkdirAll(path.Dir(optimizedPath), 0755)
	if err != nil {
		log.Error(err.Error())
		return err
	}

	// 如果原始图像是 NEF 格式，先转换为 JPG
	if strings.HasSuffix(strings.ToLower(rawPath), ".nef") {
		convertedRaw, converted := ConvertRawToJPG(rawPath, optimizedPath)
		if converted {
			rawPath = convertedRaw
			defer func() {
				log.Infoln("移除中间转换文件:", convertedRaw)
				if err := os.Remove(convertedRaw); err != nil {
					log.Warnln("删除转换文件失败", err)
				}
			}()
		}
	}

	// 打开图像
	img, err := vips.LoadImageFromFile(rawPath, &vips.ImportParams{
		FailOnError: boolFalse,
		NumPages:    intMinusOne,
	})
	if err != nil {
		log.Warnf("无法打开源图像: %v", err)
		return err
	}
	defer img.Close()

	// 预处理图像（自动旋转、调整大小等）
	shouldCopyOriginal, err := preProcessImage(img, imageType, extraParams)
	if err != nil {
		log.Warnf("无法预处理源图像: %v", err)
		if shouldCopyOriginal {
			log.Infof("由于预处理错误，将复制原图")
			return helper.CopyFile(rawPath, optimizedPath)
		}
		return err
	}

	// 根据图像类型进行编码
	var encoderErr error
	switch imageType {
	case "webp":
		encoderErr = webpEncoder(img, rawPath, optimizedPath)
	case "avif":
		encoderErr = avifEncoder(img, rawPath, optimizedPath)
	case "jxl":
		encoderErr = jxlEncoder(img, rawPath, optimizedPath)
	}

	if encoderErr != nil {
		log.Warnf("图像编码失败: %v", encoderErr)
		// 如果编码失败，我们也复制原图
		return helper.CopyFile(rawPath, optimizedPath)
	}

	// 比较转换后的文件大小
	originalInfo, err := os.Stat(rawPath)
	if err != nil {
		log.Errorf("获取原图文件信息失败: %v", err)
		return err
	}
	convertedInfo, err := os.Stat(optimizedPath)
	if err != nil {
		log.Errorf("获取转换后文件信息失败: %v", err)
		return err
	}

	if convertedInfo.Size() > originalInfo.Size() {
		log.Infof("转换后的图片大于原图，使用原图: %s", rawPath)
		// 删除转换后的大文件
		if err := os.Remove(optimizedPath); err != nil {
			log.Warnf("删除大的转换文件失败: %v", err)
		}
		// 将原图复制到目标路径
		return helper.CopyFile(rawPath, optimizedPath)
	}

	log.Infof("图像处理成功: 目标文件=%s", optimizedPath)
	return nil
}

func jxlEncoder(img *vips.ImageRef, rawPath string, optimizedPath string) error {
	var (
		buf     []byte
		quality = config.Config.Quality
		err     error
	)

	// If quality >= 100, we use lossless mode
	if quality >= 100 {
		buf, _, err = img.ExportJxl(&vips.JxlExportParams{
			Effort:   1,
			Tier:     4,
			Lossless: true,
			Distance: 1.0,
		})
	} else {
		buf, _, err = img.ExportJxl(&vips.JxlExportParams{
			Effort:   1,
			Tier:     4,
			Quality:  quality,
			Lossless: false,
			Distance: 1.0,
		})
	}

	if err != nil {
		log.Warnf("Can't encode source image: %v to JXL", err)
		return err
	}

	if err := os.WriteFile(optimizedPath, buf, 0600); err != nil {
		log.Error(err)
		return err
	}

	convertLog("JXL", rawPath, optimizedPath, quality)
	return nil
}

func avifEncoder(img *vips.ImageRef, rawPath string, optimizedPath string) error {
	var (
		buf     []byte
		quality = config.Config.Quality
		err     error
	)

	// If quality >= 100, we use lossless mode
	if quality >= 100 {
		buf, _, err = img.ExportAvif(&vips.AvifExportParams{
			Lossless:      true,
			StripMetadata: config.Config.StripMetadata,
		})
	} else {
		buf, _, err = img.ExportAvif(&vips.AvifExportParams{
			Quality:       quality,
			Lossless:      false,
			StripMetadata: config.Config.StripMetadata,
		})
	}

	if err != nil {
		log.Warnf("无法将源图像：%v 编码为 AVIF", err)
		return err
	}

	if err := os.WriteFile(optimizedPath, buf, 0600); err != nil {
		log.Error(err)
		return err
	}

	convertLog("AVIF", rawPath, optimizedPath, quality)
	return nil
}

func webpEncoder(img *vips.ImageRef, rawPath string, optimizedPath string) error {
	var (
		buf     []byte
		quality = config.Config.Quality
		err     error
	)

	// If quality >= 100, we use lossless mode
	if quality >= 100 {
		// Lossless mode will not encounter problems as below, because in libvips as code below
		// 	config.method = ExUtilGetInt(argv[++c], 0, &parse_error);
		//   use_lossless_preset = 0;   // disable -z option
		buf, _, err = img.ExportWebp(&vips.WebpExportParams{
			Lossless:      true,
			StripMetadata: config.Config.StripMetadata,
		})
	} else {
		// If some special images cannot encode with default ReductionEffort(0), then retry from 0 to 6
		// Example: https://github.com/webp-sh/webp_server_go/issues/234
		ep := vips.WebpExportParams{
			Quality:       quality,
			Lossless:      false,
			StripMetadata: config.Config.StripMetadata,
		}
		for i := range 7 {
			ep.ReductionEffort = i
			buf, _, err = img.ExportWebp(&ep)
			if err != nil && strings.Contains(err.Error(), "unable to encode") {
				log.Warnf("无法使用 ReductionEffort %d 将图像编码为 WebP，请尝试更高的值...", i)
			} else if err != nil {
				log.Warnf("无法将源图像编码为 WebP：%v", err)
			} else {
				break
			}
		}
		buf, _, err = img.ExportWebp(&ep)
	}

	if err != nil {
		log.Warnf("无法将源图像：%v 编码为 WebP", err)
		return err
	}

	if err := os.WriteFile(optimizedPath, buf, 0600); err != nil {
		log.Error(err)
		return err
	}

	convertLog("WebP", rawPath, optimizedPath, quality)
	return nil
}

func convertLog(itype, rawPath string, optimizedPath string, quality int) {
	oldf, err := os.Stat(rawPath)
	if err != nil {
		log.Error(err)
		return
	}

	newf, err := os.Stat(optimizedPath)
	if err != nil {
		log.Error(err)
		return
	}

	// 计算压缩率
	deflateRate := float32(newf.Size()) / float32(oldf.Size()) * 100

	// 记录转换信息
	log.Infof("图像转换: 类型=%s, 质量=%d%%", itype, quality)
	log.Infof("文件路径: 原始=%s, 优化=%s", rawPath, optimizedPath)
	log.Infof("文件大小: 原始=%d字节, 优化=%d字节, 压缩率=%.2f%%", oldf.Size(), newf.Size(), deflateRate)
}
