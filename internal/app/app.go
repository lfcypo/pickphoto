package app

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/lfcypo/pickphoto/internal/catalog"
	"github.com/lfcypo/pickphoto/internal/metadata"
	"github.com/lfcypo/pickphoto/internal/movefile"
	"github.com/lfcypo/pickphoto/internal/operation"
	"github.com/lfcypo/pickphoto/internal/preview"
)

type command struct {
	index  int
	target catalog.Status
}

type App struct {
	window           *mygo.Window
	store            *catalog.Store
	session          *catalog.Session
	loader           *preview.Loader
	index            int
	strip            ui.ScrollState
	large            *ui.Bitmap
	largeError       string
	largeLoading     bool
	thumbs           map[string]*ui.Bitmap
	thumbOrder       []string
	thumbErrors      map[string]string
	thumbRequested   map[string]bool
	fields           []metadata.Field
	exifList         ui.ListState
	metadataError    string
	metadataLoading  bool
	detailsClosed    bool
	statusText       string
	selectedCount    int
	rejectedCount    int
	queue            []command
	queued           map[int]catalog.Status
	busy             bool
	blocked          bool
	blockedConflict  bool
	blockError       string
	blockDialog      bool
	completionDialog bool
	completionShown  bool
	history          []int
	viewVersion      uint64
	folderVersion    uint64
	finished         bool
	largeCancel      context.CancelFunc
}

func New(store *catalog.Store) *App {
	cacheDir := ""
	if base, err := os.UserCacheDir(); err == nil {
		cacheDir = filepath.Join(base, "pickphoto", "thumbs")
	}
	return &App{store: store, loader: preview.NewLoader(cacheDir), thumbs: map[string]*ui.Bitmap{}, thumbErrors: map[string]string{}, thumbRequested: map[string]bool{}, queued: map[int]catalog.Status{}}
}

func (a *App) Close() error {
	if a.largeCancel != nil {
		a.largeCancel()
	}
	return a.loader.Close()
}

func (a *App) OpenWindow() {
	a.window = mygo.NewWindow(mygo.WindowOptions{
		Title: "PickPhoto", Width: 1200, Height: 780,
		MinWidth: 800, MinHeight: 560, StateKey: "main", Content: ui.View(a.view),
	})
}

func (a *App) view(c *ui.Context) {
	if a.session != nil && !a.blocked {
		if c.Shortcut(0, ui.KeyQ) {
			a.classify(catalog.Selected)
		}
		if c.Shortcut(0, ui.KeyP) {
			a.classify(catalog.Rejected)
		}
		if c.Shortcut(0, ui.KeyZ) {
			a.undo()
		}
		if c.Shortcut(0, ui.KeyLeft) || c.Shortcut(0, ui.KeyUp) {
			a.selectIndex(a.index - 1)
		}
		if c.Shortcut(0, ui.KeyRight) || c.Shortcut(0, ui.KeyDown) {
			a.selectIndex(a.index + 1)
		}
	}
	theme := c.Theme()
	ui.Column(c).Fill().Gap(12).Padding(16).Children(func() {
		a.toolbar(c)
		if a.session == nil {
			ui.Column(c).Grow(1).Center().Gap(12).Radius(16).Background(theme.Surface).Children(func() {
				ui.Text(c, "快速整理本地照片").FontSize(24).Bold()
				ui.Text(c, "选择照片文件夹后 用 「Q」 和 「P」 快捷键快速筛选").TextColor(theme.TextMuted)
				if ui.PrimaryButton(c, "选择文件夹").Clicked() {
					a.pickSource()
				}
			})
		} else {
			ui.Row(c).Grow(1).Gap(12).AlignItems(ui.Stretch).Children(func() {
				ui.Column(c).Grow(1).FillHeight().Gap(12).Children(func() {
					a.largeView(c)
					a.actionBar(c)
					a.filmstrip(c)
				})
				a.detailsView(c)
			})
		}
		if a.statusText != "" {
			ui.Text(c, a.statusText).FontSize(11).TextColor(theme.TextMuted).SingleLine()
		}
	})
	if a.blocked {
		a.blockDialog = true
	}
	ui.Modal(c, &a.blockDialog, func() {
		ui.Text(c, "照片移动已暂停").FontSize(18).Bold()
		ui.Text(c, a.blockError).Width(420)
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if ui.Button(c, "跳过这张").Clicked() {
				a.skipBlocked()
			}
			if a.blockedConflict {
				if ui.Button(c, "更换目标文件夹").Clicked() {
					a.pickTarget(a.queue[0].target, true)
				}
			}
			if ui.PrimaryButton(c, "重试").Clicked() {
				a.retryBlocked()
			}
		})
	})
	ui.Modal(c, &a.completionDialog, func() {
		ui.Text(c, "筛选完成").FontSize(18).Bold()
		ui.Textf(c, "共 %d 张照片", len(a.session.Photos)).TextColor(c.Theme().TextMuted)
		ui.Row(c).Gap(20).Children(func() {
			ui.Textf(c, "选用 %d 张", a.selectedCount)
			ui.Textf(c, "弃用 %d 张", a.rejectedCount)
			ui.Textf(c, "过片率 %.1f%%", passRate(a.selectedCount, a.rejectedCount))
		})
		ui.Row(c).Justify(ui.End).Children(func() {
			if ui.PrimaryButton(c, "继续查看").Clicked() {
				a.completionDialog = false
			}
		})
	})
}

