//go:build windows

package movefile

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestBusyPhotoCanBeRetriedWithoutDuplicate(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "photo.jpg")
	target := filepath.Join(root, "rejected", "photo.jpg")
	if err := os.WriteFile(source, []byte("photo bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(source)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	err = Move(source, target)
	if !IsBusy(err) {
		t.Fatalf("预期文件占用错误 实际 %v", err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("占用失败后源文件丢失: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("占用失败后出现目标副本: %v", err)
	}
	if err := windows.CloseHandle(handle); err != nil {
		t.Fatal(err)
	}
	if err := Move(source, target); err != nil {
		t.Fatalf("释放占用后重试失败: %v", err)
	}
}
