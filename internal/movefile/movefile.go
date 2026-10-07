package movefile

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

var ErrConflict = errors.New("目标文件夹中已有同名文件")

func moveWithLink(source, target string, linkSource func(string, string) error) error {
	info, samePath, err := prepareMove(source, target)
	if err != nil || samePath {
		return err
	}
	// 同一文件系统优先建立链接 避免复制大图
	if err := linkSource(source, target); err == nil {
		if err := removeSource(source); err != nil {
			_ = os.Remove(target)
			return err
		}
		return nil
	} else if os.IsExist(err) {
		return ErrConflict
	}
	return copyAcrossVolumes(source, target, info)
}

func prepareMove(source, target string) (os.FileInfo, bool, error) {
	if filepath.Clean(source) == filepath.Clean(target) {
		return nil, true, nil
	}
	info, err := os.Stat(source)
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("不是普通文件: %s", source)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, false, err
	}
	if _, err := os.Lstat(target); err == nil {
		return nil, false, ErrConflict
	} else if !os.IsNotExist(err) {
		return nil, false, err
	}
	return info, false, nil
}

func copyAcrossVolumes(source, target string, info os.FileInfo) error {
	// 跨磁盘时先完整写入目标目录中的临时文件
	temporary, err := os.CreateTemp(filepath.Dir(target), ".pickphoto-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	input, err := os.Open(source)
	if err != nil {
		_ = temporary.Close()
		return err
	}
	copied, copyErr := io.Copy(temporary, input)
	closeInputErr := input.Close()
	syncErr := temporary.Sync()
	closeErr := temporary.Close()
	if err := errors.Join(copyErr, closeInputErr, syncErr, closeErr); err != nil {
		return err
	}
	if copied != info.Size() {
		return fmt.Errorf("复制照片时源文件大小发生变化: %s", source)
	}
	if err := os.Chmod(temporaryPath, info.Mode().Perm()); err != nil {
		return err
	}
	if err := os.Chtimes(temporaryPath, info.ModTime(), info.ModTime()); err != nil {
		return err
	}
	if err := os.Link(temporaryPath, target); err != nil {
		if os.IsExist(err) {
			return ErrConflict
		}
		if err := copyExclusive(temporaryPath, target, info); err != nil {
			return err
		}
	}
	if err := removeSource(source); err != nil {
		_ = os.Remove(target)
		return err
	}
	return nil
}

func removeSource(path string) error {
	return retryBusy(func() error { return os.Remove(path) })
}

func retryBusy(operation func() error) error {
	delay := 40 * time.Millisecond
	for attempt := 0; ; attempt++ {
		err := operation()
		if err == nil || !IsBusy(err) || attempt == 5 {
			return err
		}
		time.Sleep(delay)
		delay *= 2
	}
}

func copyExclusive(source, target string, original os.FileInfo) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, original.Mode().Perm())
	if err != nil {
		if os.IsExist(err) {
			return ErrConflict
		}
		return err
	}
	copied, copyErr := io.Copy(output, input)
	syncErr := output.Sync()
	closeErr := output.Close()
	if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
		_ = os.Remove(target)
		return err
	}
	if copied != original.Size() {
		_ = os.Remove(target)
		return fmt.Errorf("复制照片时长度不一致: %s", target)
	}
	if err := os.Chtimes(target, original.ModTime(), original.ModTime()); err != nil {
		_ = os.Remove(target)
		return err
	}
	return nil
}
