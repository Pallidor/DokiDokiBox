package bili

// SEND_GIFT_V2：B 站 2026 年起的礼物消息，内容是 Base64 编码的 protobuf（data.pb），
// 类型为 bilibili.live.gift.v1.SendGiftBroadcast。一条消息可能包含多个开出结果（如十连盲盒）。
// 这里只解码需要的字段，字段编号参考 blivedm-go 的 pb/events.proto：
//
//	SendGift   { 1 uid, 2 uname, 9 blind_gift, 10 repeated gift_items, 15 sender_uinfo }
//	GiftItem   { 1 gift_id, 2 gift_name, 3 num, 5 price, 9 tid, 10 timestamp, 36 gift_tip_price }
//	BlindGift  { 1 config_id, 2 original_gift_id, 3 original_gift_name, 6 original_gift_price, 7 gift_tip_price }
//	SenderInfo { 1 uid, 2 base { 1 name } }

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/Pallidor/DokiDokiBox/internal/store"
)

// pbField 是一个 protobuf 字段，只支持 varint 和长度前缀两种类型，其余类型跳过。
type pbField struct {
	num   int
	u     uint64
	bytes []byte
}

type pbMsg []pbField

func pbVarint(b []byte, i int) (uint64, int, error) {
	var v uint64
	for shift := 0; shift < 64; shift += 7 {
		if i >= len(b) {
			return 0, 0, errors.New("protobuf 数据不完整")
		}
		c := b[i]
		i++
		v |= uint64(c&0x7f) << shift
		if c < 0x80 {
			return v, i, nil
		}
	}
	return 0, 0, errors.New("protobuf varint 过长")
}

func pbDecode(b []byte) (pbMsg, error) {
	var m pbMsg
	for i := 0; i < len(b); {
		key, n, err := pbVarint(b, i)
		if err != nil {
			return nil, err
		}
		i = n
		f := pbField{num: int(key >> 3)}
		switch key & 7 {
		case 0: // varint
			if f.u, i, err = pbVarint(b, i); err != nil {
				return nil, err
			}
		case 1: // 64 位定长
			i += 8
		case 2: // 长度前缀：字符串、字节或嵌套消息
			l, n, err := pbVarint(b, i)
			if err != nil {
				return nil, err
			}
			if l > uint64(len(b)-n) {
				return nil, errors.New("protobuf 长度越界")
			}
			f.bytes, i = b[n:n+int(l)], n+int(l)
		case 5: // 32 位定长
			i += 4
		default:
			return nil, fmt.Errorf("不支持的 protobuf 类型 %d", key&7)
		}
		if i > len(b) {
			return nil, errors.New("protobuf 数据不完整")
		}
		m = append(m, f)
	}
	return m, nil
}

func (m pbMsg) uint(num int) uint64 {
	for _, f := range m {
		if f.num == num {
			return f.u
		}
	}
	return 0
}

func (m pbMsg) str(num int) string {
	for _, f := range m {
		if f.num == num {
			return string(f.bytes)
		}
	}
	return ""
}

func (m pbMsg) msg(num int) pbMsg {
	for _, f := range m {
		if f.num == num {
			sub, _ := pbDecode(f.bytes)
			return sub
		}
	}
	return nil
}

func (m pbMsg) msgs(num int) []pbMsg {
	var out []pbMsg
	for _, f := range m {
		if f.num == num {
			if sub, err := pbDecode(f.bytes); err == nil {
				out = append(out, sub)
			}
		}
	}
	return out
}

// giftV2 是从 SEND_GIFT_V2 解出的一个开出结果。
type giftV2 struct {
	UID      int64
	Uname    string
	GiftID   int64
	Gift     string
	Num      int
	Price    int64 // 单个礼物价值（金瓜子）
	Tid      string
	TS       int64 // 秒
	BoxName  string
	BoxPrice int64 // 盲盒单价（金瓜子）
	Blind    bool
}

func parseGiftV2(pb []byte) ([]giftV2, error) {
	ev, err := pbDecode(pb)
	if err != nil {
		return nil, err
	}
	uid, uname := int64(ev.uint(1)), ev.str(2)
	if s := ev.msg(15); s != nil {
		if v := s.uint(1); v != 0 {
			uid = int64(v)
		}
		if n := s.msg(2).str(1); n != "" {
			uname = n
		}
	}
	blind := ev.msg(9)
	isBlind := blind != nil && (blind.uint(1) != 0 || blind.uint(2) != 0 || blind.str(3) != "")

	items := ev.msgs(10)
	if len(items) == 0 {
		return nil, errors.New("礼物消息里没有礼物")
	}
	out := make([]giftV2, 0, len(items))
	for _, it := range items {
		g := giftV2{
			UID: uid, Uname: uname,
			GiftID: int64(it.uint(1)), Gift: it.str(2), Num: int(it.uint(3)),
			Price: int64(it.uint(5)), Tid: it.str(9), TS: int64(it.uint(10)),
			Blind: isBlind,
		}
		if g.Num <= 0 {
			g.Num = 1
		}
		if isBlind {
			g.BoxName, g.BoxPrice = blind.str(3), int64(blind.uint(6))
			// 盲盒开出礼物的价值：优先 gift_tip_price，与 SEND_GIFT 的取法一致
			if v := int64(it.uint(36)); v > 0 {
				g.Price = v
			} else if v := int64(blind.uint(7)); v > 0 {
				g.Price = v
			}
		}
		out = append(out, g)
	}
	return out, nil
}

func (c *Client) handleGiftV2(body []byte) {
	var m struct {
		Data struct {
			PB string `json:"pb"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &m) != nil || m.Data.PB == "" {
		return
	}
	raw, err := base64.StdEncoding.DecodeString(m.Data.PB)
	if err != nil {
		log.Printf("礼物消息解码失败：%v", err)
		return
	}
	gifts, err := parseGiftV2(raw)
	if err != nil {
		log.Printf("礼物消息解析失败：%v", err)
		return
	}
	recorded := false
	for i, g := range gifts {
		if Debug {
			box := ""
			if g.Blind {
				box = fmt.Sprintf("（%s 开出，盲盒单价 %d 金瓜子）", g.BoxName, g.BoxPrice)
			}
			log.Printf("[调试] 礼物：%s 送出 %s ×%d，单价 %d 金瓜子%s", g.Uname, g.Gift, g.Num, g.Price, box)
		}
		if !g.Blind {
			continue
		}
		if !recorded {
			c.sink.AppendRaw(body)
			recorded = true
		}
		// 十连等批量开盒的多个结果共用一个 tid，加上序号区分，否则会被当成重复记录丢掉
		id := g.Tid
		if id == "" {
			id = fmt.Sprintf("v2-%d-%d-%d", g.UID, g.GiftID, g.TS)
		}
		if len(gifts) > 1 {
			id = fmt.Sprintf("%s#%d", id, i)
		}
		ts := time.Now().UnixMilli()
		if g.TS > 1e9 {
			ts = g.TS * 1000
		}
		c.sink.AddEvent(store.Event{
			ID: id, TS: ts, UID: g.UID, Uname: g.Uname,
			Box: g.BoxName, BoxPrice: g.BoxPrice,
			GiftID: g.GiftID, Gift: g.Gift, GiftPrice: g.Price, Num: g.Num,
		})
	}
}
