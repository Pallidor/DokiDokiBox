// Package app 是程序的运行状态：当前直播间、统计、OBS 叠加层设置、扫码登录流程、
// 演示模式，以及主播后台和叠加层用到的网页接口。
package app

import (
	"context"
	"encoding/json"
	"github.com/Pallidor/DokiDokiBox/internal/bili"
	"github.com/Pallidor/DokiDokiBox/internal/config"
	"github.com/Pallidor/DokiDokiBox/internal/store"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// Version 是程序版本号，由 main 设置，显示在主播后台底部。
var Version = "dev"

// App 保存运行时状态，并把新事件推送给网页（SSE）。
type App struct {
	cfg     *config.Config
	store   atomic.Pointer[store.Store] // 当前直播间的数据，切换直播间时整体替换
	dataDir string                      // data/ 目录：各直播间的数据在 room-<房间号>/ 子目录，叠加层设置在这里
	roomDir int64                       // 当前数据目录对应的房间号，示例模式为 0
	hub     *Hub
	demo    bool

	mu        sync.RWMutex
	status    string // connecting / connected / error / demo
	statusMsg string
	roomID    int64
	realRoom  int64
	title     string
	live      bool
	lastEvent int64
	liveSeen  int64 // 本次运行中确认在播的场次 ID
	room      bili.RoomInfo
	overlay   OverlaySettings
	login     string // 已登录的 B 站账号昵称，未登录为空
	qrKey     string // 扫码登录进行中的二维码
	qrURL     string

	// 程序控制（见 control.go），由 main 设置
	ctx        context.Context
	quit       context.CancelFunc
	cfgPath    string
	clientMu   sync.Mutex
	clientStop context.CancelFunc
	clientDone chan struct{}
}

// New 创建运行状态并打开当前直播间的数据（data/room-<房间号>/，旧版本的数据会自动迁移过来）。
// 示例模式下每次都重新生成 dataDir 里的模拟数据。
func New(cfg *config.Config, cfgPath, dataDir string, demo bool) (*App, error) {
	storeDir, room := dataDir, int64(0)
	if demo {
		os.RemoveAll(dataDir)
	} else if room = cfg.RoomID; room > 0 {
		storeDir = store.RoomDir(dataDir, room)
		store.MigrateLegacy(dataDir, storeDir)
	} else {
		storeDir = "" // 还没设置直播间：先只放在内存里，在网页上设置后切换到该直播间的目录
	}
	s, err := store.Open(storeDir)
	if err != nil {
		return nil, err
	}
	a := &App{cfg: cfg, cfgPath: cfgPath, hub: NewHub(), demo: demo, dataDir: dataDir, roomDir: room, status: "connecting", statusMsg: "正在启动…"}
	if demo {
		a.status, a.statusMsg = "demo", "示例模式 · 未连接 B 站"
	}
	a.overlay = loadOverlaySettings(dataDir)
	a.setStore(s)
	return a, nil
}

// Start 开始连接直播间（示例模式下开始生成模拟数据）。quit 由网页上的「关闭程序」调用。
func (a *App) Start(ctx context.Context, quit context.CancelFunc) {
	a.ctx, a.quit = ctx, quit
	if a.demo {
		go runDemo(ctx, a)
		return
	}
	a.startClient(a.cfg.RoomID)
}

// Close 关闭当前直播间的数据文件，程序退出前调用。
func (a *App) Close() { a.st().Close() }

// AppendRaw、AppendDebug 实现 bili.Sink：保存原始礼物消息到当前直播间的目录。
func (a *App) AppendRaw(body []byte)   { a.st().AppendRaw(body) }
func (a *App) AppendDebug(body []byte) { a.st().AppendDebug(body) }

func (a *App) st() *store.Store { return a.store.Load() }

func (a *App) setStore(s *store.Store) {
	a.store.Store(s)
	var last int64
	if evs := s.Range(0, 1<<62); len(evs) > 0 {
		last = evs[len(evs)-1].TS
	}
	a.mu.Lock()
	a.lastEvent = last
	a.mu.Unlock()
}

// useRoomStore 切换到指定直播间的数据（每个直播间的记录和场次分开保存）。
func (a *App) useRoomStore(room int64) {
	if a.demo || room <= 0 {
		return
	}
	a.mu.Lock()
	if a.roomDir == room {
		a.mu.Unlock()
		return
	}
	a.roomDir = room
	a.mu.Unlock()
	s, err := store.Open(store.RoomDir(a.dataDir, room))
	if err != nil {
		log.Printf("打开直播间 %d 的数据失败：%v", room, err)
		return
	}
	old := a.st()
	a.setStore(s)
	if old != nil {
		old.Close()
	}
	a.hub.Broadcast("status", map[string]int64{"store": room})
}

func (a *App) SetStatus(status, msg string) {
	a.mu.Lock()
	changed := a.status != status || a.statusMsg != msg
	a.status, a.statusMsg = status, msg
	a.mu.Unlock()
	if changed {
		a.hub.Broadcast("status", map[string]string{"status": status, "msg": msg})
	}
}

func (a *App) SetRoomID(input, real int64) {
	a.mu.Lock()
	a.roomID, a.realRoom = input, real
	a.mu.Unlock()
	if real > 0 {
		a.useRoomStore(real)
	}
}

// UpdateRoom 修改直播间资料（字段为零值表示这次没取到，保留原值）。
func (a *App) UpdateRoom(r bili.RoomInfo) {
	a.mu.Lock()
	defer a.mu.Unlock()
	old := &a.room
	if r.UID != 0 {
		old.UID = r.UID
	}
	if r.Uname != "" {
		old.Uname, old.Face, old.Level = r.Uname, r.Face, r.Level
	}
	if r.Area != "" {
		old.Area, old.Online = r.Area, r.Online
	}
	if r.Followers != 0 {
		old.Followers = r.Followers
	}
}

func (a *App) SetRoomInfo(title string, live bool) {
	a.mu.Lock()
	changed := a.live != live
	if title != "" {
		a.title = title
	}
	a.live = live
	a.mu.Unlock()
	if changed {
		a.hub.Broadcast("status", map[string]bool{"live": live})
	}
}

func (a *App) LiveStarted(start int64, title string) {
	id := a.st().StartSession(start, title)
	a.mu.Lock()
	a.liveSeen = id
	a.live = true
	if title != "" {
		a.title = title
	}
	a.mu.Unlock()
}

func (a *App) LiveEnded() {
	open, ok := a.st().OpenSession()
	if !ok {
		return
	}
	a.mu.RLock()
	stale := a.liveSeen != open.ID
	a.mu.RUnlock()
	if a.st().EndSession(time.Now().UnixMilli(), stale) {
		a.hub.Broadcast("status", map[string]bool{"live": false})
	}
}

func (a *App) AddEvent(e store.Event) {
	if !a.st().Add(e) {
		return
	}
	a.mu.Lock()
	a.lastEvent = max(a.lastEvent, e.TS)
	a.mu.Unlock()
	a.st().TouchOffline(e.TS)           // 没开播时也记进「未开播」时段，「本场」里能看到
	a.hub.Broadcast("gift", struct{}{}) // 网页收到后重新拉取统计

}

func battery(gold int64) float64 { return float64(gold) / 100 }

// ---------- SSE 推送 ----------

type Hub struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

func NewHub() *Hub { return &Hub{subs: map[chan []byte]struct{}{}} }

func (h *Hub) Subscribe() chan []byte {
	ch := make(chan []byte, 64)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *Hub) Unsubscribe(ch chan []byte) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

func (h *Hub) Broadcast(event string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	msg := []byte("event: " + event + "\ndata: " + string(b) + "\n\n")
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- msg:
		default: // 客户端太慢就丢弃，网页会定时全量刷新
		}
	}
}
