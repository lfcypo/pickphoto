package app

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/lfcypo/pickphoto/internal/catalog"
	"github.com/lfcypo/pickphoto/internal/metadata"
)

func TestNativeViewNavigatesPhotos(t *testing.T) {
	first := filepath.Join(t.TempDir(), "a.jpg")
	second := filepath.Join(t.TempDir(), "b.jpg")
	photoImage := image.NewRGBA(image.Rect(0, 0, 800, 500))
	for y := range 500 {
		for x := range 800 {
			photoImage.SetRGBA(x, y, color.RGBA{R: uint8(x / 4), G: uint8(y / 2), B: 120, A: 255})
		}
	}
	bitmap := ui.NewBitmap(photoImage)
	application := &App{
		session: &catalog.Session{Photos: []catalog.Photo{
			{OriginalPath: first, CurrentPath: first, Status: catalog.Waiting, Problem: "无法读取"},
			{OriginalPath: second, CurrentPath: second, Status: catalog.Selected, Problem: "无法读取"},
		}},
		large: bitmap, thumbs: map[string]*ui.Bitmap{first: bitmap, second: bitmap}, thumbErrors: map[string]string{},
		thumbRequested: map[string]bool{}, queued: map[int]catalog.Status{},
		fields: presentEXIF([]metadata.Field{
			{Name: "IFD/Make", Value: "FUJIFILM"},
			{Name: "IFD/Model", Value: "X-T5"},
			{Name: "IFD/Exif/LensModel", Value: "XF 33mm F1.4 R LM WR"},
			{Name: "IFD/Exif/ExposureTime", Value: "1/125"},
			{Name: "IFD/Exif/FNumber", Value: "f/2.8"},
			{Name: "IFD/GPSInfo/GPSLatitude", Value: "39 deg 54 min 12.34 sec"},
		}),
	}
	tester := ui.NewTester(application.view, 1200, 780)
	if path := os.Getenv("PICKPHOTO_SNAPSHOT_PATH"); path != "" {
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(file, tester.Image()); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if !tester.HasText("a.jpg") {
		t.Fatalf("缺少当前照片: %v", tester.Texts())
	}
	tester.Key(0, ui.KeyRight)
	if application.index != 1 || !tester.HasText("b.jpg") {
		t.Fatalf("键盘切换失败: index=%d texts=%v", application.index, tester.Texts())
	}
}

func TestNativeViewLargeCatalog(t *testing.T) {
	const count = 10000
	photos := make([]catalog.Photo, count)
	thumbErrors := make(map[string]string, count)
	for index := range photos {
		path := filepath.Join("photos", fmt.Sprintf("%05d.jpg", index))
		photos[index] = catalog.Photo{OriginalPath: path, CurrentPath: path, Status: catalog.Waiting, Problem: "无法读取"}
		thumbErrors[path] = "无法读取"
	}
	application := &App{session: &catalog.Session{Photos: photos}, thumbs: map[string]*ui.Bitmap{}, thumbErrors: thumbErrors, thumbRequested: map[string]bool{}, queued: map[int]catalog.Status{}}
	tester := ui.NewTester(application.view, 1200, 780)
	application.selectIndex(count - 1)
	tester.Frame()
	if application.index != count-1 || !tester.HasText("10000 / 10000") {
		t.Fatalf("大量照片跳转失败: %d", application.index)
	}
}

func TestCompletionDialogAfterLastSuccessfulMove(t *testing.T) {
	first := filepath.Join("photos", "a.jpg")
	second := filepath.Join("photos", "b.jpg")
	selected := catalog.Photo{OriginalPath: first, CurrentPath: first, Status: catalog.Selected, Problem: "无法读取"}
	waiting := catalog.Photo{OriginalPath: second, CurrentPath: second, Status: catalog.Waiting, Problem: "无法读取"}
	application := &App{
		session:        &catalog.Session{Photos: []catalog.Photo{selected, waiting}},
		selectedCount:  1,
		queue:          []command{{index: 1, target: catalog.Selected}},
		queued:         map[int]catalog.Status{1: catalog.Selected},
		thumbs:         map[string]*ui.Bitmap{},
		thumbErrors:    map[string]string{first: "无法读取", second: "无法读取"},
		thumbRequested: map[string]bool{},
	}
	application.showCompletionIfReady()
	if application.completionDialog {
		t.Fatal("仍有待定照片时不应提示完成")
	}
	application.finish(application.queue[0], waiting, errors.New("移动失败"))
	if application.completionDialog {
		t.Fatal("移动失败时不应提示完成")
	}
	application.blocked, application.blockDialog = false, false
	selectedSecond := waiting
	selectedSecond.Status = catalog.Selected
	application.finish(application.queue[0], selectedSecond, nil)
	if !application.completionDialog || !application.completionShown || !application.finished {
		t.Fatal("最后一张移动成功后应提示完成")
	}
	tester := ui.NewTester(application.view, 1200, 780)
	if !tester.HasText("筛选完成") || !tester.HasText("选用 2 张") || !tester.HasText("过片率 100.0%") {
		t.Fatalf("完成提示内容不正确: %v", tester.Texts())
	}
	tester.Click("继续查看")
	if application.completionDialog {
		t.Fatal("关闭提示后不应继续显示")
	}
	application.showCompletionIfReady()
	if application.completionDialog {
		t.Fatal("同一次筛选不应重复弹出")
	}
	application.queue = []command{{index: 1, target: catalog.Waiting}}
	application.queued[1] = catalog.Waiting
	application.finish(application.queue[0], waiting, nil)
	if application.completionShown || application.completionDialog || application.finished {
		t.Fatal("撤销成功后应允许再次提示完成")
	}
	application.queue = []command{{index: 1, target: catalog.Selected}}
	application.queued[1] = catalog.Selected
	application.finish(application.queue[0], selectedSecond, nil)
	if !application.completionDialog {
		t.Fatal("重新筛完后应再次提示完成")
	}
}
