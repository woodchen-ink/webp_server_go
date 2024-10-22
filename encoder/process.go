package encoder

import (
	"errors"
	"os"
	"path"
	"slices"
	"strings"
	"webp_server_go/config"
	"webp_server_go/helper"

	"github.com/davidbyttow/govips/v2/vips"
	log "github.com/sirupsen/logrus"
)

func resizeImage(img *vips.ImageRef, extraParams config.ExtraParams) error {
	imageHeight := img.Height()
	imageWidth := img.Width()

	imgHeightWidthRatio := float32(imageHeight) / float32(imageWidth)

	log.Infof("开始调整图像大小。原始尺寸: %dx%d, 比例: %.2f", imageWidth, imageHeight, imgHeightWidthRatio)
	log.Infof("请求的参数: MaxWidth=%d, MaxHeight=%d, Width=%d, Height=%d",
		extraParams.MaxWidth, extraParams.MaxHeight, extraParams.Width, extraParams.Height)

	if extraParams.MaxHeight > 0 && extraParams.MaxWidth > 0 {
		if imageHeight > extraParams.MaxHeight || imageWidth > extraParams.MaxWidth {
			heightExceedRatio := float32(imageHeight) / float32(extraParams.MaxHeight)
			widthExceedRatio := float32(imageWidth) / float32(extraParams.MaxWidth)
			if heightExceedRatio > widthExceedRatio {
				newWidth := int(float32(extraParams.MaxHeight) / imgHeightWidthRatio)
				log.Infof("使用MaxHeight调整大小: %dx%d", newWidth, extraParams.MaxHeight)
				err := img.Thumbnail(newWidth, extraParams.MaxHeight, 0)
				if err != nil {
					log.Errorf("调整大小失败: %v", err)
					return err
				}
			} else {
				newHeight := int(float32(extraParams.MaxWidth) * imgHeightWidthRatio)
				log.Infof("使用MaxWidth调整大小: %dx%d", extraParams.MaxWidth, newHeight)
				err := img.Thumbnail(extraParams.MaxWidth, newHeight, 0)
				if err != nil {
					log.Errorf("调整大小失败: %v", err)
					return err
				}
			}
		} else {
			log.Info("图像尺寸在MaxWidth和MaxHeight范围内，无需调整")
		}
	}

	if extraParams.MaxHeight > 0 && imageHeight > extraParams.MaxHeight && extraParams.MaxWidth == 0 {
		newWidth := int(float32(extraParams.MaxHeight) / imgHeightWidthRatio)
		log.Infof("仅使用MaxHeight调整大小: %dx%d", newWidth, extraParams.MaxHeight)
		err := img.Thumbnail(newWidth, extraParams.MaxHeight, 0)
		if err != nil {
			log.Errorf("调整大小失败: %v", err)
			return err
		}
	}

	if extraParams.MaxWidth > 0 && imageWidth > extraParams.MaxWidth && extraParams.MaxHeight == 0 {
		newHeight := int(float32(extraParams.MaxWidth) * imgHeightWidthRatio)
		log.Infof("仅使用MaxWidth调整大小: %dx%d", extraParams.MaxWidth, newHeight)
		err := img.Thumbnail(extraParams.MaxWidth, newHeight, 0)
		if err != nil {
			log.Errorf("调整大小失败: %v", err)
			return err
		}
	}

	if extraParams.Width > 0 && extraParams.Height > 0 {
		log.Infof("使用指定的Width和Height调整大小: %dx%d", extraParams.Width, extraParams.Height)
		cropInteresting := getCropInteresting()
		err := img.Thumbnail(extraParams.Width, extraParams.Height, cropInteresting)
		if err != nil {
			log.Errorf("调整大小失败: %v", err)
			return err
		}
	}

	if extraParams.Width > 0 && extraParams.Height == 0 {
		newHeight := int(float32(extraParams.Width) * imgHeightWidthRatio)
		log.Infof("仅使用Width调整大小: %dx%d", extraParams.Width, newHeight)
		err := img.Thumbnail(extraParams.Width, newHeight, 0)
		if err != nil {
			log.Errorf("调整大小失败: %v", err)
			return err
		}
	}

	if extraParams.Height > 0 && extraParams.Width == 0 {
		newWidth := int(float32(extraParams.Height) / imgHeightWidthRatio)
		log.Infof("仅使用Height调整大小: %dx%d", newWidth, extraParams.Height)
		err := img.Thumbnail(newWidth, extraParams.Height, 0)
		if err != nil {
			log.Errorf("调整大小失败: %v", err)
			return err
		}
	}

	log.Infof("图像调整完成。新尺寸: %dx%d", img.Width(), img.Height())
	return nil
}

func getCropInteresting() vips.Interesting {
	cropInteresting := vips.InterestingAttention
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
	}
	log.Infof("使用裁剪策略: %s", config.Config.ExtraParamsCropInteresting)
	return cropInteresting
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

// 预处理图像（自动旋转、调整大小等）

