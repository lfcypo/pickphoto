package preview

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/egoist/mygo/ui"
	"github.com/lfcypo/pickphoto/internal/metadata"
	"github.com/lfcypo/pickphoto/internal/photofile"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
	_ "image/jpeg"
)

type Kind int

const (
	Thumbnail Kind = iota
	Large
)

type Result struct {
	Path   string
	Kind   Kind
	Bitmap *ui.Bitmap
	Err    error
}

type job struct {
	path     string
	kind     Kind
	context  context.Context
	callback func(Result)
}

type Loader struct {
	largeJobs         chan job
	priorityThumbJobs chan job
	thumbJobs         chan job
	context           context.Context
	cancel            context.CancelFunc
	stop              chan struct{}
	workers           sync.WaitGroup
	closeOnce         sync.Once
	closeErr          error
	mu                sync.Mutex
	closed            bool
	cache             map[string]*ui.Bitmap
	order             []string
	pending           map[string][]func(Result)
	active            map[string]bool
	cacheDir          string
	writes            int
}

func NewLoader(cacheDir string) *Loader {
	ctx, cancel := context.WithCancel(context.Background())
	loader := &Loader{
		largeJobs:         make(chan job, 8),
		priorityThumbJobs: make(chan job, 8),
		thumbJobs:         make(chan job, 64),
		context:           ctx,
		cancel:            cancel,
		stop:              make(chan struct{}),
		cache:             make(map[string]*ui.Bitmap),
		pending:           make(map[string][]func(Result)),
		active:            make(map[string]bool),
		cacheDir:          cacheDir,
	}
	if cacheDir != "" {
		_ = os.MkdirAll(cacheDir, 0o700)
		loader.workers.Add(1)
		go func() {
			defer loader.workers.Done()
			pruneCache(cacheDir)
		}()
	}
	go loader.worker(loader.largeJobs)
	loader.workers.Add(2)
	go loader.thumbnailWorker()
	go loader.thumbnailWorker()
	return loader
}

func (l *Loader) Close() error {
	l.closeOnce.Do(func() {
		l.mu.Lock()
		l.closed = true
		l.mu.Unlock()
		l.cancel()
		close(l.stop)
		l.workers.Wait()
		if l.cacheDir != "" {
			l.closeErr = os.RemoveAll(l.cacheDir)
		}
	})
	return l.closeErr
}

func (l *Loader) Request(path string, kind Kind, done func(Result)) bool {
	return l.request(path, kind, false, done)
}

func (l *Loader) RequestPriorityThumbnail(path string, done func(Result)) bool {
	return l.request(path, Thumbnail, true, done)
}

func (l *Loader) request(path string, kind Kind, priority bool, done func(Result)) bool {
	key := cacheKey(path, kind)
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return false
	}
	if bitmap := l.cache[key]; bitmap != nil {
		l.mu.Unlock()
		go done(Result{Path: path, Kind: kind, Bitmap: bitmap})
		return true
	}
	if callbacks, found := l.pending[key]; found {
		l.pending[key] = append(callbacks, done)
		promote := priority && !l.active[key]
		l.mu.Unlock()
		if promote {
			select {
			case l.priorityThumbJobs <- job{path: path, kind: kind, context: l.context}:
			default:
			}
		}
		return true
	}
	l.pending[key] = []func(Result){done}
	l.mu.Unlock()
	work := job{path: path, kind: kind}
	if kind == Thumbnail {
		work.context = l.context
	}
	queue := l.thumbJobs
	if kind == Large {
		queue = l.largeJobs
	} else if priority {
		queue = l.priorityThumbJobs
	}
	select {
	case queue <- work:
		return true
	default:
		l.mu.Lock()
		delete(l.pending, key)
		l.mu.Unlock()
		return false
	}
}

