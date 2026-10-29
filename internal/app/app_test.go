package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Pallidor/DokiDokiBox/internal/config"
	"github.com/Pallidor/DokiDokiBox/internal/store"
)

func newTestApp(t *testing.T, room int64) *App {
	cfg := config.Default()
	cfg.RoomID = room
	a, err := New(cfg, "", t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a
}

func box(id string, ts int64, user, gift string, price int64, num int) store.Event {
	return store.Event{ID: id, TS: ts, Uname: user, Box: "心动盲盒", BoxPrice: 15000, Gift: gift, GiftPrice: price, Num: num}
}

func TestStatsAndSessions(t *testing.T) {
	app := newTestApp(t, 0)
	app.LiveStarted(1000, "测试")
	add := func(id string, ts int64, user string, gift int64, num int) {
		app.AddEvent(box(id, ts, user, "g"+id, gift, num))
	}
	add("1", 2000, "a", 5000, 3)    // 3 盒小奖
	add("2", 3000, "b", 200000, 1)  // 大奖
	add("3", 4000, "a", 10000, 2)   // 2 盒
	add("4", 500, "early", 5000, 1) // 开播前，不计入本场

	r := app.computeStats("session", "", "net", false)
	s := r.Summary
	if s.Boxes != 6 || s.Users != 2 || s.Spent != 900 || s.Got != 2350 || s.Net != 1450 {
		t.Fatalf("summary %+v", s)
	}
	if r.BigCount != 1 || r.SinceBig != 2 || r.LongestDrought != 3 || !r.HasBig {
		t.Fatalf("droughts big=%d since=%d longest=%d", r.BigCount, r.SinceBig, r.LongestDrought)
	}
	if r.Senders[0].Name != "b" || r.Senders[0].Net != 1850 {
		t.Fatalf("senders %+v", r.Senders)
	}
	if r.Tiers[0].Value != 2000 || r.Feed[0].Delta != -100 {
		t.Fatalf("tiers %+v feed %+v", r.Tiers[0], r.Feed[0])
	}

	// 重启时若已下播，结束时间取最后一条记录
	app.liveSeen = 0
	app.LiveEnded()
	cur, _ := app.st().CurrentSession()
	if cur.End != 4001 {
		t.Fatalf("stale session end = %d", cur.End)
	}
	// 同一场重新检测到开播（时间相近）应复用场次
	app.LiveStarted(1000+60_000, "")
	if n := len(app.st().Sessions()); n != 1 {
		t.Fatalf("want 1 session, got %d", n)
	}
}

func TestOverlaySettings(t *testing.T) {
	app := newTestApp(t, 0)
	h := app.Routes(fstest.MapFS{})
	put := func(body, origin, ctype string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "http://127.0.0.1:11124/api/overlay-settings", strings.NewReader(body))
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		req.Header.Set("Content-Type", ctype)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}

	// 越界取值会被修正，未提交的字段保持原值
	if w := put(`{"accent":"#ff8000","opacity":3,"title":"  ","showLucky":false}`, "http://127.0.0.1:11124", "application/json"); w.Code != 200 {
		t.Fatalf("PUT 失败：%d %s", w.Code, w.Body)
	}
	s := app.OverlaySettings()
	if s.Accent != "#FF8000" || s.Opacity != 1 || s.Title != defaultOverlayTitle || s.ShowLucky || !s.ShowWins || s.Layout != "full" {
		t.Fatalf("设置不对：%+v", s)
	}
	if got := loadOverlaySettings(app.dataDir); got != s {
		t.Fatalf("重新读取的设置不一致：%+v", got)
	}
	if w := put(`{"accent":"red;}"}`, "", "application/json"); w.Code != 200 || app.OverlaySettings().Accent != "" {
		t.Fatalf("非法颜色应清空：%d %+v", w.Code, app.OverlaySettings())
	}

	// 其他网站发起的请求、非 JSON 请求都拒绝
	if w := put(`{"title":"x"}`, "https://evil.example", "application/json"); w.Code != http.StatusForbidden {
		t.Fatalf("跨站请求应被拒绝：%d", w.Code)
	}
	if w := put(`{"title":"x"}`, "", "text/plain"); w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("非 JSON 请求应被拒绝：%d", w.Code)
	}
	if app.OverlaySettings().Title == "x" {
		t.Fatal("被拒绝的请求不应修改设置")
	}
}

func TestTiersIncludeRecentGifts(t *testing.T) {
	app := newTestApp(t, 0)
	now := time.Now().UnixMilli()
	// 上一场开出过城堡，本场没开出：本场档位表里仍有城堡（0 次），且排在第 1
	app.AddEvent(box("old", now-2*86400e3, "a", "城堡", 2233000, 1))
	app.LiveStarted(now-3600e3, "测试")
	app.AddEvent(box("new", now-60e3, "b", "棉花糖", 9000, 2))

	r := app.computeStats("session", "", "net", false)
	if len(r.Tiers) != 2 || r.Tiers[0].Name != "城堡" || r.Tiers[0].Count != 0 || r.Tiers[1].Count != 2 {
		t.Fatalf("tiers %+v", r.Tiers)
	}
	if r.Summary.Boxes != 2 || r.Feed[0].Tier != 1 {
		t.Fatalf("summary %+v feed %+v", r.Summary, r.Feed)
	}
}

