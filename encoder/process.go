package encoder

import (
	"errors"
	"os"
	"path"
	"slices"
	"strings"
	"webp_server_go/config"

	"github.com/davidbyttow/govips/v2/vips"
	log "github.com/sirupsen/logrus"
)

func resizeImage(img *vips.ImageRef, extraParams config.ExtraParams) error {
	imageHeight := img.Height()
	imageWidth := img.Width()

	imgHeightWidthRatio := float32(imageHeight) / float32(imageWidth)

	//这里我们有宽度、高度和 max_width、max_height
	//两对不能同时使用

	//max_height 和 max_width 用于确保将更大的图像调整为 max_height 和 max_width
	//例如，max_width=200,max_height=100 的 500x500px 图像将调整为 100x100
	//而较小的图像则保持不变

	//如果两者都使用，我们将使用宽度和高度

	if extraParams.MaxHeight > 0 && extraParams.MaxWidth > 0 {
		// If any of it exceeds
		if imageHeight > extraParams.MaxHeight || imageWidth > extraParams.MaxWidth {
			// Check which dimension exceeds most
			heightExceedRatio := float32(imageHeight) / float32(extraParams.MaxHeight)
			widthExceedRatio := float32(imageWidth) / float32(extraParams.MaxWidth)
			// 如果高度超过更多，例如 500x500 -> 200x100 (2.5 < 5)
			// 以max_height为新高度，调整大小并保留比例
			if heightExceedRatio > widthExceedRatio {
				err := img.Thumbnail(int(float32(extraParams.MaxHeight)/imgHeightWidthRatio), extraParams.MaxHeight, 0)
				if err != nil {
					return err
				}
			} else {
				err := img.Thumbnail(extraParams.MaxWidth, int(float32(extraParams.MaxWidth)*imgHeightWidthRatio), 0)
				if err != nil {
					return err
				}
			}
		}
	}

	if extraParams.MaxHeight > 0 && imageHeight > extraParams.MaxHeight && extraParams.MaxWidth == 0 {
		err := img.Thumbnail(int(float32(extraParams.MaxHeight)/imgHeightWidthRatio), extraParams.MaxHeight, 0)
		if err != nil {
			return err
		}
	}

	if extraParams.MaxWidth > 0 && imageWidth > extraParams.MaxWidth && extraParams.MaxHeight == 0 {
		err := img.Thumbnail(extraParams.MaxWidth, int(float32(extraParams.MaxWidth)*imgHeightWidthRatio), 0)
		if err != nil {
			return err
		}
	}

	if extraParams.Width > 0 && extraParams.Height > 0 {
		var cropInteresting vips.Interesting
		switch config.Config.ExtraParamsCropInteresting {
		case "InterestingNone":
			cropInteresting = vips.InterestingNone
		case "InterestingCentre":
			cropInteresting = vips.InterestingCentre
		case "InterestingEntropy":
			cropInteresting = vips.InterestingEntropy
		case "InterestingAttention":
			cropInteresting = vips.InterestingAttention
		case "InterestingLow":
			cropInteresting = vips.InterestingLow
		case "InterestingHigh":
			cropInteresting = vips.InterestingHigh
		case "InterestingAll":
			cropInteresting = vips.InterestingAll
		default:
			cropInteresting = vips.InterestingAttention
		}

		err := img.Thumbnail(extraParams.Width, extraParams.Height, cropInteresting)
		if err != nil {
			return err
		}
	}
	if extraParams.Width > 0 && extraParams.Height == 0 {
		err := img.Thumbnail(extraParams.Width, int(float32(extraParams.Width)*imgHeightWidthRatio), 0)
		if err != nil {
			return err
		}
	}
	if extraParams.Height > 0 && extraParams.Width == 0 {
		err := img.Thumbnail(int(float32(extraParams.Height)/imgHeightWidthRatio), extraParams.Height, 0)
		if err != nil {
			return err
		}
	}
	return nil
}

func ResizeItself(raw, dest string, extraParams config.ExtraParams) {
	log.Infof("开始调整图像大小: 源文件=%s, 目标文件=%s", raw, dest)

	// 创建目标目录
	if err := os.MkdirAll(path.Dir(dest), 0755); err != nil {
		log.Errorf("创建目标目录失败: %v", err)
		return
	}

	// 加载图像
	img, err := vips.LoadImageFromFile(raw, &vips.ImportParams{
		FailOnError: boolFalse,
	})
	if err != nil {
		log.Warnf("加载图像失败: 文件=%s, 错误=%v", raw, err)
		return
	}
	defer img.Close()

	// 调整图像大小
	if err := resizeImage(img, extraParams); err != nil {
		log.Warnf("调整图像大小失败: %v", err)
		return
	}

	// 移除元数据（如果配置要求）
	if config.Config.StripMetadata {
		log.Debug("正在移除图像元数据")
		img.RemoveMetadata()
	}

	// 导出图像
	buf, _, err := img.ExportNative()
	if err != nil {
		log.Errorf("导出图像失败: %v", err)
		return
	}

	// 写入文件
	if err := os.WriteFile(dest, buf, 0600); err != nil {
		log.Errorf("写入目标文件失败: 文件=%s, 错误=%v", dest, err)
		return
	}

	log.Infof("图像大小调整成功: 目标文件=%s", dest)
}

