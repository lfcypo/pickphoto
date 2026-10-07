package catalog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFolderRestoresMovedPhoto(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "photos")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(filepath.Join(root, "data", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	first := filepath.Join(source, "a.jpg")
	if err := os.WriteFile(first, []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	session, err := LoadFolder(store, source)
	if err != nil || len(session.Photos) != 1 {
		t.Fatalf("初次加载: %v %v", session, err)
	}
	selected := filepath.Join(root, "custom", "a.jpg")
	if err := os.MkdirAll(filepath.Dir(selected), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(first, selected); err != nil {
		t.Fatal(err)
	}
	session.SelectedDir = filepath.Dir(selected)
	session.Photos[0].CurrentPath = selected
	session.Photos[0].Status = Selected
	if err := store.Save(session); err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadFolder(store, source)
	if err != nil || len(reloaded.Photos) != 1 || reloaded.Photos[0].CurrentPath != selected {
		t.Fatalf("重启恢复失败: %v %v", reloaded, err)
	}
}

func TestReconcilePending(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "a.jpg")
	target := filepath.Join(root, "selected.jpg")
	if err := os.WriteFile(target, []byte("done"), 0o600); err != nil {
		t.Fatal(err)
	}
	photo := Photo{OriginalPath: source, CurrentPath: source, Status: Waiting, PendingPath: target, PendingType: Selected}
	Reconcile(&photo)
	if photo.Status != Selected || photo.CurrentPath != target || photo.PendingPath != "" {
		t.Fatalf("未恢复已完成的移动: %+v", photo)
	}
}