func (a *App) toolbar(c *ui.Context) {
	ui.Row(c).Gap(12).AlignItems(ui.Center).Padding(0, 0, 10, 0).BorderWidth(0, 0, 1, 0).BorderColor(c.Theme().Border).Children(func() {
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			ui.Text(c, "PickPhoto").FontSize(21).Bold()
			if a.session == nil {
				ui.Text(c, "轻松整理你的照片").FontSize(12).TextColor(c.Theme().TextMuted)
			} else {
				ui.Text(c, a.session.SourceDir).FontSize(11).TextColor(c.Theme().TextMuted).SingleLine()
			}
		})
		if ui.Button(c, "打开照片文件夹").Disabled(a.busy || len(a.queue) > 0).Clicked() {
			a.pickSource()
		}
		if a.session != nil {
			if ui.Button(c, "选用目录").Clicked() {
				a.pickTarget(catalog.Selected, false)
			}
			if ui.Button(c, "弃用目录").Clicked() {
				a.pickTarget(catalog.Rejected, false)
			}
		}
	})
}

func (a *App) actionBar(c *ui.Context) {
	ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(4, 0).Children(func() {
		if ui.Button(c, "上一张").Disabled(a.index <= 0).Clicked() {
			a.selectIndex(a.index - 1)
		}
		if ui.Button(c, "下一张").Disabled(a.index >= len(a.session.Photos)-1).Clicked() {
			a.selectIndex(a.index + 1)
		}
		ui.Box(c).Grow(1)
		if ui.PrimaryButton(c, "选用  Q").Disabled(len(a.session.Photos) == 0 || a.blocked || a.finished).Clicked() {
			a.classify(catalog.Selected)
		}
		if ui.Button(c, "弃用  P").Disabled(len(a.session.Photos) == 0 || a.blocked || a.finished).Clicked() {
			a.classify(catalog.Rejected)
		}
		if ui.Button(c, "撤销  Z").Disabled(len(a.history) == 0 || a.busy || len(a.queue) > 0).Clicked() {
			a.undo()
		}
		ui.Box(c).Grow(1)
		if a.finished {
			ui.Text(c, "全部已处理").TextColor(c.Theme().TextMuted)
		}
	})
}