// Pre-process image(auto rotate, resize, etc.)
func preProcessImage(img *vips.ImageRef, imageType string, extraParams config.ExtraParams) error {
	log.Debugf("开始预处理图像: 类型=%s, 宽度=%d, 高度=%d", imageType, img.Metadata().Width, img.Metadata().Height)

	// 检查宽度/高度并忽略特定图像格式
	switch imageType {
	case "webp":
		if img.Metadata().Width > config.WebpMax || img.Metadata().Height > config.WebpMax {
			log.Warnf("WebP图像尺寸超限: 宽度=%d, 高度=%d, 最大限制=%d", img.Metadata().Width, img.Metadata().Height, config.WebpMax)
			return errors.New("WebP：图像太大")
		}
		if slices.Contains(webpIgnore, img.Format()) {
			log.Infof("WebP编码器忽略图像类型: %s", img.Format())
			return errors.New("WebP 编码器：忽略图像类型")
		}
	case "avif":
		if img.Metadata().Width > config.AvifMax || img.Metadata().Height > config.AvifMax {
			log.Warnf("AVIF图像尺寸超限: 宽度=%d, 高度=%d, 最大限制=%d", img.Metadata().Width, img.Metadata().Height, config.AvifMax)
			return errors.New("AVIF：图像太大")
		}
		if slices.Contains(avifIgnore, img.Format()) {
			log.Infof("AVIF编码器忽略图像类型: %s", img.Format())
			return errors.New("AVIF 编码器：忽略图像类型")
		}
	}

	// 自动旋转
	if err := img.AutoRotate(); err != nil {
		log.Errorf("图像自动旋转失败: %v", err)
		return err
	}
	log.Debug("图像自动旋转完成")

	// 额外参数处理
	if config.Config.EnableExtraParams {
		log.Debug("开始应用额外图像处理参数")
		if err := resizeImage(img, extraParams); err != nil {
			log.Errorf("应用额外图像处理参数失败: %v", err)
			return err
		}
		log.Debug("额外图像处理参数应用完成")
	}

	log.Debug("图像预处理完成")
	return nil
}

func ProcessAndSaveImage(rawImageAbs, exhaustFilename string, extraParams config.ExtraParams) error {
	log.Infof("开始处理图像: 源文件=%s, 目标文件=%s", rawImageAbs, exhaustFilename)

	// 创建目标目录
	if err := os.MkdirAll(path.Dir(exhaustFilename), 0755); err != nil {
		log.Errorf("创建目标目录失败: %v", err)
		return err
	}

	// 加载图像
	img, err := vips.LoadImageFromFile(rawImageAbs, &vips.ImportParams{
		FailOnError: boolFalse,
	})
	if err != nil {
		log.Warnf("加载图像失败: 文件=%s, 错误=%v", rawImageAbs, err)
		return err
	}
	defer img.Close()

	// 调整图像大小
	if err := resizeImage(img, extraParams); err != nil {
		log.Warnf("调整图像大小失败: %v", err)
		return err
	}

	// 移除元数据（如果配置要求）
	if config.Config.StripMetadata {
		log.Debug("正在移除图像元数据")
		img.RemoveMetadata()
	}

	var buf []byte
	var exportErr error

	// 确定输出格式
	if strings.HasSuffix(exhaustFilename, ".avif") {
		buf, _, exportErr = img.ExportAvif(vips.AvifExportParams{
			Quality: config.Config.Quality,
		})
	} else if strings.HasSuffix(exhaustFilename, ".jxl") {
		// 注意：govips 可能不直接支持 JXL 导出，这里使用 JPEG 作为替代
		buf, _, exportErr = img.ExportJpeg(vips.JpegExportParams{
			Quality: config.Config.Quality,
		})
	} else {
		// 默认使用 WebP
		buf, _, exportErr = img.ExportWebp(vips.WebpExportParams{
			Quality: config.Config.Quality,
		})
	}

	if exportErr != nil {
		log.Errorf("导出图像失败: %v", exportErr)
		return exportErr
	}

	// 写入文件
	if err := os.WriteFile(exhaustFilename, buf, 0600); err != nil {
		log.Errorf("写入目标文件失败: 文件=%s, 错误=%v", exhaustFilename, err)
		return err
	}

	log.Infof("图像处理成功: 目标文件=%s", exhaustFilename)
	return nil
}
