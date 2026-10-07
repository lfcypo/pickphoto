package movefile

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/lfcypo/pickphoto/internal/photofile"
)

func TestMoveAndConflict(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.jpg")
	target := filepath.Join(root, "selected", "source.jpg")
	if err := os.WriteFile(source, []byte("photo"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Move(source, target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("源文件仍存在: %v", err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "photo" {
		t.Fatalf("目标文件不正确: %q %v", data, err)
	}
	if err := os.WriteFile(source, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Move(source, target); !errors.Is(err, ErrConflict) {
		t.Fatalf("预期同名冲突 实际 %v", err)
	}
	if data, err := os.ReadFile(source); err != nil || string(data) != "new" {
		t.Fatalf("冲突后源文件丢失: %q %v", data, err)
	}
}

func TestMoveWhilePhotoIsBeingRead(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "photo.jpg")
	target := filepath.Join(root, "selected", "photo.jpg")
	if err := os.WriteFile(source, []byte("photo bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	reader, err := photofile.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if err := Move(source, target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("移动后源文件仍存在: %v", err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "photo bytes" {
		t.Fatalf("移动后的照片错误: %q %v", data, err)
	}
}

func TestMoveAcrossVolumesFallback(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "original.png")
	target := filepath.Join(root, "different-disk", "original.png")
	if err := os.WriteFile(source, []byte("image bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := moveWithLink(source, target, func(string, string) error { return syscall.EXDEV }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("跨磁盘移动后源文件仍存在: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "image bytes" {
		t.Fatalf("目标文件内容错误: %q %v", data, err)
	}
	entries, err := os.ReadDir(filepath.Dir(target))
	if err != nil || len(entries) != 1 {
		t.Fatalf("临时文件未清理: %v %v", entries, err)
	}
}
