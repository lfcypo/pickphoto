package operation

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/lfcypo/pickphoto/internal/catalog"
	"github.com/lfcypo/pickphoto/internal/movefile"
)

func Execute(store *catalog.Store, source string, photo catalog.Photo, targetDir string, target catalog.Status) (catalog.Photo, error) {
	if photo.Problem != "" {
		return photo, errors.New(photo.Problem)
	}
	targetPath := filepath.Join(targetDir, photo.Name())
	if filepath.Clean(photo.CurrentPath) == filepath.Clean(targetPath) {
		before := photo
		photo.Status = target
		if err := store.SavePhoto(source, photo); err != nil {
			return before, err
		}
		return photo, nil
	}
	photo.PendingPath, photo.PendingType = targetPath, target
	if err := store.SavePhoto(source, photo); err != nil {
		photo.PendingPath, photo.PendingType = "", ""
		return photo, fmt.Errorf("无法保存操作记录: %w", err)
	}
	moveError := movefile.Move(photo.CurrentPath, targetPath)
	if moveError != nil {
		if errors.Is(moveError, movefile.ErrConflict) {
			photo.PendingPath, photo.PendingType = "", ""
		} else {
			catalog.Reconcile(&photo)
		}
		if err := store.SavePhoto(source, photo); err != nil {
			moveError = errors.Join(moveError, err)
		}
		return photo, moveError
	}
	photo.CurrentPath, photo.Status = targetPath, target
	photo.PendingPath, photo.PendingType = "", ""
	if err := store.SavePhoto(source, photo); err != nil {
		photo.Problem = "文件已移动 但保存记录失败"
		return photo, err
	}
	return photo, nil
}