func TestMultipleBoxKinds(t *testing.T) {
	app := newTestApp(t, 0)
	now := time.Now().UnixMilli()
	app.LiveStarted(now-3600e3, "测试")
	app.AddEvent(box("h1", now-50e3, "a", "棉花糖", 9000, 3))
	app.AddEvent(box("h2", now-40e3, "a", "城堡", 2233000, 1))
	star := box("s1", now-30e3, "b", "荧光棒", 3000, 4)
	star.Box, star.BoxPrice = "星光盲盒", 5000
	app.AddEvent(star)

	// 全部：两种盲盒都统计，档位按盲盒分组，出现率在各自盲盒内计算
	r := app.computeStats("session", "", "net", false)
	if r.Summary.Boxes != 8 || len(r.Kinds) != 2 || r.Kinds[0].Name != "心动盲盒" || r.Kinds[0].Boxes != 4 || r.Kinds[1].Boxes != 4 {
		t.Fatalf("summary %+v kinds %+v", r.Summary, r.Kinds)
	}
	if len(r.Tiers) != 3 || r.Tiers[0].Name != "城堡" || r.Tiers[0].Rate != 0.25 || r.Tiers[2].Box != "星光盲盒" || r.Tiers[2].Rank != 0 || r.Tiers[2].Rate != 1 {
		t.Fatalf("tiers %+v", r.Tiers)
	}
	if r.Feed[0].Box != "星光盲盒" || r.Feed[0].Tier != 0 {
		t.Fatalf("feed %+v", r.Feed[0])
	}

	// 只看星光盲盒：汇总只算它，种类列表仍然完整
	r = app.computeStats("session", "星光盲盒", "net", false)
	if r.Summary.Boxes != 4 || r.Summary.Spent != 200 || len(r.Tiers) != 1 || len(r.Kinds) != 2 || r.SessionsBoxes != 4 {
		t.Fatalf("filtered summary %+v tiers %+v", r.Summary, r.Tiers)
	}
}

func TestOfflineSessions(t *testing.T) {
	app := newTestApp(t, 0)
	now := time.Now().UnixMilli()
	ev := func(id string, ts int64) { app.AddEvent(box(id, ts, "a", "棉花糖", 9000, 1)) }

	// 没开播时收到盲盒：自动开一段未开播时段，「本场」能看到
	ev("1", now-3*3600e3)
	cur, _ := app.st().CurrentSession()
	if !cur.Offline || cur.End != 0 {
		t.Fatalf("应开启未开播时段：%+v", cur)
	}
	if r := app.computeStats("session", "", "net", false); r.Summary.Boxes != 1 || !r.Meta.SessionOff {
		t.Fatalf("本场应包含未开播记录：%+v %+v", r.Summary, r.Meta)
	}
	app.LiveEnded() // 未开播时每分钟都会确认一次下播，不能结束未开播时段
	if cur, _ := app.st().CurrentSession(); cur.End != 0 {
		t.Fatalf("未开播时段被误结束：%+v", cur)
	}

	// 超过 2 小时没有新记录，下一条另起一段
	ev("2", now-30*60e3)
	if ss := app.st().Sessions(); len(ss) != 2 || ss[0].End == 0 || !ss[1].Offline {
		t.Fatalf("应另起一段：%+v", ss)
	}

	// 开播：未开播时段结束，开始正式场次
	app.LiveStarted(now-10*60e3, "直播")
	ss := app.st().Sessions()
	if len(ss) != 3 || ss[1].End == 0 || ss[2].Offline || ss[2].End != 0 {
		t.Fatalf("开播后场次 %+v", ss)
	}
	ev("3", now-60e3)
	if r := app.computeStats("session", "", "net", false); r.Summary.Boxes != 1 || r.Meta.SessionOff {
		t.Fatalf("开播后本场只算直播中的记录：%+v", r.Summary)
	}
}

func TestRoomStores(t *testing.T) {
	app := newTestApp(t, 1)
	app.AddEvent(box("a", time.Now().UnixMilli(), "a", "棉花糖", 9000, 1))

	app.SetRoomID(2, 2) // 切到另一个直播间：数据分开
	if r := app.computeStats("30d", "", "net", false); r.Summary.Boxes != 0 {
		t.Fatalf("直播间 2 不应看到直播间 1 的记录：%+v", r.Summary)
	}
	app.SetRoomID(1, 1)
	if r := app.computeStats("30d", "", "net", false); r.Summary.Boxes != 1 {
		t.Fatalf("切回直播间 1 应看到原来的记录：%+v", r.Summary)
	}
}

func TestStartWithoutRoom(t *testing.T) {
	data := t.TempDir()
	app, err := New(config.Default(), "", data, false)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	// 还没设置直播间：只放在内存里，不在 data/ 里生成任何文件
	app.AddEvent(box("a", time.Now().UnixMilli(), "a", "棉花糖", 9000, 1))
	if entries, _ := os.ReadDir(data); len(entries) != 0 {
		t.Fatalf("未设置直播间时不应写文件：%v", entries)
	}
	// 在网页上设置直播间后，数据放进该直播间的目录
	app.SetRoomID(5, 5)
	app.AddEvent(box("b", time.Now().UnixMilli(), "a", "棉花糖", 9000, 1))
	if _, err := os.Stat(filepath.Join(store.RoomDir(data, 5), "events.jsonl")); err != nil {
		t.Fatal("设置直播间后应写入 room-5 目录")
	}
}

func TestOverlaySaveWithoutDataDir(t *testing.T) {
	// 首次运行还没设置直播间：data/ 不存在时也要能保存叠加层设置
	data := filepath.Join(t.TempDir(), "data")
	app, err := New(config.Default(), "", data, false)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if _, err := app.SetOverlaySettings(OverlaySettings{Title: "测试"}); err != nil {
		t.Fatalf("保存失败：%v", err)
	}
	if got := loadOverlaySettings(data); got.Title != "测试" {
		t.Fatalf("重新读取的设置不对：%+v", got)
	}
}
