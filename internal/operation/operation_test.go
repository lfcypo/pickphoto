package operation

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/lfcypo/pickphoto/internal/catalog"
	"github.com/lfcypo/pickphoto/internal/movefile"
)

func TestClassifyUndoAndResume(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "photos")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(source, "photo.jpg")
	if err := os.WriteFile(original, []byte("photo"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := catalog.OpenStore(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	session, err := catalog.LoadFolder(store, source)
	if err != nil {
		t.Fatal(err)
	}
	selectedDir := filepath.Join(root, "custom-selected")
	photo, err := Execute(store, source, session.Photos[0], selectedDir, catalog.Selected)
	if err != nil || photo.Status != catalog.Selected {
		t.Fatalf("选用失败: %+v %v", photo, err)
	}
	reloaded, err := catalog.LoadFolder(store, source)
	if err != nil || len(reloaded.Photos) != 1 || reloaded.Photos[0].Status != catalog.Selected {
		t.Fatalf("恢复失败: %+v %v", reloaded, err)
	}
	photo, err = Execute(store, source, reloaded.Photos[0], source, catalog.Waiting)
	if err != nil || photo.Status != catalog.Waiting || photo.CurrentPath != original {
		t.Fatalf("撤销失败: %+v %v", photo, err)
	}
}

func TestConflictPreservesSource(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "photos")
	selected := filepath.Join(root, "selected")
	for _, path := range []string{source, selected} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{filepath.Join(source, "same.jpg"), filepath.Join(selected, "same.jpg")} {
		if err := os.WriteFile(path, []byte(path), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	store, err := catalog.OpenStore(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	session, err := catalog.LoadFolder(store, source)
	if err != nil {
		t.Fatal(err)
	}
	photo, err := Execute(store, source, session.Photos[0], selected, catalog.Selected)
	if !errors.Is(err, movefile.ErrConflict) || photo.Status != catalog.Waiting {
		t.Fatalf("同名冲突处理错误: %+v %v", photo, err)
	}
	if _, err := os.Stat(photo.CurrentPath); err != nil {
		t.Fatal(err)
	}
}
