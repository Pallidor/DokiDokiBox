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
	Login        string        `json:"login"`       // 已登录的 B 站账号昵称
	BigMultiple  float64       `json:"bigMultiple"` // 开出价值 ≥ 盲盒单价 × 该倍数算大奖
}

type Summary struct {
	Boxes int     `json:"boxes"`
	Users int     `json:"users"`
	Spent float64 `json:"spent"`
	Got   float64 `json:"got"`
	Net   float64 `json:"net"`
	Ratio float64 `json:"ratio"`
}

// Tier 是某种盲盒里的一个档位（礼物 + 单价）；Rank 是该盲盒内按价值从高到低的序号，Rate 是该盲盒内的出现率。
type Tier struct {
	Box   string  `json:"box"`
	Rank  int     `json:"rank"`
	Name  string  `json:"name"`
	Value float64 `json:"value"`
	Count int     `json:"count"`
	Rate  float64 `json:"rate"`
	Big   bool    `json:"big"`
}

// BoxKind 是一种盲盒（按名字区分）在统计范围内的汇总。
type BoxKind struct {
	Name  string  `json:"name"`
	Price float64 `json:"price"` // 单价（电池），取最近一条记录
	Boxes int     `json:"boxes"`
	Net   float64 `json:"net"`
	Ratio float64 `json:"ratio"`
}

type FeedItem struct {
	TS    int64   `json:"ts"`
	Box   string  `json:"box"`
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
	Box            string       `json:"box"`   // 只看某种盲盒，空表示全部
	Kinds          []BoxKind    `json:"kinds"` // 近 30 天和本范围内出现过的盲盒种类
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
		LastEventAt: a.lastEvent, Version: Version, Accent: a.cfg.Accent, BigMultiple: a.cfg.BigPrizeMultiple,
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

// rangeEvents 返回统计范围内的所有盲盒事件。
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
	return a.st().Range(from, to), true
}

// filterBox 只保留某种盲盒的事件；box 为空时原样返回。
func filterBox(evs []store.Event, box string) []store.Event {
	if box == "" {
		return evs
	}
	out := []store.Event{}
	for _, e := range evs {
		if e.Box == box {
			out = append(out, e)
		}
	}
	return out
}

// boxKinds 汇总 evs 里各种盲盒的数据；known 里出现过、evs 里没有的种类也列出（0 盒），
// 让种类列表和档位顺序在不同统计范围之间保持一致。按单价从高到低排列。
func boxKinds(evs, known []store.Event) []BoxKind {
	type acc struct {
		price, ts, spent, got int64
		boxes                 int
	}
	m := map[string]*acc{}
	see := func(e store.Event) *acc {
		k := m[e.Box]
		if k == nil {
			k = &acc{}
			m[e.Box] = k
		}
		if e.TS >= k.ts {
			k.price, k.ts = e.BoxPrice, e.TS
		}
		return k
	}
	for _, e := range known {
		see(e)
	}
	for _, e := range evs {
		k := see(e)
		k.boxes += e.Num
		k.spent += e.BoxPrice * int64(e.Num)
		k.got += e.GiftPrice * int64(e.Num)
	}
	list := make([]BoxKind, 0, len(m))
	for name, k := range m {
		list = append(list, BoxKind{
			Name: name, Price: battery(k.price), Boxes: k.boxes,
			Net: battery(k.got - k.spent), Ratio: ratio(k.got, k.spent),
		})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Price != list[j].Price {
			return list[i].Price > list[j].Price
		}
		return list[i].Name < list[j].Name
	})
	return list
}

