package bili

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Pallidor/DokiDokiBox/internal/store"

	"github.com/andybalholm/brotli"
)

// fakeSink 只收集事件，用来单独测试协议解析。
type fakeSink struct{ events []store.Event }

func (f *fakeSink) SetStatus(string, string)  {}
func (f *fakeSink) SetRoomID(int64, int64)    {}
func (f *fakeSink) SetRoomInfo(string, bool)  {}
func (f *fakeSink) UpdateRoom(RoomInfo)       {}
func (f *fakeSink) LiveStarted(int64, string) {}
func (f *fakeSink) LiveEnded()                {}
func (f *fakeSink) SetLogin(string)           {}
func (f *fakeSink) AppendRaw([]byte)          {}
func (f *fakeSink) AppendDebug([]byte)        {}
func (f *fakeSink) AddEvent(e store.Event)    { f.events = append(f.events, e) }

// 示例数据来自 bilibili-API-collect 的 wbi 签名文档。
func TestSignWbi(t *testing.T) {
	v := url.Values{"foo": {"114"}, "bar": {"514"}, "zab": {"1919810"}}
	got := signWbi(v, "7cd084941338484aae1ad9425b84077c", "4932caff0ff746eab6f01bf08b70ac45", 1702204169)
	want := "bar=514&foo=114&wts=1702204169&zab=1919810&w_rid=8f6f2b5b3d485fe1886cec6a0be8c5d4"
	if got != want {
		t.Fatalf("signWbi:\n got %s\nwant %s", got, want)
	}
}

const blindGiftJSON = `{"cmd":"SEND_GIFT","data":{"uid":123,"uname":"夜航船","giftId":32251,"giftName":"礼物A","num":2,"price":200000,"timestamp":1793150000,"tid":"1793150000112233","rnd":"998877",
"blind_gift":{"blind_gift_config_id":51,"gift_action":"爆出","original_gift_id":32251,"original_gift_name":"心动盲盒","original_gift_price":15000,"gift_tip_price":200000}}}`

const normalGiftJSON = `{"cmd":"SEND_GIFT","data":{"uid":5,"uname":"x","giftId":1,"giftName":"辣条","num":1,"price":100,"tid":"1","blind_gift":null}}`

func packet(op uint32, ver uint16, body []byte) []byte {
	p := encodePacket(op, body)
	binary.BigEndian.PutUint16(p[6:], ver)
	return p
}

func TestHandlePacketsCompressed(t *testing.T) {
	inner := append(packet(opMessage, 0, []byte(blindGiftJSON)), packet(opMessage, 0, []byte(normalGiftJSON))...)

	var br bytes.Buffer
	w := brotli.NewWriter(&br)
	w.Write(inner)
	w.Close()

	var zb bytes.Buffer
	zw := zlib.NewWriter(&zb)
	zw.Write(packet(opMessage, 0, []byte(strings.Replace(blindGiftJSON, "1793150000112233", "other-tid", 1))))
	zw.Close()

	authed := false
	buf := append(packet(opAuthReply, 1, []byte(`{"code":0}`)), packet(opMessage, 3, br.Bytes())...)
	buf = append(buf, packet(opMessage, 2, zb.Bytes())...)

	sink := &fakeSink{}
	c := NewClient(sink, 0, "")
	err := c.handlePackets(context.Background(), buf, func(code int) error { authed = code == 0; return nil })
	if err != nil || !authed {
		t.Fatalf("err=%v authed=%v", err, authed)
	}
	if len(sink.events) != 2 {
		t.Fatalf("want 2 blind-box events (normal gift ignored), got %d", len(sink.events))
	}
	e := sink.events[0]
	if e.Uname != "夜航船" || e.Box != "心动盲盒" || e.BoxPrice != 15000 || e.GiftPrice != 200000 || e.Num != 2 || e.Gift != "礼物A" || e.ID != "1793150000112233" {
		t.Fatalf("unexpected event %+v", e)
	}
}

// pbTag / pbBytes 用于在测试里手工拼 protobuf。
func pbTag(num, wire int) []byte { return binary.AppendUvarint(nil, uint64(num<<3|wire)) }
func pbUint(num int, v uint64) []byte {
	return binary.AppendUvarint(pbTag(num, 0), v)
}
func pbBytes(num int, b []byte) []byte {
	return append(binary.AppendUvarint(pbTag(num, 2), uint64(len(b))), b...)
}
func pbJoin(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

func TestGiftV2(t *testing.T) {
	item := func(id uint64, name string, num, price uint64) []byte {
		return pbBytes(10, pbJoin(pbUint(1, id), pbBytes(2, []byte(name)), pbUint(3, num), pbUint(5, price),
			pbBytes(9, []byte("tx1")), pbUint(10, uint64(time.Now().Unix()-60)), pbUint(36, price)))
	}
	blindPB := pbJoin(
		pbUint(1, 42), pbBytes(2, []byte("观众")),
		pbBytes(9, pbJoin(pbUint(1, 139), pbUint(2, 32251), pbBytes(3, []byte("心动盲盒")), pbUint(6, 15000))),
		item(32128, "爱心抱枕", 4, 16000), item(32125, "电影票", 1, 2000),
	)
	gifts, err := parseGiftV2(blindPB)
	if err != nil {
		t.Fatal(err)
	}
	if len(gifts) != 2 || !gifts[0].Blind || gifts[0].BoxName != "心动盲盒" || gifts[0].BoxPrice != 15000 ||
		gifts[0].Gift != "爱心抱枕" || gifts[0].Num != 4 || gifts[0].Price != 16000 || gifts[1].Gift != "电影票" {
		t.Fatalf("解析结果 %+v", gifts)
	}

	// 同一条消息里的多个结果共用 tid，记录 ID 必须不同（否则会被当成重复丢掉）；
	// 同一条消息重复推送时 ID 不变，由存储层去重
	sink := &fakeSink{}
	c := NewClient(sink, 0, "")
	body, _ := json.Marshal(map[string]any{"cmd": "SEND_GIFT_V2", "data": map[string]string{"pb": base64.StdEncoding.EncodeToString(blindPB)}})
	c.handleCmd(context.Background(), body)
	c.handleCmd(context.Background(), body)
	if len(sink.events) != 4 || sink.events[0].ID == sink.events[1].ID ||
		sink.events[0].ID != sink.events[2].ID || sink.events[1].ID != sink.events[3].ID {
		t.Fatalf("事件 ID %+v", sink.events)
	}

	// 普通礼物（没有 blind_gift）不记录
	plain := pbJoin(pbUint(1, 7), pbBytes(2, []byte("观众")), item(31164, "粉丝团灯牌", 1, 100))
	if gifts, err := parseGiftV2(plain); err != nil || len(gifts) != 1 || gifts[0].Blind {
		t.Fatalf("普通礼物 %+v %v", gifts, err)
	}
}
