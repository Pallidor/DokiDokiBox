package app

import (
	"github.com/Pallidor/DokiDokiBox/internal/bili"
	"github.com/Pallidor/DokiDokiBox/internal/store"
	"math"
	"sort"
	"strconv"
	"time"
)

type Meta struct {
	Demo         bool          `json:"demo"`
	RoomID       int64         `json:"roomId"`
	Title        string        `json:"title"`
	Live         bool          `json:"live"`
	SessionStart int64         `json:"sessionStart"`
	SessionEnd   int64         `json:"sessionEnd"`
	SessionOff   bool          `json:"sessionOffline"` // 「本场」是未开播时段
	ServerNow    int64         `json:"serverNow"`
	Status       string        `json:"status"`
	StatusMsg    string        `json:"statusMsg"`
	LastEventAt  int64         `json:"lastEventAt"`
	Version      string        `json:"version"`
	Accent       string        `json:"accent"`
	RealRoom     int64         `json:"realRoom"`
	Room         bili.RoomInfo `json:"room"`
	Login        string        `json:"login"` // 已登录的 B 站账号昵称
	BigPrize     float64       `json:"bigPrize"`
}

type Summary struct {
	Boxes int     `json:"boxes"`
	Users int     `json:"users"`
	Spent float64 `json:"spent"`
	Got   float64 `json:"got"`
	Net   float64 `json:"net"`
	Ratio float64 `json:"ratio"`
}

type Tier struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
	Count int     `json:"count"`
	Rate  float64 `json:"rate"`
	Big   bool    `json:"big"`
}

type FeedItem struct {
	TS    int64   `json:"ts"`
	User  string  `json:"user"`
	Gift  string  `json:"gift"`
	Value float64 `json:"value"`
	Num   int     `json:"num"`
	Delta float64 `json:"delta"`
	Big   bool    `json:"big"`
	Tier  int     `json:"tier"`
}

type Sender struct {
	Name  string  `json:"name"`
	Boxes int     `json:"boxes"`
	Spent float64 `json:"spent"`
	Got   float64 `json:"got"`
	Net   float64 `json:"net"`
	Ratio float64 `json:"ratio"`
	Big   int     `json:"big"`
}

type SessionBar struct {
	Date    string  `json:"date"`
	Boxes   int     `json:"boxes"`
	Spent   float64 `json:"spent"`
	Got     float64 `json:"got"`
	Ratio   float64 `json:"ratio"`
	Current bool    `json:"current"`
}

type StatsResp struct {
	Meta           Meta         `json:"meta"`
	Range          string       `json:"range"`
	RangeLabel     string       `json:"rangeLabel"`
	HasRange       bool         `json:"hasRange"`
	Summary        Summary      `json:"summary"`
	Tiers          []Tier       `json:"tiers"`
	BigCount       int          `json:"bigCount"`
	BigRate        float64      `json:"bigRate"`
	SinceBig       int          `json:"sinceBig"`
	HasBig         bool         `json:"hasBig"`
	LongestDrought int          `json:"longestDrought"`
	Feed           []FeedItem   `json:"feed"`
	Senders        []Sender     `json:"senders"`
	SenderTotal    int          `json:"senderTotal"`
	Sessions       []SessionBar `json:"sessions"`
	SessionsBoxes  int          `json:"sessionsBoxes"`
	SessionsRatio  float64      `json:"sessionsRatio"`
}

type OverlayResp struct {
	Meta     Meta            `json:"meta"`
	Settings OverlaySettings `json:"settings"`
	Boxes    int             `json:"boxes"`
	Spent    float64         `json:"spent"`
	Got      float64         `json:"got"`
	Net      float64         `json:"net"`
	Wins     []FeedItem      `json:"wins"`
	Lucky    []Sender        `json:"lucky"`
	SinceBig int             `json:"sinceBig"`
	HasBig   bool            `json:"hasBig"`
}

var rangeLabels = map[string]string{"session": "本场", "today": "今日", "7d": "近 7 天", "30d": "近 30 天"}

func sessionTo(s store.Session) int64 {
	if s.End != 0 {
		return s.End
	}
	return math.MaxInt64
}

func (a *App) meta() Meta {
	a.mu.RLock()
	m := Meta{
		Demo: a.demo, RoomID: a.roomID, Title: a.title, Live: a.live,
		ServerNow: time.Now().UnixMilli(), Status: a.status, StatusMsg: a.statusMsg,
		LastEventAt: a.lastEvent, Version: Version, Accent: a.cfg.Accent, BigPrize: a.cfg.BigPrizeValue,
		RealRoom: a.realRoom, Room: a.room, Login: a.login,
	}
	a.mu.RUnlock()
	if cur, ok := a.st().CurrentSession(); ok {
		m.SessionStart, m.SessionEnd, m.SessionOff = cur.Start, cur.End, cur.Offline
		if m.Title == "" && !cur.Offline {
			m.Title = cur.Title
		}
	}
	return m
}