func (l *Loader) thumbnailWorker() {
	defer l.workers.Done()
	for {
		select {
		case <-l.stop:
			return
		default:
		}
		select {
		case <-l.stop:
			return
		case work := <-l.priorityThumbJobs:
			l.process(work)
		default:
			select {
			case <-l.stop:
				return
			case work := <-l.priorityThumbJobs:
				l.process(work)
			case work := <-l.thumbJobs:
				l.process(work)
			}
		}
	}
}

func (l *Loader) RequestLarge(ctx context.Context, path string, done func(Result)) bool {
	key := cacheKey(path, Large)
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return false
	}
	bitmap := l.cache[key]
	l.mu.Unlock()
	if bitmap != nil {
		go func() {
			if ctx.Err() == nil {
				done(Result{Path: path, Kind: Large, Bitmap: bitmap})
			}
		}()
		return true
	}
	select {
	case l.largeJobs <- job{path: path, kind: Large, context: ctx, callback: done}:
		return true
	default:
		select {
		case <-l.largeJobs:
		default:
		}
		select {
		case l.largeJobs <- job{path: path, kind: Large, context: ctx, callback: done}:
			return true
		default:
			return false
		}
	}
}

func (l *Loader) worker(jobs <-chan job) {
	for work := range jobs {
		l.process(work)
	}
}

func (l *Loader) process(work job) {
	if work.context != nil && work.context.Err() != nil {
		return
	}
	key := cacheKey(work.path, work.kind)
	l.mu.Lock()
	if bitmap := l.cache[key]; bitmap != nil {
		callbacks := l.pending[key]
		delete(l.pending, key)
		l.mu.Unlock()
		if work.callback != nil {
			callbacks = append(callbacks, work.callback)
		}
		for _, callback := range callbacks {
			if callback != nil {
				callback(Result{Path: work.path, Kind: work.kind, Bitmap: bitmap})
			}
		}
		return
	}
	if l.active[key] {
		l.mu.Unlock()
		return
	}
	l.active[key] = true
	l.mu.Unlock()
	bitmap, err := decode(work.context, work.path, work.kind, l.cacheDir)
	if work.context != nil && work.context.Err() != nil {
		l.mu.Lock()
		delete(l.active, key)
		l.mu.Unlock()
		return
	}
	l.mu.Lock()
	delete(l.active, key)
	if existing := l.cache[key]; existing != nil {
		bitmap, err = existing, nil
	}
	callbacks := []func(Result){work.callback}
	if work.callback == nil {
		callbacks = l.pending[key]
		delete(l.pending, key)
	}
	if err == nil && l.cache[key] == nil {
		l.cache[key] = bitmap
		l.order = append(l.order, key)
		limit := 48
		if work.kind == Large {
			limit = 2
		}
		for countKind(l.order, work.kind) > limit {
			for index, old := range l.order {
				if len(old) > 0 && old[0] == key[0] {
					delete(l.cache, old)
					l.order = slices.Delete(l.order, index, index+1)
					break
				}
			}
		}
	}
	if err == nil && work.kind == Thumbnail {
		l.writes++
		if l.writes%200 == 0 && l.cacheDir != "" {
			l.workers.Add(1)
			go func() {
				defer l.workers.Done()
				pruneCache(l.cacheDir)
			}()
		}
	}
	l.mu.Unlock()
	result := Result{Path: work.path, Kind: work.kind, Bitmap: bitmap, Err: err}
	for _, callback := range callbacks {
		if callback != nil {
			callback(result)
		}
	}
}

func cacheKey(path string, kind Kind) string { return fmt.Sprintf("%d:%s", kind, path) }

func countKind(keys []string, kind Kind) int {
	count := 0
	prefix := fmt.Sprintf("%d:", kind)
	for _, key := range keys {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			count++
		}
	}
	return count
}