func (a *App) isBig(e store.Event) bool { return a.cfg.IsBig(e.GiftPrice, e.BoxPrice) }

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
func droughts(evs []store.Event, isBig func(store.Event) bool) (since int, found bool, longest int, bigCount int) {
	run := 0
	for _, e := range evs {
		if isBig(e) {
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

func senders(evs []store.Event, isBig func(store.Event) bool) []Sender {
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
		if isBig(e) {
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

func (a *App) computeStats(rangeKey, box, sortKey string, bigOnly bool) StatsResp {
	if _, ok := rangeLabels[rangeKey]; !ok {
		rangeKey = "session"
	}
	isBig := a.isBig
	all, hasRange := a.rangeEvents(rangeKey)
	// 近 30 天开出过的盲盒和礼物即使本范围内没开出也列出来（次数为 0），
	// 这样档位编号和颜色在不同场次、不同统计范围之间保持一致；奖池调整后旧礼物 30 天后自动消失
	known, _ := a.rangeEvents("30d")
	evs := filterBox(all, box)
	r := StatsResp{
		Meta: a.meta(), Range: rangeKey, RangeLabel: rangeLabels[rangeKey], HasRange: hasRange,
		Box: box, Kinds: boxKinds(all, known), Summary: summarize(evs),
	}

	// 档位：每种盲盒内按礼物名 + 单价分组；盲盒按种类列表的顺序，同一盲盒内按价值从高到低
	kindOrder := map[string]int{}
	boxCount := map[string]int{}
	for i, k := range r.Kinds {
		kindOrder[k.Name] = i
		boxCount[k.Name] = k.Boxes
	}
	type tierAcc struct {
		box, name     string
		gold, boxGold int64 // boxGold 取最近一条记录的盲盒单价，用来判断该档是否算大奖
		count         int
	}
	tierKey := func(e store.Event) string {
		return e.Box + "|" + e.Gift + "|" + strconv.FormatInt(e.GiftPrice, 10)
	}
	tm := map[string]*tierAcc{}
	for _, e := range append(filterBox(known, box), evs...) {
		if tm[tierKey(e)] == nil {
			tm[tierKey(e)] = &tierAcc{box: e.Box, name: e.Gift, gold: e.GiftPrice}
		}
		tm[tierKey(e)].boxGold = e.BoxPrice
	}
	for _, e := range evs {
		tm[tierKey(e)].count += e.Num
	}
	tiers := make([]*tierAcc, 0, len(tm))
	for _, t := range tm {
		tiers = append(tiers, t)
	}
	sort.Slice(tiers, func(i, j int) bool {
		a, b := tiers[i], tiers[j]
		if a.box != b.box {
			return kindOrder[a.box] < kindOrder[b.box]
		}
		if a.gold != b.gold {
			return a.gold > b.gold
		}
		return a.name < b.name
	})
	tierRank := map[string]int{}
	r.Tiers = []Tier{}
	rank := 0
	for i, t := range tiers {
		if i > 0 && tiers[i-1].box != t.box {
			rank = 0
		}
		tierRank[t.box+"|"+t.name+"|"+strconv.FormatInt(t.gold, 10)] = rank
		tr := Tier{Box: t.box, Rank: rank, Name: t.name, Value: battery(t.gold), Count: t.count, Big: a.cfg.IsBig(t.gold, t.boxGold)}
		if n := boxCount[t.box]; n > 0 {
			tr.Rate = float64(t.count) / float64(n)
		}
		r.Tiers = append(r.Tiers, tr)
		rank++
	}

	r.SinceBig, r.HasBig, r.LongestDrought, r.BigCount = droughts(evs, isBig)
	if r.Summary.Boxes > 0 {
		r.BigRate = float64(r.BigCount) / float64(r.Summary.Boxes)
	}

	r.Feed = []FeedItem{}
	for i := len(evs) - 1; i >= 0 && len(r.Feed) < 50; i-- {
		e := evs[i]
		big := isBig(e)
		if bigOnly && !big {
			continue
		}
		r.Feed = append(r.Feed, FeedItem{
			TS: e.TS, Box: e.Box, User: e.Uname, Gift: e.Gift, Value: battery(e.GiftPrice),
			Num: e.Num, Delta: battery((e.GiftPrice - e.BoxPrice) * int64(e.Num)), Big: big,
			Tier: tierRank[tierKey(e)],
		})
	}

	list := senders(evs, isBig)
	sortSenders(list, sortKey)
	r.SenderTotal = len(list)
	r.Senders = list[:min(len(list), 50)]

	r.Sessions, r.SessionsBoxes, r.SessionsRatio = a.sessionBars(10, box)
	return r
}

// sessionBars 返回最近 n 场有盲盒记录的直播；box 不为空时只统计该种盲盒。
func (a *App) sessionBars(n int, box string) ([]SessionBar, int, float64) {
	sessions := a.st().Sessions()
	cur, _ := a.st().CurrentSession()
	bars := []SessionBar{}
	var spent, got int64
	boxes := 0
	for i := len(sessions) - 1; i >= 0 && len(bars) < n; i-- {
		s := sessions[i]
		var sp, gt int64
		bx := 0
		for _, e := range filterBox(a.st().Range(s.Start, sessionTo(s)), box) {
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
	evs, _ := a.rangeEvents("session")
	sum := summarize(evs)
	r := OverlayResp{Meta: a.meta(), Settings: a.OverlaySettings(), Boxes: sum.Boxes, Spent: sum.Spent, Got: sum.Got, Net: sum.Net}
	if r.Settings.Accent == "" {
		r.Settings.Accent = a.cfg.Accent
	}
	r.SinceBig, r.HasBig, _, _ = droughts(evs, a.isBig)
	r.Wins = []FeedItem{}
	for i := len(evs) - 1; i >= 0 && len(r.Wins) < 3; i-- {
		if e := evs[i]; a.isBig(e) {
			r.Wins = append(r.Wins, FeedItem{TS: e.TS, Box: e.Box, User: e.Uname, Gift: e.Gift, Value: battery(e.GiftPrice), Num: e.Num, Big: true})
		}
	}
	list := senders(evs, a.isBig)
	sortSenders(list, "net")
	r.Lucky = list[:min(len(list), 3)]
	return r
}
