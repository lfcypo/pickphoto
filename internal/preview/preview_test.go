package preview

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOrientationSix(t *testing.T) {
	input := image.NewRGBA(image.Rect(0, 0, 2, 1))
	input.Set(0, 0, color.RGBA{R: 255, A: 255})
	input.Set(1, 0, color.RGBA{B: 255, A: 255})
	rotated := orient(input, 6)
	if rotated.Bounds().Dx() != 1 || rotated.Bounds().Dy() != 2 {
		t.Fatalf("方向旋转后的尺寸错误: %v", rotated.Bounds())
	}
	if got := color.RGBAModel.Convert(rotated.At(0, 0)).(color.RGBA); got.R != 255 {
		t.Fatalf("首像素方向错误: %+v", got)
	}
	if got := color.RGBAModel.Convert(rotated.At(0, 1)).(color.RGBA); got.B != 255 {
		t.Fatalf("末像素方向错误: %+v", got)
	}
}

func TestThumbnailDiskCache(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "photo.png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	photo := image.NewRGBA(image.Rect(0, 0, 1200, 800))
	if err := png.Encode(file, photo); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	cacheDir := filepath.Join(root, "cache")
	if err := os.Mkdir(cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := decode(context.Background(), path, Thumbnail, cacheDir); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(cacheDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("缩略图缓存数量错误: %d %v", len(entries), err)
	}
}

func TestLoaderCloseRemovesThumbnailCache(t *testing.T) {
	root := t.TempDir()
	photoPath := filepath.Join(root, "photo.png")
	file, err := os.Create(photoPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, image.NewRGBA(image.Rect(0, 0, 120, 80))); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(root, "state.db")
	if err := os.WriteFile(statePath, []byte("state"), 0o600); err != nil {
		t.Fatal(err)
	}
	cacheDir := filepath.Join(root, "thumbs")
	loader := NewLoader(cacheDir)
	defer loader.Close()
	results := make(chan Result, 1)
	if !loader.Request(photoPath, Thumbnail, func(result Result) { results <- result }) {
		t.Fatal("缩略图请求未加入队列")
	}
	select {
	case result := <-results:
		if result.Err != nil {
			t.Fatal(result.Err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("等待缩略图超时")
	}
	entries, err := os.ReadDir(cacheDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("缩略图缓存未生成: %d %v", len(entries), err)
	}
	if err := loader.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
		t.Fatalf("缩略图缓存仍然存在: %v", err)
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("筛选记录被删除: %v", err)
	}
	if loader.Request(photoPath, Thumbnail, func(Result) {}) {
		t.Fatal("已关闭的加载器仍接受请求")
	}
}
