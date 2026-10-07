package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	bolt "go.etcd.io/bbolt"
)

type Status string

const (
	Waiting  Status = "waiting"
	Selected Status = "selected"
	Rejected Status = "rejected"
)

type Photo struct {
	OriginalPath string `json:"original_path"`
	CurrentPath  string `json:"current_path"`
	Status       Status `json:"status"`
	PendingPath  string `json:"pending_path,omitempty"`
	PendingType  Status `json:"pending_type,omitempty"`
	Problem      string `json:"problem,omitempty"`
}

func (p Photo) Name() string { return filepath.Base(p.OriginalPath) }

type Session struct {
	SourceDir   string  `json:"source_dir"`
	SelectedDir string  `json:"selected_dir"`
	RejectedDir string  `json:"rejected_dir"`
	Photos      []Photo `json:"photos"`
}

type Store struct{ db *bolt.DB }

var sessionsBucket = []byte("sessions")

func OpenStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 0})
	if err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Load(source string) (*Session, error) {
	var session *Session
	err := s.db.View(func(tx *bolt.Tx) error {
		root := tx.Bucket(sessionsBucket)
		if root == nil {
			return nil
		}
		bucket := root.Bucket([]byte(source))
		if bucket == nil {
			return nil
		}
		data := bucket.Get([]byte("config"))
		if data == nil {
			return nil
		}
		session = new(Session)
		if err := json.Unmarshal(data, session); err != nil {
			return err
		}
		photos := bucket.Bucket([]byte("photos"))
		if photos == nil {
			return nil
		}
		return photos.ForEach(func(_, value []byte) error {
			var photo Photo
			if err := json.Unmarshal(value, &photo); err != nil {
				return err
			}
			session.Photos = append(session.Photos, photo)
			return nil
		})
	})
	return session, err
}

func (s *Store) Save(session *Session) error {
	config := *session
	config.Photos = nil
	data, err := json.Marshal(config)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		root, err := tx.CreateBucketIfNotExists(sessionsBucket)
		if err != nil {
			return err
		}
		bucket, err := root.CreateBucketIfNotExists([]byte(session.SourceDir))
		if err != nil {
			return err
		}
		if err := bucket.Put([]byte("config"), data); err != nil {
			return err
		}
		photos, err := bucket.CreateBucketIfNotExists([]byte("photos"))
		if err != nil {
			return err
		}
		for _, photo := range session.Photos {
			encoded, err := json.Marshal(photo)
			if err != nil {
				return err
			}
			if err := photos.Put([]byte(photo.OriginalPath), encoded); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) SavePhoto(source string, photo Photo) error {
	return s.SavePhotos(source, []Photo{photo})
}

func (s *Store) SavePhotos(source string, changed []Photo) error {
	if len(changed) == 0 {
		return nil
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		root := tx.Bucket(sessionsBucket)
		if root == nil {
			return errors.New("筛选记录不存在")
		}
		session := root.Bucket([]byte(source))
		if session == nil {
			return errors.New("筛选记录不存在")
		}
		photos := session.Bucket([]byte("photos"))
		for _, photo := range changed {
			encoded, err := json.Marshal(photo)
			if err != nil {
				return err
			}
			if err := photos.Put([]byte(photo.OriginalPath), encoded); err != nil {
				return err
			}
		}
		return nil
	})
}

func IsPhoto(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg", ".png", ".webp":
		return true
	default:
		return false
	}
}

func ValidTargets(source, selected, rejected string) error {
	if source == "" || selected == "" || rejected == "" {
		return errors.New("照片和分类文件夹都不能为空")
	}
	source = filepath.Clean(source)
	selected = filepath.Clean(selected)
	rejected = filepath.Clean(rejected)
	if samePath(source, selected) || samePath(source, rejected) || samePath(selected, rejected) {
		return errors.New("源文件夹 选用文件夹和弃用文件夹必须各不相同")
	}
	return nil
}

func samePath(a, b string) bool {
	infoA, errA := os.Stat(a)
	infoB, errB := os.Stat(b)
	if errA == nil && errB == nil && os.SameFile(infoA, infoB) {
		return true
	}
	if os.PathSeparator == '\\' {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func LoadFolder(store *Store, source string) (*Session, error) {
	absolute, err := filepath.Abs(source)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("不是文件夹: %s", absolute)
	}
	session, err := store.Load(absolute)
	if err != nil {
		return nil, err
	}
	newSession := session == nil
	if session == nil {
		session = &Session{SourceDir: absolute, SelectedDir: filepath.Join(absolute, "选用"), RejectedDir: filepath.Join(absolute, "弃用")}
	}
	entries, err := os.ReadDir(absolute)
	if err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(session.Photos))
	var changed []Photo
	for index := range session.Photos {
		photo := &session.Photos[index]
		known[photo.OriginalPath] = true
		before := *photo
		Reconcile(photo)
		if *photo != before {
			changed = append(changed, *photo)
		}
	}
	for _, entry := range entries {
		if entry.IsDir() || !IsPhoto(entry.Name()) {
			continue
		}
		path := filepath.Join(absolute, entry.Name())
		if !known[path] {
			photo := Photo{OriginalPath: path, CurrentPath: path, Status: Waiting}
			session.Photos = append(session.Photos, photo)
			changed = append(changed, photo)
		}
	}
	slices.SortStableFunc(session.Photos, func(a, b Photo) int {
		left, right := strings.ToLower(a.Name()), strings.ToLower(b.Name())
		if left < right {
			return -1
		}
		if left > right {
			return 1
		}
		return strings.Compare(a.OriginalPath, b.OriginalPath)
	})
	if newSession {
		if err := store.Save(session); err != nil {
			return nil, err
		}
	} else if err := store.SavePhotos(session.SourceDir, changed); err != nil {
		return nil, err
	}
	return session, nil
}

func Reconcile(photo *Photo) {
	photo.Problem = ""
	if photo.PendingPath != "" {
		fromExists := exists(photo.CurrentPath)
		toExists := exists(photo.PendingPath)
		switch {
		case !fromExists && toExists:
			photo.CurrentPath, photo.Status = photo.PendingPath, photo.PendingType
			photo.PendingPath, photo.PendingType = "", ""
		case fromExists && !toExists:
			photo.PendingPath, photo.PendingType = "", ""
		case fromExists && toExists:
			photo.Problem = "移动中断 两处均有文件 请检查后处理"
		default:
			photo.Problem = "移动中断 原位置和目标位置都找不到文件"
		}
	}
	if photo.Problem == "" && !exists(photo.CurrentPath) {
		photo.Problem = "文件已在应用外移动或删除"
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