func (a *App) largeView(c *ui.Context) {
	ui.Column(c).Grow(1).Gap(10).Children(func() {
		if len(a.session.Photos) == 0 {
			ui.Box(c).Grow(1).Center().Radius(12).Background(c.Theme().Surface).Children(func() {
				ui.Text(c, "这个文件夹没有可筛选的照片").TextColor(c.Theme().TextMuted)
			})
			return
		}
		photo := a.session.Photos[a.index]
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, photo.Name()).FontSize(16).Bold().SingleLine().Grow(1)
			if a.largeLoading {
				ui.Text(c, "正在载入高清预览").FontSize(11).TextColor(c.Theme().TextMuted)
			}
			ui.Text(c, statusLabel(photo.Status)).FontSize(11).Padding(4, 9).Radius(999).Background(statusColor(c, photo.Status).Alpha(0.15)).TextColor(statusColor(c, photo.Status))
			ui.Textf(c, "%d / %d", a.index+1, len(a.session.Photos)).FontSize(12).TextColor(c.Theme().TextMuted)
		})
		ui.Box(c).Grow(1).Center().Radius(12).Background(ui.Hex("#171A20")).Children(func() {
			switch {
			case a.large != nil:
				ui.Image(c, a.large).Fill().Fit(ui.Contain).Label(photo.Name())
			case a.largeError != "":
				ui.Text(c, "预览失败: "+a.largeError).TextColor(ui.Hex("#FFB4AB"))
			case a.thumbs[photo.OriginalPath] != nil:
				ui.Image(c, a.thumbs[photo.OriginalPath]).Fill().Fit(ui.Contain).Label(photo.Name() + "的小图预览")
			case a.largeLoading:
				ui.Text(c, "正在加载照片…").TextColor(ui.Hex("#CED3DD"))
			default:
				ui.Text(c, "暂无预览").TextColor(ui.Hex("#CED3DD"))
			}
		})
	})
}

func (a *App) detailsView(c *ui.Context) {
	if a.detailsClosed {
		ui.Column(c).Width(42).FillHeight().Padding(4).Radius(12).Background(c.Theme().Surface).Children(func() {
			if ui.Button(c, "‹").Tooltip("展开照片信息").Clicked() {
				a.detailsClosed = false
			}
		})
		return
	}
	ui.Column(c).Width(280).FillHeight().Gap(10).Children(func() {
		a.progressView(c)
		a.exifView(c)
	})
}

func (a *App) progressView(c *ui.Context) {
	total := len(a.session.Photos)
	processed := a.selectedCount + a.rejectedCount
	ui.Column(c).Padding(12).Gap(7).Radius(12).Background(c.Theme().Surface).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "筛选进度").FontSize(13).Bold().Grow(1)
			ui.Textf(c, "%d / %d", processed, total).FontSize(11).TextColor(c.Theme().TextMuted)
		})
		progress := 0.0
		if total > 0 {
			progress = float64(processed) / float64(total)
		}
		ui.Progress(c, progress).Label("筛选进度")
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "过片率").FontSize(11).TextColor(c.Theme().TextMuted).Grow(1)
			ui.Textf(c, "%.1f%%", passRate(a.selectedCount, a.rejectedCount)).FontSize(15).Bold().TextColor(c.Theme().Success)
		})
		ui.Row(c).Gap(8).Children(func() {
			ui.Textf(c, "选用 %d", a.selectedCount).FontSize(11).TextColor(statusColor(c, catalog.Selected))
			ui.Textf(c, "弃用 %d", a.rejectedCount).FontSize(11).TextColor(statusColor(c, catalog.Rejected))
			ui.Textf(c, "待定 %d", total-processed).FontSize(11).TextColor(c.Theme().TextMuted)
		})
	})
}

