package encoder

import (
	"path/filepath"

	"github.com/jeremytorres/rawparser"
)

// ConvertRawToJPG 将原始图像文件转换为JPEG格式，并保存到优化路径。
// rawPath: 原始图像文件的路径。
// optimizedPath: 转换后的JPEG文件保存路径。
// 返回值: 成功时返回转换后的JPEG文件路径和true；失败时返回原始路径和false。

func ConvertRawToJPG(rawPath, optimizedPath string) (string, bool) {
	parser, _ := rawparser.NewNefParser(true)
	info := &rawparser.RawFileInfo{
		File:    rawPath,
		Quality: 100,
		DestDir: optimizedPath,
	}
	_, err := parser.ProcessFile(info)
	if err == nil {
		_, file := filepath.Split(rawPath)
		return optimizedPath + file + "_extracted.jpg", true
	}
	return rawPath, false
}
