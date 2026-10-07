package app

import (
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/lfcypo/pickphoto/internal/catalog"
	"github.com/lfcypo/pickphoto/internal/metadata"
)

func TestEXIFOrderAndCopy(t *testing.T) {
	fields := presentEXIF([]metadata.Field{
		{Name: "IFD/GPSInfo/GPSLatitude", Value: "39 deg 54 min"},
		{Name: "IFD/Exif/ExposureTime", Value: "1/125"},
		{Name: "IFD/Model", Value: "X-T5"},
	})
	if fields[0].Name != "IFD/Model" || fields[1].Name != "IFD/Exif/ExposureTime" || fields[2].Name != "IFD/GPSInfo/GPSLatitude" {
		t.Fatalf("常用字段顺序错误: %+v", fields)
	}
	if exifLabel(fields[0].Name) != "相机型号" || exifLabel(fields[2].Name) != "GPSLatitude" {
		t.Fatal("字段显示名称错误")
	}
	path := filepath.Join(t.TempDir(), "photo.jpg")
	app := &App{
		session: &catalog.Session{Photos: []catalog.Photo{{OriginalPath: path, CurrentPath: path, Problem: "无法读取"}}},
		fields:  fields, thumbs: map[string]*ui.Bitmap{}, thumbErrors: map[string]string{path: "无法读取"},
		thumbRequested: map[string]bool{}, queued: map[int]catalog.Status{},
	}
	tester := ui.NewTester(app.view, 1200, 780)
	if err := tester.Click("相机型号"); err != nil || tester.Clipboard() != "相机型号" {
		t.Fatalf("复制字段名失败: %v %q", err, tester.Clipboard())
	}
	if err := tester.Click("X-T5"); err != nil || tester.Clipboard() != "X-T5" {
		t.Fatalf("复制字段内容失败: %v %q", err, tester.Clipboard())
	}
	if err := tester.Click("›"); err != nil || !app.detailsClosed {
		t.Fatalf("收起侧栏失败: %v", err)
	}
	if err := tester.Click("‹"); err != nil || app.detailsClosed {
		t.Fatalf("展开侧栏失败: %v", err)
	}
}