func (a *App) exifView(c *ui.Context) {
	ui.Column(c).Grow(1).Padding(12).Gap(8).Radius(12).Background(c.Theme().Surface).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Text(c, "EXIF 信息").FontSize(15).Bold().Grow(1)
			ui.Textf(c, "%d 项", len(a.fields)).FontSize(11).TextColor(c.Theme().TextMuted)
			if ui.Button(c, "›").Tooltip("收起照片信息").Clicked() {
				a.detailsClosed = true
			}
		})
		ui.Text(c, "常用参数优先显示 · 点击字段或内容复制").FontSize(10).TextColor(c.Theme().TextMuted)
		if len(a.session.Photos) == 0 {
			ui.Text(c, "暂无照片").FontSize(12).TextColor(c.Theme().TextMuted)
			return
		}
		photo := a.session.Photos[a.index]
		if photo.Problem != "" {
			ui.Text(c, photo.Problem).FontSize(11).TextColor(c.Theme().Danger)
		}
		if a.metadataError != "" {
			ui.Text(c, "EXIF 读取失败: "+a.metadataError).FontSize(11).TextColor(c.Theme().Danger)
		} else if a.metadataLoading {
			ui.Text(c, "正在读取 EXIF…").FontSize(12).TextColor(c.Theme().TextMuted)
		} else if len(a.fields) == 0 && photo.Problem == "" {
			ui.Text(c, "这张照片没有 EXIF 信息").FontSize(12).TextColor(c.Theme().TextMuted)
		}
		ui.List(c, &a.exifList, len(a.fields), func(index int) {
			a.exifField(c, a.fields[index])
		}).Grow(1)
	})
}

func (a *App) exifField(c *ui.Context, field metadata.Field) {
	label := exifLabel(field.Name)
	row := ui.Column(c).Padding(7, 4).Gap(3).BorderWidth(0, 0, 1, 0).BorderColor(c.Theme().Border)
	if row.Hovered() {
		row.Background(c.Theme().SurfaceHover)
	}
	row.Children(func() {
		if ui.Text(c, label).FontSize(12).Bold().SingleLine().Tooltip(field.Name + " · 点击复制字段名").Cursor(ui.CursorPointer).Clicked() {
			c.WriteClipboard(label)
			a.statusText = "已复制字段名 " + label
		}
		if ui.Text(c, field.Value).FontSize(12).SingleLine().Tooltip(field.Value + " · 点击复制内容").Cursor(ui.CursorPointer).Clicked() {
			c.WriteClipboard(field.Value)
			a.statusText = "已复制 " + label
		}
	})
}

func passRate(selected, rejected int) float64 {
	processed := selected + rejected
	if processed == 0 {
		return 0
	}
	return float64(selected) * 100 / float64(processed)
}

func (a *App) filmstrip(c *ui.Context) {
	const itemWidth = 122
	width, _ := c.Size()
	visibleItems := max(8, int(math.Ceil(float64(width)/itemWidth))+6)
	count := len(a.session.Photos)
	start := max(0, int(a.strip.X/itemWidth)-3)
	end := min(count, start+visibleItems)
	ui.Column(c).Gap(8).Padding(10).Radius(12).Background(c.Theme().Surface).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "照片胶片").FontSize(13).Bold().Grow(1)
			ui.Textf(c, "共 %d 张  ·  点击小图切换", count).FontSize(11).TextColor(c.Theme().TextMuted)
		})
		ui.ScrollHorizontal(c).TrackScroll(&a.strip).Height(124).Children(func() {
			ui.Row(c).Gap(0).Children(func() {
				if start > 0 {
					ui.Box(c).Width(float32(start * itemWidth)).Height(116).Shrink(0)
				}
				for index := start; index < end; index++ {
					a.thumbnail(c, index)
				}
				if end < count {
					ui.Box(c).Width(float32((count - end) * itemWidth)).Height(116).Shrink(0)
				}
			})
		})
	})
}