// rangeEvents 返回统计范围内、且属于所统计盲盒的事件。
func (a *App) rangeEvents(key string) ([]store.Event, bool) {
	now := time.Now()
	var from, to int64 = 0, math.MaxInt64
	switch key {
	case "today":
		y, m, d := now.Date()
		from = time.Date(y, m, d, 0, 0, 0, 0, time.Local).UnixMilli()
	case "7d":
		from = now.Add(-7 * 24 * time.Hour).UnixMilli()
	case "30d":
		from = now.Add(-30 * 24 * time.Hour).UnixMilli()
	default:
		cur, ok := a.st().CurrentSession()
		if !ok {
			return nil, false
		}
		from, to = cur.Start, sessionTo(cur)
	}
	return a.filterBox(a.st().Range(from, to)), true
}

func (a *App) filterBox(evs []store.Event) []store.Event {
	out := evs[:0]
	for _, e := range evs {
		if a.cfg.MatchBox(e.Box) {
			out = append(out, e)
		}
	}
	return out
}

func ratio(got, spent int64) float64 {
	if spent <= 0 {
		return 0
	}
	return float64(got) / float64(spent)
}

func userKey(e store.Event) string {
	if e.UID > 0 {
		return "u" + strconv.FormatInt(e.UID, 10)
	}
	return "n" + e.Uname
}

func summarize(evs []store.Event) Summary {
	var spent, got int64
	boxes := 0
	users := map[string]struct{}{}
	for _, e := range evs {
		n := int64(e.Num)
		boxes += e.Num
		spent += e.BoxPrice * n
		got += e.GiftPrice * n
		users[userKey(e)] = struct{}{}
	}
	return Summary{
		Boxes: boxes, Users: len(users),
		Spent: battery(spent), Got: battery(got), Net: battery(got - spent),
		Ratio: ratio(got, spent),
	}
}

// droughts 返回（距上次大奖的盒数，是否出过大奖，最长连续未出大奖盒数，大奖盒数）。
func droughts(evs []store.Event, bigGold int64) (since int, found bool, longest int, bigCount int) {
	run := 0
	for _, e := range evs {
		if e.GiftPrice >= bigGold {
			longest = max(longest, run)
			run = 0
			found = true
			bigCount += e.Num
		} else {
			run += e.Num
		}
	}
	return run, found, max(longest, run), bigCount
}

func senders(evs []store.Event, bigGold int64) []Sender {
	type acc struct {
		s          Sender
		spent, got int64
	}
	m := map[string]*acc{}
	for _, e := range evs {
		k := userKey(e)
		u := m[k]
		if u == nil {
			u = &acc{}
			m[k] = u
		}
		u.s.Name = e.Uname
		u.s.Boxes += e.Num
		u.spent += e.BoxPrice * int64(e.Num)
		u.got += e.GiftPrice * int64(e.Num)
		if e.GiftPrice >= bigGold {
			u.s.Big += e.Num
		}
	}
	out := make([]Sender, 0, len(m))
	for _, u := range m {
		s := u.s
		s.Spent, s.Got, s.Net, s.Ratio = battery(u.spent), battery(u.got), battery(u.got-u.spent), ratio(u.got, u.spent)
		out = append(out, s)
	}
	return out
}

func sortSenders(list []Sender, key string) {
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		switch key {
		case "net":
			if a.Net != b.Net {
				return a.Net > b.Net
			}
		case "big":
			if a.Big != b.Big {
				return a.Big > b.Big
			}
			if a.Net != b.Net {
				return a.Net > b.Net
			}
		}
		if a.Boxes != b.Boxes {
			return a.Boxes > b.Boxes
		}
		return a.Name < b.Name
	})
}