func preProcessImage(img *vips.ImageRef, imageType string, extraParams config.ExtraParams) (bool, error) {
	log.Debugf("开始预处理图像: 类型=%s, 宽度=%d, 高度=%d", imageType, img.Metadata().Width, img.Metadata().Height)

	// 标志，用于指示是否应该复制原图
	var shouldCopyOriginal bool

	// 检查宽度/高度并忽略特定图像格式
	switch imageType {
	case "webp":
		if img.Metadata().Width > config.WebpMax || img.Metadata().Height > config.WebpMax {
			log.Warnf("WebP图像尺寸超限: 宽度=%d, 高度=%d, 最大限制=%d", img.Metadata().Width, img.Metadata().Height, config.WebpMax)
			shouldCopyOriginal = true
			return shouldCopyOriginal, errors.New("WebP：图像太大")
		}
		if slices.Contains(webpIgnore, img.Format()) {
			log.Infof("WebP编码器忽略图像类型: %s", img.Format())
			shouldCopyOriginal = true
			return shouldCopyOriginal, errors.New("WebP 编码器：忽略图像类型")
		}
	case "avif":
		if img.Metadata().Width > config.AvifMax || img.Metadata().Height > config.AvifMax {
			log.Warnf("AVIF图像尺寸超限: 宽度=%d, 高度=%d, 最大限制=%d", img.Metadata().Width, img.Metadata().Height, config.AvifMax)
			shouldCopyOriginal = true
			return shouldCopyOriginal, errors.New("AVIF：图像太大")
		}
		if slices.Contains(avifIgnore, img.Format()) {
			log.Infof("AVIF编码器忽略图像类型: %s", img.Format())
			shouldCopyOriginal = true
			return shouldCopyOriginal, errors.New("AVIF 编码器：忽略图像类型")
		}
	}

	// 自动旋转
	if err := img.AutoRotate(); err != nil {
		log.Errorf("图像自动旋转失败: %v", err)
		shouldCopyOriginal = true
		return shouldCopyOriginal, err
	}
	log.Debug("图像自动旋转完成")

	// 额外参数处理
	if config.Config.EnableExtraParams {
		log.Debug("开始应用额外图像处理参数")
		if err := resizeImage(img, extraParams); err != nil {
			log.Errorf("应用额外图像处理参数失败: %v", err)
			// 这里不设置 shouldCopyOriginal 为 true，因为我们不想在这种情况下复制原图
			return shouldCopyOriginal, err
		}
		log.Debug("额外图像处理参数应用完成")
	}

	log.Debug("图像预处理完成")
	return shouldCopyOriginal, nil
}

func ProcessAndSaveImage(rawImageAbs, exhaustFilename string, extraParams config.ExtraParams) error {
	log.Infof("开始处理图像: 源文件=%s, 目标文件=%s", rawImageAbs, exhaustFilename)

	// 创建目标目录
	if err := os.MkdirAll(path.Dir(exhaustFilename), 0755); err != nil {
		log.Errorf("创建目标目录失败: %v", err)
		return err
	}

	// 获取原图文件大小
	originalInfo, err := os.Stat(rawImageAbs)
	if err != nil {
		log.Errorf("获取原图文件信息失败: %v", err)
		return err
	}
	originalSize := originalInfo.Size()

	// 如果原始图像是 NEF 格式，先转换为 JPG
	if strings.HasSuffix(strings.ToLower(rawImageAbs), ".nef") {
		tempJPG, converted := ConvertRawToJPG(rawImageAbs, exhaustFilename)
		if converted {
			defer func() {
				log.Infoln("移除中间转换文件:", tempJPG)
				if err := os.Remove(tempJPG); err != nil {
					log.Warnln("删除转换文件失败", err)
				}
			}()
			rawImageAbs = tempJPG
		}
	}

	// 加载图像
	img, err := vips.LoadImageFromFile(rawImageAbs, &vips.ImportParams{
		FailOnError: boolFalse,
		NumPages:    intMinusOne,
	})
	if err != nil {
		log.Warnf("无法打开源图像: %v", err)
		return err
	}
	defer img.Close()

	// 确定输出格式
	var imageType string
	switch {
	case strings.HasSuffix(exhaustFilename, ".avif"):
		imageType = "avif"
	case strings.HasSuffix(exhaustFilename, ".jxl"):
		imageType = "jxl"
	default:
		imageType = "webp"
	}

	// 预处理图像（自动旋转、调整大小等）
	shouldCopyOriginal, err := preProcessImage(img, imageType, extraParams)
	if err != nil {
		log.Warnf("预处理源图像时出错: %v", err)
		if shouldCopyOriginal {
			log.Infof("由于预处理错误，将复制原图")
			return helper.CopyFile(rawImageAbs, exhaustFilename)
		}
		// 如果不应该复制原图，就返回错误
		return err
	}

	// 根据图像类型进行编码
	var encoderErr error
	switch imageType {
	case "webp":
		encoderErr = webpEncoder(img, rawImageAbs, exhaustFilename)
	case "avif":
		encoderErr = avifEncoder(img, rawImageAbs, exhaustFilename)
	case "jxl":
		encoderErr = jxlEncoder(img, rawImageAbs, exhaustFilename)
	}

	if encoderErr != nil {
		log.Errorf("图像编码失败: %v", encoderErr)
		return helper.CopyFile(rawImageAbs, exhaustFilename) // 这里可以考虑复制原图
	}

	// 比较转换后的文件大小
	convertedInfo, err := os.Stat(exhaustFilename)
	if err != nil {
		log.Errorf("获取转换后文件信息失败: %v", err)
		return err
	}

	if convertedInfo.Size() > originalSize {
		log.Infof("转换后的图片大于原图，使用原图: %s", rawImageAbs)
		// 删除转换后的大文件
		if err := os.Remove(exhaustFilename); err != nil {
			log.Warnf("删除大的转换文件失败: %v", err)
		}
		// 将原图复制到 EXHAUST_PATH
		if err := helper.CopyFile(rawImageAbs, exhaustFilename); err != nil {
			log.Errorf("复制原图到 EXHAUST_PATH 失败: %v", err)
			return err
		}
		log.Infof("成功将原图复制到 EXHAUST_PATH: %s", exhaustFilename)
	} else {
		log.Infof("图像处理成功: 目标文件=%s", exhaustFilename)
	}

	return nil
}