func (a *App) thumbnail(c *ui.Context, index int) {
	photo := a.session.Photos[index]
	key := photo.OriginalPath
	a.ensureThumbnail(key, photo.CurrentPath, false)
	box := ui.Column(c).Width(122).Height(116).Shrink(0).Padding(4).Gap(3).Radius(8).Background(c.Theme().Background)
	if index == a.index {
		box.Border(2, c.Theme().Accent)
	}
	box.Children(func() {
		ui.Box(c).Width(112).Height(76).Center().Radius(5).Background(ui.Hex("#242933")).Children(func() {
			if bitmap := a.thumbs[key]; bitmap != nil {
				ui.Image(c, bitmap).Width(112).Height(76).Fit(ui.Contain)
			} else if a.thumbErrors[key] != "" {
				ui.Text(c, "无法预览").FontSize(10).TextColor(ui.Hex("#FFB4AB"))
			} else {
				ui.Text(c, "加载中").FontSize(10).TextColor(ui.Hex("#CED3DD"))
			}
		})
		status := photo.Status
		if queued, found := a.queued[index]; found {
			status = queued
		}
		ui.Text(c, photo.Name()).FontSize(10).SingleLine()
		ui.Text(c, statusLabel(status)).FontSize(10).TextColor(statusColor(c, status))
	})
	if box.Clicked() {
		a.selectIndex(index)
	}
}

func statusColor(c *ui.Context, status catalog.Status) ui.Color {
	switch status {
	case catalog.Selected:
		return c.Theme().Success
	case catalog.Rejected:
		return c.Theme().Danger
	default:
		return c.Theme().TextMuted
	}
}

func statusLabel(status catalog.Status) string {
	switch status {
	case catalog.Selected:
		return "选用"
	case catalog.Rejected:
		return "弃用"
	default:
		return "待定"
	}
}

func (a *App) pickSource() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.window, Title: "选择照片文件夹", Directory: true})
		if err != nil || len(paths) == 0 {
			return
		}
		session, err := catalog.LoadFolder(a.store, paths[0])
		a.window.Update(func() {
			if err != nil {
				a.statusText = "打开失败: " + err.Error()
				return
			}
			if a.busy || len(a.queue) > 0 {
				a.statusText = "当前分类尚未完成 请稍后再打开文件夹"
				return
			}
			a.session = session
			a.folderVersion++
			a.viewVersion++
			if a.largeCancel != nil {
				a.largeCancel()
			}
			a.index = 0
			a.queue = nil
			a.queued = map[int]catalog.Status{}
			a.history = nil
			a.busy, a.blocked, a.blockDialog, a.finished = false, false, false, false
			a.completionDialog, a.completionShown = false, false
			a.thumbs = map[string]*ui.Bitmap{}
			a.thumbOrder = nil
			a.thumbErrors = map[string]string{}
			a.thumbRequested = map[string]bool{}
			a.selectedCount, a.rejectedCount = 0, 0
			foundWaiting := false
			for index, photo := range session.Photos {
				switch photo.Status {
				case catalog.Selected:
					a.selectedCount++
				case catalog.Rejected:
					a.rejectedCount++
				case catalog.Waiting:
					if !foundWaiting {
						a.index = index
						foundWaiting = true
					}
				}
			}
			a.strip.X = 0
			a.statusText = "已打开 " + session.SourceDir
			if len(session.Photos) > 0 {
				a.selectIndex(a.index)
			}
			a.finished = !foundWaiting && len(session.Photos) > 0
			a.completionShown = a.finished
		})
	}()
}

func (a *App) pickTarget(target catalog.Status, retry bool) {
	if a.session == nil {
		return
	}
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.window, Title: "选择" + statusLabel(target) + "文件夹", Directory: true, CreateDirectories: true})
		if err != nil || len(paths) == 0 {
			return
		}
		a.window.Update(func() {
			selected, rejected := a.session.SelectedDir, a.session.RejectedDir
			if target == catalog.Selected {
				selected = paths[0]
			} else {
				rejected = paths[0]
			}
			if err := catalog.ValidTargets(a.session.SourceDir, selected, rejected); err != nil {
				a.statusText = err.Error()
				return
			}
			oldSelected, oldRejected := a.session.SelectedDir, a.session.RejectedDir
			a.session.SelectedDir, a.session.RejectedDir = selected, rejected
			if err := a.store.Save(a.session); err != nil {
				a.session.SelectedDir, a.session.RejectedDir = oldSelected, oldRejected
				a.statusText = "保存目标目录失败: " + err.Error()
				return
			}
			a.statusText = statusLabel(target) + "目录已设为 " + paths[0]
			if retry && a.blocked {
				a.blocked, a.blockDialog = false, false
				a.startNext()
			}
		})
	}()
}