func (a *App) computeStats(rangeKey, sortKey string, bigOnly bool) StatsResp {
	if _, ok := rangeLabels[rangeKey]; !ok {
		rangeKey = "session"
	}
	bigGold := a.cfg.BigGold()
	evs, hasRange := a.rangeEvents(rangeKey)
	r := StatsResp{
		Meta: a.meta(), Range: rangeKey, RangeLabel: rangeLabels[rangeKey], HasRange: hasRange,
		Summary: summarize(evs),
	}

	// 档位：按礼物名 + 单价分组，按价值从高到低排列
	type tierAcc struct {
		name  string
		gold  int64
		count int
	}
	tm := map[string]*tierAcc{}
	for _, e := range evs {
		k := e.Gift + "|" + strconv.FormatInt(e.GiftPrice, 10)
		t := tm[k]
		if t == nil {
			t = &tierAcc{name: e.Gift, gold: e.GiftPrice}
			tm[k] = t
		}
		t.count += e.Num
	}
	// 近 30 天开出过的礼物即使本范围内没开出也列出来（次数为 0），
	// 这样档位编号和颜色在不同场次、不同统计范围之间保持一致；奖池调整后旧礼物 30 天后自动消失
	known, _ := a.rangeEvents("30d")
	for _, e := range known {
		k := e.Gift + "|" + strconv.FormatInt(e.GiftPrice, 10)
		if tm[k] == nil {
			tm[k] = &tierAcc{name: e.Gift, gold: e.GiftPrice}
		}
	}
	tiers := make([]*tierAcc, 0, len(tm))
	for _, t := range tm {
		tiers = append(tiers, t)
	}
	sort.Slice(tiers, func(i, j int) bool {
		if tiers[i].gold != tiers[j].gold {
			return tiers[i].gold > tiers[j].gold
		}
		return tiers[i].name < tiers[j].name
	})
	tierIndex := map[string]int{}
	r.Tiers = []Tier{}
	for i, t := range tiers {
		tierIndex[t.name+"|"+strconv.FormatInt(t.gold, 10)] = i
		tr := Tier{Name: t.name, Value: battery(t.gold), Count: t.count, Big: t.gold >= bigGold}
		if r.Summary.Boxes > 0 {
			tr.Rate = float64(t.count) / float64(r.Summary.Boxes)
		}
		r.Tiers = append(r.Tiers, tr)
	}

	r.SinceBig, r.HasBig, r.LongestDrought, r.BigCount = droughts(evs, bigGold)
	if r.Summary.Boxes > 0 {
		r.BigRate = float64(r.BigCount) / float64(r.Summary.Boxes)
	}

	r.Feed = []FeedItem{}
	for i := len(evs) - 1; i >= 0 && len(r.Feed) < 50; i-- {
		e := evs[i]
		big := e.GiftPrice >= bigGold
		if bigOnly && !big {
			continue
		}
		r.Feed = append(r.Feed, FeedItem{
			TS: e.TS, User: e.Uname, Gift: e.Gift, Value: battery(e.GiftPrice),
			Num: e.Num, Delta: battery((e.GiftPrice - e.BoxPrice) * int64(e.Num)), Big: big,
			Tier: tierIndex[e.Gift+"|"+strconv.FormatInt(e.GiftPrice, 10)],
		})
	}

	list := senders(evs, bigGold)
	sortSenders(list, sortKey)
	r.SenderTotal = len(list)
	r.Senders = list[:min(len(list), 50)]

	r.Sessions, r.SessionsBoxes, r.SessionsRatio = a.sessionBars(10)
	return r
}

// sessionBars 返回最近 n 场有盲盒记录的直播。
func (a *App) sessionBars(n int) ([]SessionBar, int, float64) {
	sessions := a.st().Sessions()
	cur, _ := a.st().CurrentSession()
	bars := []SessionBar{}
	var spent, got int64
	boxes := 0
	for i := len(sessions) - 1; i >= 0 && len(bars) < n; i-- {
		s := sessions[i]
		var sp, gt int64
		bx := 0
		for _, e := range a.filterBox(a.st().Range(s.Start, sessionTo(s))) {
			bx += e.Num
			sp += e.BoxPrice * int64(e.Num)
			gt += e.GiftPrice * int64(e.Num)
		}
		if bx == 0 {
			continue
		}
		boxes += bx
		spent += sp
		got += gt
		bars = append(bars, SessionBar{
			Date:  time.UnixMilli(s.Start).Format("01/02"),
			Boxes: bx, Spent: battery(sp), Got: battery(gt), Ratio: ratio(gt, sp), Current: s.ID == cur.ID,
		})
	}
	for i, j := 0, len(bars)-1; i < j; i, j = i+1, j-1 {
		bars[i], bars[j] = bars[j], bars[i]
	}
	return bars, boxes, ratio(got, spent)
}

func (a *App) computeOverlay() OverlayResp {
	bigGold := a.cfg.BigGold()
	evs, _ := a.rangeEvents("session")
	sum := summarize(evs)
	r := OverlayResp{Meta: a.meta(), Settings: a.OverlaySettings(), Boxes: sum.Boxes, Spent: sum.Spent, Got: sum.Got, Net: sum.Net}
	if r.Settings.Accent == "" {
		r.Settings.Accent = a.cfg.Accent
	}
	r.SinceBig, r.HasBig, _, _ = droughts(evs, bigGold)
	r.Wins = []FeedItem{}
	for i := len(evs) - 1; i >= 0 && len(r.Wins) < 3; i-- {
		if e := evs[i]; e.GiftPrice >= bigGold {
			r.Wins = append(r.Wins, FeedItem{TS: e.TS, User: e.Uname, Gift: e.Gift, Value: battery(e.GiftPrice), Num: e.Num, Big: true})
		}
	}
	list := senders(evs, bigGold)
	sortSenders(list, "net")
	r.Lucky = list[:min(len(list), 3)]
	return r
}