func decode(ctx context.Context, path string, kind Kind, cacheDir string) (*ui.Bitmap, error) {
	cachePath := ""
	if kind == Thumbnail && cacheDir != "" {
		if info, err := os.Stat(path); err == nil {
			key := fmt.Sprintf("%s:%d:%d", path, info.Size(), info.ModTime().UnixNano())
			hash := sha256.Sum256([]byte(key))
			cachePath = filepath.Join(cacheDir, hex.EncodeToString(hash[:])+".png")
			if cached, err := os.ReadFile(cachePath); err == nil {
				if bitmap, err := ui.DecodeBitmap(cached); err == nil {
					return bitmap, nil
				}
			}
		}
	}
	orientation, embedded := metadata.Preview(path)
	var decoded image.Image
	if kind == Thumbnail && len(embedded) > 0 {
		decoded, _, _ = image.Decode(bytes.NewReader(embedded))
	}
	if decoded == nil {
		file, err := photofile.Open(path)
		if err != nil {
			return nil, err
		}
		reader := contextReader{reader: file, context: ctx}
		decoded, _, err = image.Decode(&reader)
		_ = file.Close()
		if err != nil {
			return nil, err
		}
	}
	if ctx != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	maxSide := 192
	if kind == Large {
		maxSide = 1800
	}
	bounds := decoded.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("照片尺寸无效")
	}
	if width > maxSide || height > maxSide {
		scale := float64(maxSide) / float64(max(width, height))
		width = max(1, int(float64(width)*scale))
		height = max(1, int(float64(height)*scale))
	}
	scaled := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.ApproxBiLinear.Scale(scaled, scaled.Bounds(), decoded, bounds, draw.Src, nil)
	if ctx != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	upright := orient(scaled, orientation)
	if cachePath != "" {
		writeCache(cachePath, upright)
	}
	return ui.NewBitmap(upright), nil
}

type contextReader struct {
	reader  *os.File
	context context.Context
}

func (r *contextReader) Read(buffer []byte) (int, error) {
	if r.context != nil && r.context.Err() != nil {
		return 0, r.context.Err()
	}
	return r.reader.Read(buffer)
}

func writeCache(path string, photo image.Image) {
	file, err := os.CreateTemp(filepath.Dir(path), ".thumbnail-*.tmp")
	if err != nil {
		return
	}
	defer os.Remove(file.Name())
	if err := png.Encode(file, photo); err != nil {
		_ = file.Close()
		return
	}
	if err := file.Close(); err == nil {
		_ = os.Rename(file.Name(), path)
	}
}

func pruneCache(directory string) {
	const maxBytes = 512 << 20
	entries, err := os.ReadDir(directory)
	if err != nil {
		return
	}
	type cached struct {
		path string
		size int64
		age  int64
	}
	var files []cached
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".png" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, cached{path: filepath.Join(directory, entry.Name()), size: info.Size(), age: info.ModTime().UnixNano()})
		total += info.Size()
	}
	slices.SortFunc(files, func(a, b cached) int {
		if a.age < b.age {
			return -1
		}
		if a.age > b.age {
			return 1
		}
		return 0
	})
	for _, file := range files {
		if total <= maxBytes {
			break
		}
		if os.Remove(file.path) == nil {
			total -= file.size
		}
	}
}

func orient(source *image.RGBA, orientation int) image.Image {
	if orientation < 2 || orientation > 8 {
		return source
	}
	width, height := source.Bounds().Dx(), source.Bounds().Dy()
	outputWidth, outputHeight := width, height
	if orientation >= 5 {
		outputWidth, outputHeight = height, width
	}
	output := image.NewRGBA(image.Rect(0, 0, outputWidth, outputHeight))
	for y := range height {
		for x := range width {
			var targetX, targetY int
			switch orientation {
			case 2:
				targetX, targetY = width-1-x, y
			case 3:
				targetX, targetY = width-1-x, height-1-y
			case 4:
				targetX, targetY = x, height-1-y
			case 5:
				targetX, targetY = y, x
			case 6:
				targetX, targetY = height-1-y, x
			case 7:
				targetX, targetY = height-1-y, width-1-x
			case 8:
				targetX, targetY = y, width-1-x
			}
			output.Set(targetX, targetY, source.At(x, y))
		}
	}
	return output
}