func (a *App) selectIndex(index int) {
	if a.session == nil || index < 0 || index >= len(a.session.Photos) {
		return
	}
	a.index = index
	a.finished = false
	if a.largeCancel != nil {
		a.largeCancel()
	}
	previewContext, cancel := context.WithCancel(context.Background())
	a.largeCancel = cancel
	a.strip.X = float32(math.Max(0, float64(index*122-480)))
	a.viewVersion++
	version := a.viewVersion
	photo := a.session.Photos[index]
	a.large, a.largeError, a.largeLoading = nil, "", true
	a.fields, a.metadataError, a.metadataLoading = nil, "", true
	if photo.Problem != "" {
		a.largeError, a.largeLoading = photo.Problem, false
		a.metadataLoading = false
		return
	}
	path := photo.CurrentPath
	a.ensureThumbnail(photo.OriginalPath, path, true)
	if !a.loader.RequestLarge(previewContext, path, func(result preview.Result) {
		a.window.Update(func() {
			if a.viewVersion != version {
				return
			}
			a.large, a.largeLoading = result.Bitmap, false
			if result.Err != nil {
				a.largeError = result.Err.Error()
			}
		})
	}) {
		a.largeError, a.largeLoading = "预览队列繁忙 请稍后切换回来", false
	}
	go func() {
		data, err := metadata.Read(path)
		a.window.Update(func() {
			if a.viewVersion != version {
				return
			}
			a.fields = presentEXIF(data.Fields)
			a.exifList.ScrollTo(0, ui.Start)
			a.metadataLoading = false
			if err != nil {
				a.metadataError = err.Error()
			}
		})
	}()
}

func (a *App) ensureThumbnail(original, current string, priority bool) {
	if a.thumbs[original] != nil || a.thumbErrors[original] != "" {
		return
	}
	if a.thumbRequested[original] {
		if priority {
			a.loader.RequestPriorityThumbnail(current, func(preview.Result) {})
		}
		return
	}
	a.thumbRequested[original] = true
	folderVersion := a.folderVersion
	callback := func(result preview.Result) {
		a.window.Update(func() {
			if a.folderVersion != folderVersion {
				return
			}
			delete(a.thumbRequested, original)
			if result.Err != nil {
				a.thumbErrors[original] = result.Err.Error()
			} else {
				a.thumbs[original] = result.Bitmap
				a.thumbOrder = append(a.thumbOrder, original)
				for len(a.thumbOrder) > 64 {
					delete(a.thumbs, a.thumbOrder[0])
					a.thumbOrder = a.thumbOrder[1:]
				}
			}
		})
	}
	requested := false
	if priority {
		requested = a.loader.RequestPriorityThumbnail(current, callback)
	} else {
		requested = a.loader.Request(current, preview.Thumbnail, callback)
	}
	if !requested {
		delete(a.thumbRequested, original)
	}
}

func (a *App) classify(target catalog.Status) {
	if a.session == nil || len(a.session.Photos) == 0 || a.blocked || a.finished {
		return
	}
	if a.session.Photos[a.index].Status == target || a.queued[a.index] != "" {
		return
	}
	a.queue = append(a.queue, command{index: a.index, target: target})
	a.queued[a.index] = target
	a.advance()
	a.startNext()
}

func (a *App) advance() {
	for index := a.index + 1; index < len(a.session.Photos); index++ {
		if a.session.Photos[index].Status == catalog.Waiting && a.queued[index] == "" {
			a.selectIndex(index)
			return
		}
	}
	for index := 0; index < len(a.session.Photos); index++ {
		if a.session.Photos[index].Status == catalog.Waiting && a.queued[index] == "" {
			a.selectIndex(index)
			return
		}
	}
	a.finished = true
}

