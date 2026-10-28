package app

// 示例模式：不连接 B 站，生成近 10 场的模拟数据，并持续模拟实时送盲盒。
// 奖池（礼物、价值、概率）取自 bilibili-API-collect 的 docs/live/gift.md「获取盲盒概率」
// 中心动盲盒的官方接口返回示例（2025 年 6 月收录），当前奖池可能已调整。

import (
	"context"
	"fmt"
	"github.com/Pallidor/DokiDokiBox/internal/bili"
	"github.com/Pallidor/DokiDokiBox/internal/config"
	"github.com/Pallidor/DokiDokiBox/internal/store"
	"math"
	"math/rand/v2"
	"sort"
	"time"
)

const demoBox, demoBoxPrice = "心动盲盒", 15000

var demoGifts = []struct {
	id   int64
	name string
	gold int64
	p    float64 // 百分比
}{
	{32132, "浪漫城堡", 2233000, 0.04},
	{34894, "蛇形护符", 200000, 0.08},
	{32282, "时空之站", 100000, 0.12},
	{32281, "绮彩权杖", 40000, 3.7},
	{32128, "爱心抱枕", 16000, 45.56},
	{32126, "棉花糖", 9000, 44.5},
	{32125, "电影票", 2000, 6},
}

var demoUsers = []string{
	"夜航船", "北纬十七度", "柚子不酸", "阿土不土", "一只小飞棍", "路过的猫", "晚风与你",
	"橘子汽水", "不吃香菜", "山海不可平", "月亮邮递员", "今天也要早睡", "小鹿乱撞", "云边小卖部",
	"芝士就是力量", "银河打工人", "南风知我意", "电波少女", "周末去看海", "白桃乌龙",
}

type demoGen struct {
	rng *rand.Rand
	seq int
}

func demoConfig(cfg *config.Config) *config.Config {
	c := *cfg
	c.BoxNames = []string{demoBox}
	return &c
}

// purchase 模拟一位观众一次性开若干个盲盒；同一礼物合并为一条消息，与 B 站推送方式一致。
func (g *demoGen) purchase(ts time.Time) []store.Event {
	// 幂分布让少数观众开得特别多，接近真实直播间
	idx := int(math.Pow(g.rng.Float64(), 2.2) * float64(len(demoUsers)))
	user, uid := demoUsers[idx], int64(3000000+idx)
	n := 1
	switch x := g.rng.Float64(); {
	case x > 0.92:
		n = 10
	case x > 0.75:
		n = 2 + g.rng.IntN(4)
	}
	counts := map[int]int{}
	for range n {
		x := g.rng.Float64() * 100
		for i, gift := range demoGifts {
			if x < gift.p || i == len(demoGifts)-1 {
				counts[i]++
				break
			}
			x -= gift.p
		}
	}
	var evs []store.Event
	for i, gift := range demoGifts {
		if counts[i] == 0 {
			continue
		}
		g.seq++
		evs = append(evs, store.Event{
			ID: fmt.Sprintf("demo-%d-%d", ts.UnixNano(), g.seq), TS: ts.UnixMilli(),
			UID: uid, Uname: user, Box: demoBox, BoxPrice: demoBoxPrice,
			GiftID: gift.id, Gift: gift.name, GiftPrice: gift.gold, Num: counts[i],
		})
	}
	return evs
}

func (g *demoGen) fill(st *store.Store, from, to time.Time, boxes int) {
	span := int64(to.Sub(from))
	var evs []store.Event
	for n := 0; n < boxes; {
		for _, e := range g.purchase(from.Add(time.Duration(g.rng.Int64N(span)))) {
			evs = append(evs, e)
			n += e.Num
		}
	}
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].TS < evs[j].TS })
	for _, e := range evs {
		st.Add(e)
	}
}

func seedDemo(app *App, g *demoGen) {
	now := time.Now()
	for d := 9; d >= 1; d-- {
		day := now.AddDate(0, 0, -d)
		start := time.Date(day.Year(), day.Month(), day.Day(), 19+g.rng.IntN(2), g.rng.IntN(60), 0, 0, time.Local)
		end := start.Add(time.Duration(150+g.rng.IntN(90)) * time.Minute)
		app.st().StartSession(start.UnixMilli(), "示例直播间")
		g.fill(app.st(), start, end, 700+g.rng.IntN(800))
		app.st().EndSession(end.UnixMilli(), false)
	}
	start := now.Add(-(2*time.Hour + 14*time.Minute))
	app.LiveStarted(start.UnixMilli(), "今晚冲浪漫城堡！心动盲盒开到下播")
	g.fill(app.st(), start, now.Add(-10*time.Second), 1200)
	app.SetRoomInfo("今晚冲浪漫城堡！心动盲盒开到下播", true)
	app.SetRoomID(1234567, 1234567)
	app.UpdateRoom(bili.RoomInfo{UID: 1, Uname: "示例主播", Level: 28, Area: "娱乐 · 聊天室", Online: 86420, Followers: 125800})
}

func runDemo(ctx context.Context, app *App) {
	g := &demoGen{rng: rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 7))}
	seedDemo(app, g)
	for {
		wait := 600*time.Millisecond + time.Duration(g.rng.IntN(2400))*time.Millisecond
		if !sleepCtx(ctx, wait) {
			return
		}
		for _, e := range g.purchase(time.Now()) {
			app.AddEvent(e)
		}
	}
}

// sleepCtx 等待 d，ctx 结束时提前返回 false。
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
