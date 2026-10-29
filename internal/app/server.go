package app

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Routes 返回网页服务：主播后台、OBS 叠加层页面和它们用到的接口。static 是 web/ 目录的内容。
func (a *App) Routes(static fs.FS) http.Handler {
	page := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFileFS(w, r, static, name)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", page("dashboard.html"))
	mux.HandleFunc("GET /overlays", page("overlays.html"))
	mux.HandleFunc("GET /overlay/stats", page("overlay-stats.html"))
	mux.Handle("GET /static/", http.StripPrefix("/static/", noCache(http.FileServerFS(static))))

	mux.HandleFunc("GET /api/stats", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		writeJSON(w, a.computeStats(q.Get("range"), q.Get("box"), q.Get("sort"), q.Get("bigOnly") == "1"))
	})
	mux.HandleFunc("GET /api/overlay", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, a.computeOverlay())
	})
	mux.HandleFunc("GET /api/events", a.handleSSE)
	mux.HandleFunc("GET /api/export.csv", a.handleExport)
	mux.HandleFunc("GET /api/overlay-settings", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, a.OverlaySettings())
	})
	mux.HandleFunc("PUT /api/overlay-settings", a.handleOverlaySettings)
	mux.HandleFunc("PUT /api/room", a.handleRoom)
	mux.HandleFunc("POST /api/shutdown", a.handleShutdown)
	mux.HandleFunc("POST /api/login/start", a.handleLoginStart)
	mux.HandleFunc("GET /api/login/qr.png", a.handleLoginQR)
	mux.HandleFunc("POST /api/login/poll", a.handleLoginPoll)
	mux.HandleFunc("POST /api/logout", a.handleLogout)
	return mux
}

// localJSON 检查修改类请求来自本程序自己的网页：要求 application/json 并核对 Origin，
// 这样其他网站无法跨站修改设置或关闭程序。
func localJSON(w http.ResponseWriter, r *http.Request) bool {
	if o := r.Header.Get("Origin"); o != "" && o != "http://"+r.Host {
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		http.Error(w, "需要 JSON", http.StatusUnsupportedMediaType)
		return false
	}
	return true
}

// handleOverlaySettings 保存叠加层设置。
func (a *App) handleOverlaySettings(w http.ResponseWriter, r *http.Request) {
	if !localJSON(w, r) {
		return
	}
	s := a.OverlaySettings()
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&s); err != nil {
		http.Error(w, "设置格式错误", http.StatusBadRequest)
		return
	}
	s, err := a.SetOverlaySettings(s)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, s)
}

// handleRoom 切换直播间，请求体为 {"room": "直播间号或链接"}。
func (a *App) handleRoom(w http.ResponseWriter, r *http.Request) {
	if !localJSON(w, r) {
		return
	}
	var body struct {
		Room string `json:"room"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
		http.Error(w, "请求格式错误", http.StatusBadRequest)
		return
	}
	room, ok := parseRoomInput(body.Room)
	if !ok {
		http.Error(w, "直播间号应为数字，也可以粘贴直播间链接", http.StatusBadRequest)
		return
	}
	if err := a.SwitchRoom(r.Context(), room); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if !localJSON(w, r) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
	a.Shutdown()
}

func (a *App) handleLoginStart(w http.ResponseWriter, r *http.Request) {
	if !localJSON(w, r) {
		return
	}
	if err := a.LoginStart(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handleLoginQR(w http.ResponseWriter, r *http.Request) {
	png, err := a.LoginQR()
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(png)
}

func (a *App) handleLoginPoll(w http.ResponseWriter, r *http.Request) {
	if !localJSON(w, r) {
		return
	}
	state, uname, err := a.LoginPoll(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]string{"state": state, "uname": uname})
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	if !localJSON(w, r) {
		return
	}
	if err := a.SetCookie(""); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("已退出 B 站登录")
	w.WriteHeader(http.StatusNoContent)
}

func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		h.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

func (a *App) handleSSE(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Connection", "keep-alive")

	ch := a.hub.Subscribe()
	defer a.hub.Unsubscribe(ch)
	fmt.Fprint(w, "retry: 3000\n\n")
	fl.Flush()

	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-ch:
			w.Write(msg)
			fl.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		}
	}
}

func (a *App) handleExport(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("range")
	if _, ok := rangeLabels[key]; !ok {
		key = "session"
	}
	evs, _ := a.rangeEvents(key)
	label := rangeLabels[key]
	if box := r.URL.Query().Get("box"); box != "" {
		evs = filterBox(evs, box)
		label += "-" + box
	}
	name := fmt.Sprintf("盲盒记录-%s-%s.csv", label, time.Now().Format("20060102-1504"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+urlPathEscape(name))
	w.Write([]byte{0xEF, 0xBB, 0xBF}) // 让 Excel 正确识别 UTF-8

	cw := csv.NewWriter(w)
	cw.Write([]string{"时间", "用户UID", "用户名", "盲盒", "盲盒单价(电池)", "开出礼物", "礼物单价(电池)", "数量", "投入(电池)", "开出价值(电池)", "盈亏(电池)"})
	f := func(gold int64) string { return strconv.FormatFloat(battery(gold), 'f', -1, 64) }
	for _, e := range evs {
		n := int64(e.Num)
		cw.Write([]string{
			time.UnixMilli(e.TS).Format("2006-01-02 15:04:05"),
			strconv.FormatInt(e.UID, 10), e.Uname, e.Box, f(e.BoxPrice), e.Gift, f(e.GiftPrice),
			strconv.Itoa(e.Num), f(e.BoxPrice * n), f(e.GiftPrice * n), f((e.GiftPrice - e.BoxPrice) * n),
		})
	}
	cw.Flush()
}

func urlPathEscape(s string) string {
	const hex = "0123456789ABCDEF"
	out := make([]byte, 0, len(s)*3)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_' {
			out = append(out, c)
		} else {
			out = append(out, '%', hex[c>>4], hex[c&15])
		}
	}
	return string(out)
}
