package app

import (
	"slices"
	"strings"

	"github.com/lfcypo/pickphoto/internal/metadata"
)

var exifNames = map[string]string{
	"Make":                    "相机厂商",
	"Model":                   "相机型号",
	"LensMake":                "镜头厂商",
	"LensModel":               "镜头型号",
	"DateTimeOriginal":        "拍摄时间",
	"DateTimeDigitized":       "数字化时间",
	"DateTime":                "修改时间",
	"ExposureTime":            "快门时间",
	"FNumber":                 "光圈",
	"ISOSpeedRatings":         "感光度 ISO",
	"PhotographicSensitivity": "感光度 ISO",
	"FocalLength":             "焦距",
	"FocalLengthIn35mmFilm":   "等效焦距",
	"ExposureBiasValue":       "曝光补偿",
	"ExposureProgram":         "曝光模式",
	"MeteringMode":            "测光模式",
	"WhiteBalance":            "白平衡",
	"Flash":                   "闪光灯",
	"PixelXDimension":         "照片宽度",
	"PixelYDimension":         "照片高度",
	"ImageWidth":              "图像宽度",
	"ImageLength":             "图像高度",
	"Orientation":             "方向",
	"Software":                "处理软件",
	"Artist":                  "作者",
	"Copyright":               "版权",
}

var exifFirst = []string{
	"Make", "Model", "LensModel", "DateTimeOriginal", "ExposureTime",
	"FNumber", "ISOSpeedRatings", "PhotographicSensitivity", "FocalLength",
	"FocalLengthIn35mmFilm", "ExposureBiasValue", "PixelXDimension", "PixelYDimension",
}

func exifTag(name string) string {
	if index := strings.LastIndexByte(name, '/'); index >= 0 {
		return name[index+1:]
	}
	return name
}

func exifLabel(name string) string {
	tag := exifTag(name)
	if label, found := exifNames[tag]; found {
		return label
	}
	return tag
}

func presentEXIF(fields []metadata.Field) []metadata.Field {
	ordered := slices.Clone(fields)
	slices.SortStableFunc(ordered, func(a, b metadata.Field) int {
		return exifPriority(a.Name) - exifPriority(b.Name)
	})
	return ordered
}

func exifPriority(name string) int {
	for index, tag := range exifFirst {
		if exifTag(name) == tag {
			return index
		}
	}
	return len(exifFirst)
}
