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
	// we need to create dir first
	var err = os.MkdirAll(path.Dir(optimizedPath), 0755)
	if err != nil {
		log.Error(err.Error())
	}
	// If original image is NEF, convert NEF image to JPG first
	if strings.HasSuffix(strings.ToLower(rawPath), ".nef") {
		var convertedRaw, converted = ConvertRawToJPG(rawPath, optimizedPath)
		// If converted, use converted file as raw
		if converted {
			// Use converted file(JPG) as raw input for further conversion
			rawPath = convertedRaw
			// Remove converted file after conversion
			defer func() {
				log.Infoln("Removing intermediate conversion file:", convertedRaw)
				err := os.Remove(convertedRaw)
				if err != nil {
					log.Warnln("failed to delete converted file", err)
				}
			}()
		}
	}

	// Image is only opened here
	img, err := vips.LoadImageFromFile(rawPath, &vips.ImportParams{
		FailOnError: boolFalse,
		NumPages:    intMinusOne,
	})
	if err != nil {
		log.Warnf("Can't open source image: %v", err)
		return err
	}
	defer img.Close()

	// Pre-process image(auto rotate, resize, etc.)
	err = preProcessImage(img, imageType, extraParams)
	if err != nil {
		log.Warnf("Can't pre-process source image: %v", err)
		return err
	}

	switch imageType {
	case "webp":
		err = webpEncoder(img, rawPath, optimizedPath)
	case "avif":
		err = avifEncoder(img, rawPath, optimizedPath)
	case "jxl":
		err = jxlEncoder(img, rawPath, optimizedPath)
	}

	return err
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