func (a *App) undo() {
	if a.busy || len(a.queue) > 0 || len(a.history) == 0 {
		return
	}
	index := a.history[len(a.history)-1]
	a.history = a.history[:len(a.history)-1]
	a.completionDialog = false
	a.queue = append(a.queue, command{index: index, target: catalog.Waiting})
	a.queued[index] = catalog.Waiting
	a.startNext()
}

func (a *App) startNext() {
	if a.busy || a.blocked || len(a.queue) == 0 || a.session == nil {
		return
	}
	command := a.queue[0]
	photo := a.session.Photos[command.index]
	targetDir := a.session.SourceDir
	if command.target == catalog.Selected {
		targetDir = a.session.SelectedDir
	} else if command.target == catalog.Rejected {
		targetDir = a.session.RejectedDir
	}
	if err := catalog.ValidTargets(a.session.SourceDir, a.session.SelectedDir, a.session.RejectedDir); err != nil {
		a.pause(err)
		return
	}
	a.busy = true
	source := a.session.SourceDir
	go func() {
		updated, err := operation.Execute(a.store, source, photo, targetDir, command.target)
		a.window.Update(func() { a.finish(command, updated, err) })
	}()
}

func (a *App) finish(operation command, updated catalog.Photo, moveError error) {
	a.busy = false
	photo := &a.session.Photos[operation.index]
	oldStatus := photo.Status
	*photo = updated
	if oldStatus == catalog.Selected {
		a.selectedCount--
	} else if oldStatus == catalog.Rejected {
		a.rejectedCount--
	}
	if photo.Status == catalog.Selected {
		a.selectedCount++
	} else if photo.Status == catalog.Rejected {
		a.rejectedCount++
	}
	if moveError != nil {
		if operation.target == catalog.Waiting {
			a.history = append(a.history, operation.index)
		}
		a.pause(moveError)
		return
	}
	if operation.target != catalog.Waiting {
		for index := len(a.history) - 1; index >= 0; index-- {
			if a.history[index] == operation.index {
				a.history = append(a.history[:index], a.history[index+1:]...)
			}
		}
		a.history = append(a.history, operation.index)
	}
	a.statusText = photo.Name() + " 已移入" + statusLabel(operation.target)
	a.queue = a.queue[1:]
	delete(a.queued, operation.index)
	if operation.target == catalog.Waiting || a.index == operation.index {
		a.selectIndex(operation.index)
	}
	if operation.target == catalog.Waiting {
		a.completionDialog, a.completionShown, a.finished = false, false, false
	} else {
		a.showCompletionIfReady()
	}
	a.startNext()
}

func (a *App) showCompletionIfReady() {
	if a.completionShown || len(a.queue) > 0 || len(a.session.Photos) == 0 {
		return
	}
	for _, photo := range a.session.Photos {
		if photo.Status == catalog.Waiting {
			return
		}
	}
	a.finished = true
	a.completionShown = true
	a.completionDialog = true
	a.statusText = "筛选完成"
}

func (a *App) pause(err error) {
	a.blocked, a.blockDialog = true, true
	a.blockedConflict = errors.Is(err, movefile.ErrConflict)
	if a.blockedConflict {
		a.blockError = "目标文件夹已有同名照片。请选择跳过或更换目标文件夹。"
	} else if movefile.IsBusy(err) {
		a.blockError = "照片正被其他程序使用。请关闭占用它的程序后重试。\n" + err.Error()
	} else {
		a.blockError = err.Error()
	}
	a.statusText = a.blockError
}

func (a *App) retryBlocked() {
	a.blocked, a.blockDialog = false, false
	a.startNext()
}

func (a *App) skipBlocked() {
	if len(a.queue) == 0 {
		return
	}
	index := a.queue[0].index
	a.queue = a.queue[1:]
	delete(a.queued, index)
	a.blocked, a.blockDialog = false, false
	a.finished = false
	a.startNext()
}
