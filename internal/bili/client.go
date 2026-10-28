// Package bili 实现 B 站直播间连接：房间信息、wbi 签名、弹幕长连接协议、礼物解析
// 和扫码登录接口。连接产生的事件通过 Sink 回报，不依赖程序其他部分。
package bili

// B 站直播间长连接客户端：获取房间信息、wbi 签名、弹幕 WebSocket 协议解析，
// 只关心盲盒开出（带 blind_gift 字段的 SEND_GIFT）以及开播 / 下播事件。

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Pallidor/DokiDokiBox/internal/store"

	"github.com/andybalholm/brotli"
	"github.com/gorilla/websocket"
)

const (
	userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"

	opHeartbeat = 2
	opMessage   = 5
	opAuth      = 7
	opAuthReply = 8

	defaultDanmuHost = "wss://broadcastlv.chat.bilibili.com:443/sub"
)

// B 站接口返回的时间均为北京时间。
var cst = time.FixedZone("CST", 8*3600)

// Debug 为 true 时打印收到的每一条礼物消息（-debug 参数）。
var Debug bool

// Sink 接收直播间连接产生的事件，由程序的运行状态（internal/app）实现。
type Sink interface {
	SetStatus(status, msg string)
	SetRoomID(input, real int64)
	SetRoomInfo(title string, live bool)
	UpdateRoom(RoomInfo)
	LiveStarted(start int64, title string)
	LiveEnded()
	AddEvent(store.Event)
	SetLogin(uname string)
	AppendRaw(body []byte)   // 保存原始盲盒消息
	AppendDebug(body []byte) // 调试模式下保存所有礼物类消息
}

// RoomInfo 是直播间资料，全部来自 B 站公开接口（无需登录）。字段为零值表示这次没取到。
type RoomInfo struct {
	UID       int64  `json:"-"` // 只用于读取主播资料，不发给网页
	Uname     string `json:"uname"`
	Face      string `json:"face"`
	Level     int    `json:"level"`  // 主播等级 UL
	Area      string `json:"area"`   // 分区，如「生活 · 影音馆」
	Online    int64  `json:"online"` // 人气值
	Followers int64  `json:"followers"`
}

// RoomInit 把短号转换成真实房间号，房间不存在时返回错误。
func RoomInit(ctx context.Context, room int64) (int64, error) {
	return NewClient(nil, room, "").roomInit(ctx, room)
}

// LoginName 用 cookie 确认登录是否有效，返回账号昵称。
func LoginName(ctx context.Context, cookie string) (string, error) {
	return NewClient(nil, 0, cookie).loginName(ctx)
}

var mixinKeyEncTab = []int{
	46, 47, 18, 2, 53, 8, 23, 32, 15, 50, 10, 31, 58, 3, 45, 35, 27, 43, 5, 49,
	33, 9, 42, 19, 29, 28, 14, 39, 12, 38, 41, 13, 37, 48, 7, 16, 24, 55, 40,
	61, 26, 17, 0, 1, 60, 51, 30, 4, 22, 25, 54, 21, 56, 59, 6, 63, 57, 62, 11,
	36, 20, 34, 44, 52,
}

// flexInt 兼容接口里时而是数字、时而是字符串的整数字段。
type flexInt int64

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		*f = flexInt(n)
	} else if fl, err := strconv.ParseFloat(s, 64); err == nil {
		*f = flexInt(fl)
	} else {
		*f = 0
	}
	return nil
}

// flexStr 兼容数字或字符串形式的 ID 字段。
type flexStr string

func (f *flexStr) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = flexStr(s)
		return nil
	}
	if string(b) == "null" {
		*f = ""
		return nil
	}
	*f = flexStr(b)
	return nil
}

type Client struct {
	sink      Sink
	inputRoom int64
	httpc     *http.Client
	cookie    string // 扫码登录后保存的 SESSDATA 等，未登录时为空
	uid       int64  // 已登录账号的 UID，未登录为 0
	buvid     string // 匿名设备标识，B 站网页访客也会有，不含任何账号信息
	imgKey    string
	subKey    string
	roomID    int64
	masterAt  time.Time // 上次读取主播资料的时间

	dbgMu     sync.Mutex
	dbgCounts map[string]int
	dbgSince  time.Time
}

// NewClient 创建直播间连接。cookie 为空时不登录，B 站几乎不会推送礼物消息。
func NewClient(sink Sink, room int64, cookie string) *Client {
	return &Client{
		sink:      sink,
		inputRoom: room,
		httpc:     &http.Client{Timeout: 15 * time.Second},
		cookie:    cookie,
	}
}

func (c *Client) cookieHeader() string {
	parts := []string{}
	if c.cookie != "" {
		parts = append(parts, c.cookie)
	}
	if c.buvid != "" {
		parts = append(parts, "buvid3="+c.buvid)
	}
	return strings.Join(parts, "; ")
}

func (c *Client) Run(ctx context.Context) {
	backoff := 2 * time.Second
	for ctx.Err() == nil {
		if c.roomID == 0 {
			c.sink.SetStatus("connecting", "正在获取直播间信息…")
			if err := c.prepare(ctx); err != nil {
				if ctx.Err() != nil {
					return // 已切换到其他直播间或程序退出
				}
				log.Printf("获取直播间信息失败：%v", err)
				c.sink.SetStatus("error", "获取直播间信息失败："+err.Error())
				if !sleepCtx(ctx, backoff) {
					return
				}
				backoff = min(backoff*2, 30*time.Second)
				continue
			}
			go c.pollRoom(ctx)
		}

		started := time.Now()
		err := c.connectOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		log.Printf("弹幕连接断开：%v", err)
		c.sink.SetStatus("error", "弹幕连接断开，正在重连…")
		if time.Since(started) > time.Minute {
			backoff = 2 * time.Second
		}
		if !sleepCtx(ctx, backoff) {
			return
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func (c *Client) prepare(ctx context.Context) error {
	c.ensureBuvid(ctx)
	c.refreshNav(ctx)
	room, err := c.roomInit(ctx, c.inputRoom)
	if err != nil {
		return err
	}
	c.roomID = room
	c.sink.SetRoomID(c.inputRoom, room)
	log.Printf("直播间 %d（真实房间号 %d）", c.inputRoom, room)
	c.refreshRoom(ctx)
	return nil
}

// pollRoom 定时刷新开播状态，弥补长连接偶尔漏掉 LIVE / PREPARING 消息的情况。
func (c *Client) pollRoom(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	n := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.refreshRoom(ctx)
			if n++; n%360 == 0 {
				c.refreshNav(ctx) // wbi 密钥每天会轮换
			}
		}
	}
}

// ---------- HTTP 接口 ----------

func (c *Client) getRaw(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", "https://live.bilibili.com/")
	req.Header.Set("Origin", "https://live.bilibili.com")
	if h := c.cookieHeader(); h != "" {
		req.Header.Set("Cookie", h)
	}
	resp, err := c.httpc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

func (c *Client) getAPI(ctx context.Context, rawURL string, out any) (int, string, error) {
	body, err := c.getRaw(ctx, rawURL)
	if err != nil {
		return 0, "", err
	}
	var env struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Msg     string          `json:"msg"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return 0, "", err
	}
	msg := env.Message
	if msg == "" {
		msg = env.Msg
	}
	// 出错时 data 可能是 [] 或 null，只在是对象时解析
	if out != nil && len(env.Data) > 0 && env.Data[0] == '{' {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return env.Code, msg, err
		}
	}
	return env.Code, msg, nil
}

func (c *Client) ensureBuvid(ctx context.Context) {
	var d struct {
		B3 string `json:"b_3"`
	}
	if _, _, err := c.getAPI(ctx, "https://api.bilibili.com/x/frontend/finger/spi", &d); err != nil || d.B3 == "" {
		log.Printf("获取 buvid 失败（不影响使用）：%v", err)
		return
	}
	c.buvid = d.B3
}

type navInfo struct {
	IsLogin bool    `json:"isLogin"`
	Mid     flexInt `json:"mid"`
	Uname   string  `json:"uname"`
	WbiImg  struct {
		ImgURL string `json:"img_url"`
		SubURL string `json:"sub_url"`
	} `json:"wbi_img"`
}

// refreshNav 读取 wbi 签名密钥，并确认登录状态（未登录时 code 为 -101，但 wbi_img 依然会返回）。
func (c *Client) refreshNav(ctx context.Context) {
	var d navInfo
	if _, _, err := c.getAPI(ctx, "https://api.bilibili.com/x/web-interface/nav", &d); err != nil {
		log.Printf("获取 wbi 密钥失败：%v", err)
		return
	}
	c.imgKey = keyFromURL(d.WbiImg.ImgURL)
	c.subKey = keyFromURL(d.WbiImg.SubURL)
	switch {
	case d.IsLogin:
		if c.uid == 0 {
			log.Printf("已登录 B 站账号：%s", d.Uname)
		}
		c.uid = int64(d.Mid)
		c.sink.SetLogin(d.Uname)
	case c.cookie != "":
		log.Printf("B 站登录已失效，请在主播后台重新扫码登录")
		c.uid = 0
		c.sink.SetLogin("")
	default:
		log.Printf("未登录 B 站：B 站几乎不会推送礼物消息，请在主播后台点「扫码登录」")
	}
}

// loginName 用当前 cookie 确认登录是否有效，返回账号昵称。
func (c *Client) loginName(ctx context.Context) (string, error) {
	var d navInfo
	if _, _, err := c.getAPI(ctx, "https://api.bilibili.com/x/web-interface/nav", &d); err != nil {
		return "", err
	}
	if !d.IsLogin {
		return "", errors.New("登录没有生效，请重新扫码")
	}
	return d.Uname, nil
}

func (c *Client) roomInit(ctx context.Context, room int64) (int64, error) {
	var d struct {
		RoomID flexInt `json:"room_id"`
	}
	code, msg, err := c.getAPI(ctx, fmt.Sprintf("https://api.live.bilibili.com/room/v1/Room/room_init?id=%d", room), &d)
	if err != nil {
		return 0, err
	}
	if code != 0 || d.RoomID == 0 {
		if msg == "" {
			msg = fmt.Sprintf("错误码 %d", code)
		}
		return 0, fmt.Errorf("直播间 %d 无效：%s", room, msg)
	}
	return int64(d.RoomID), nil
}

// refreshMaster 读取主播昵称、头像、等级和粉丝数（公开接口）。
func (c *Client) refreshMaster(ctx context.Context, uid int64) {
	var d struct {
		Info struct {
			Uname string `json:"uname"`
			Face  string `json:"face"`
		} `json:"info"`
		Exp struct {
			MasterLevel struct {
				Level flexInt `json:"level"`
			} `json:"master_level"`
		} `json:"exp"`
		FollowerNum flexInt `json:"follower_num"`
	}
	code, msg, err := c.getAPI(ctx, fmt.Sprintf("https://api.live.bilibili.com/live_user/v1/Master/info?uid=%d", uid), &d)
	if err != nil || code != 0 {
		if ctx.Err() == nil {
			log.Printf("获取主播信息失败：%v %s", err, msg)
		}
		return
	}
	c.masterAt = time.Now()
	c.sink.UpdateRoom(RoomInfo{Uname: d.Info.Uname, Face: d.Info.Face, Level: int(d.Exp.MasterLevel.Level), Followers: int64(d.FollowerNum)})
}

func (c *Client) refreshRoom(ctx context.Context) {
	var d struct {
		UID            flexInt `json:"uid"`
		Title          string  `json:"title"`
		LiveStatus     flexInt `json:"live_status"`
		LiveTime       flexStr `json:"live_time"`
		Online         flexInt `json:"online"`
		Attention      flexInt `json:"attention"`
		AreaName       string  `json:"area_name"`
		ParentAreaName string  `json:"parent_area_name"`
	}
	code, msg, err := c.getAPI(ctx, fmt.Sprintf("https://api.live.bilibili.com/room/v1/Room/get_info?room_id=%d", c.roomID), &d)
	if err != nil || code != 0 {
		if ctx.Err() == nil {
			log.Printf("刷新开播状态失败：%v %s", err, msg)
		}
		return
	}
	area := d.AreaName
	if d.ParentAreaName != "" && d.ParentAreaName != d.AreaName {
		area = d.ParentAreaName + " · " + d.AreaName
	}
	c.sink.UpdateRoom(RoomInfo{UID: int64(d.UID), Area: area, Online: int64(d.Online), Followers: int64(d.Attention)})
	if d.UID != 0 && time.Since(c.masterAt) > 10*time.Minute {
		c.refreshMaster(ctx, int64(d.UID))
	}
	live := d.LiveStatus == 1
	c.sink.SetRoomInfo(d.Title, live)
	if !live {
		c.sink.LiveEnded()
		return
	}
	start := time.Now()
	lt := string(d.LiveTime)
	if t, err := time.ParseInLocation(time.DateTime, lt, cst); err == nil {
		start = t
	} else if n, err := strconv.ParseInt(lt, 10, 64); err == nil && n > 0 {
		start = time.Unix(n, 0)
	}
	c.sink.LiveStarted(start.UnixMilli(), d.Title)
}

type danmuInfo struct {
	Token    string `json:"token"`
	HostList []struct {
		Host    string `json:"host"`
		WssPort int    `json:"wss_port"`
	} `json:"host_list"`
}

func (c *Client) danmuInfo(ctx context.Context) (*danmuInfo, error) {
	q := c.signWbi(url.Values{
		"id":           {strconv.FormatInt(c.roomID, 10)},
		"type":         {"0"},
		"web_location": {"444.8"},
	})
	var d danmuInfo
	code, msg, err := c.getAPI(ctx, "https://api.live.bilibili.com/xlive/web-room/v1/index/getDanmuInfo?"+q, &d)
	if err != nil {
		return nil, err
	}
	if code == 65530 {
		if c.cookie != "" {
			return nil, fmt.Errorf("登录已失效（错误码 65530），请在主播后台重新扫码登录")
		}
		return nil, fmt.Errorf("B 站拒绝了请求（错误码 65530），稍后自动重试")
	}
	if code != 0 {
		return nil, fmt.Errorf("错误码 %d %s", code, msg)
	}
	return &d, nil
}

func (c *Client) signWbi(v url.Values) string {
	return signWbi(v, c.imgKey, c.subKey, time.Now().Unix())
}

func signWbi(v url.Values, imgKey, subKey string, wts int64) string {
	orig := imgKey + subKey
	if len(orig) < 64 {
		return v.Encode()
	}
	var mk strings.Builder
	for _, i := range mixinKeyEncTab {
		mk.WriteByte(orig[i])
	}
	mixin := mk.String()[:32]

	v.Set("wts", strconv.FormatInt(wts, 10))
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		val := strings.Map(func(r rune) rune {
			if strings.ContainsRune("!'()*", r) {
				return -1
			}
			return r
		}, v.Get(k))
		parts = append(parts, url.QueryEscape(k)+"="+strings.ReplaceAll(url.QueryEscape(val), "+", "%20"))
	}
	q := strings.Join(parts, "&")
	sum := md5.Sum([]byte(q + mixin))
	return q + "&w_rid=" + hex.EncodeToString(sum[:])
}

// ---------- WebSocket 长连接 ----------

func (c *Client) connectOnce(ctx context.Context) error {
	token := ""
	hosts := []string{defaultDanmuHost}
	if info, err := c.danmuInfo(ctx); err != nil {
		log.Printf("获取弹幕服务器失败，改用默认服务器：%v", err)
	} else {
		token = info.Token
		var hs []string
		for _, h := range info.HostList {
			if h.Host != "" && h.WssPort > 0 {
				hs = append(hs, fmt.Sprintf("wss://%s:%d/sub", h.Host, h.WssPort))
			}
		}
		hosts = append(hs, hosts...)
	}

	c.sink.SetStatus("connecting", "正在连接弹幕服务器…")
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second, Proxy: http.ProxyFromEnvironment}
	hdr := http.Header{}
	hdr.Set("User-Agent", userAgent)
	hdr.Set("Origin", "https://live.bilibili.com")
	if h := c.cookieHeader(); h != "" {
		hdr.Set("Cookie", h)
	}
	var conn *websocket.Conn
	var err error
	for _, u := range hosts {
		if conn, _, err = dialer.DialContext(ctx, u, hdr); err == nil {
			break
		}
		log.Printf("连接 %s 失败：%v", u, err)
	}
	if conn == nil {
		return err
	}
	defer conn.Close()

	var wmu sync.Mutex
	send := func(op uint32, body []byte) error {
		wmu.Lock()
		defer wmu.Unlock()
		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return conn.WriteMessage(websocket.BinaryMessage, encodePacket(op, body))
	}
	heartbeat := []byte("[object Object]")

	auth, _ := json.Marshal(map[string]any{
		"uid": c.uid, "roomid": c.roomID, "protover": 3, "buvid": c.buvid,
		"platform": "web", "type": 2, "key": token,
	})
	if err := send(opAuth, auth); err != nil {
		return err
	}

	done := make(chan struct{})
	defer close(done)
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				conn.Close()
				return
			case <-t.C:
				if send(opHeartbeat, heartbeat) != nil {
					conn.Close()
					return
				}
			}
		}
	}()

	onAuth := func(code int) error {
		if code != 0 {
			return fmt.Errorf("弹幕服务器认证失败（code %d）", code)
		}
		if c.uid == 0 {
			// 未登录的连接会被 B 站限流，礼物消息基本收不到
			log.Printf("弹幕已连接，但未登录 B 站：礼物消息基本收不到，请在主播后台点「扫码登录」")
			c.sink.SetStatus("limited", "未登录 · 收不到礼物")
		} else {
			log.Printf("弹幕连接成功，开始记录盲盒")
			c.sink.SetStatus("connected", "弹幕连接正常")
		}
		return send(opHeartbeat, heartbeat)
	}

	for {
		conn.SetReadDeadline(time.Now().Add(70 * time.Second))
		_, data, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		if err := c.handlePackets(ctx, data, onAuth); err != nil {
			return err
		}
	}
}

func encodePacket(op uint32, body []byte) []byte {
	buf := make([]byte, 16+len(body))
	binary.BigEndian.PutUint32(buf[0:], uint32(len(buf)))
	binary.BigEndian.PutUint16(buf[4:], 16)
	binary.BigEndian.PutUint16(buf[6:], 1)
	binary.BigEndian.PutUint32(buf[8:], op)
	binary.BigEndian.PutUint32(buf[12:], 1)
	copy(buf[16:], body)
	return buf
}

func (c *Client) handlePackets(ctx context.Context, buf []byte, onAuth func(int) error) error {
	for off := 0; off+16 <= len(buf); {
		plen := int(binary.BigEndian.Uint32(buf[off:]))
		hlen := int(binary.BigEndian.Uint16(buf[off+4:]))
		ver := binary.BigEndian.Uint16(buf[off+6:])
		op := binary.BigEndian.Uint32(buf[off+8:])
		if hlen < 16 || plen < hlen || off+plen > len(buf) {
			return nil
		}
		body := buf[off+hlen : off+plen]
		off += plen

		switch op {
		case opAuthReply:
			var r struct {
				Code int `json:"code"`
			}
			json.Unmarshal(body, &r)
			if err := onAuth(r.Code); err != nil {
				return err
			}
		case opMessage:
			var inner []byte
			var err error
			switch ver {
			case 2:
				var zr io.ReadCloser
				if zr, err = zlib.NewReader(bytes.NewReader(body)); err == nil {
					inner, err = io.ReadAll(zr)
					zr.Close()
				}
			case 3:
				inner, err = io.ReadAll(brotli.NewReader(bytes.NewReader(body)))
			default:
				c.handleCmd(ctx, body)
				continue
			}
			if err != nil {
				log.Printf("解压弹幕消息失败：%v", err)
				continue
			}
			if err := c.handlePackets(ctx, inner, onAuth); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Client) handleCmd(ctx context.Context, body []byte) {
	var head struct {
		Cmd string `json:"cmd"`
	}
	if json.Unmarshal(body, &head) != nil {
		return
	}
	cmd, _, _ := strings.Cut(head.Cmd, ":")
	if Debug {
		c.countCmd(cmd)
		if strings.Contains(cmd, "GIFT") {
			c.sink.AppendDebug(body)
		}
	}
	switch cmd {
	case "SEND_GIFT":
		c.handleGift(body)
	case "SEND_GIFT_V2": // 2026 年起的礼物消息，内容为 protobuf
		c.handleGiftV2(body)
	case "LIVE":
		go func() {
			if sleepCtx(ctx, 3*time.Second) {
				c.refreshRoom(ctx)
			}
		}()
	case "PREPARING":
		c.sink.SetRoomInfo("", false)
		c.sink.LiveEnded()
	case "LOG_IN_NOTICE":
		// 未登录的连接会收到这条提示；已登录还收到说明登录失效
		var m struct {
			Data struct {
				NoticeMsg string `json:"notice_msg"`
			} `json:"data"`
		}
		json.Unmarshal(body, &m)
		if c.uid != 0 {
			log.Printf("B 站提示未登录：%s。请在主播后台重新扫码登录", m.Data.NoticeMsg)
			c.sink.SetStatus("limited", "登录已失效 · 收不到礼物")
		} else if Debug {
			log.Printf("B 站提示：%s", m.Data.NoticeMsg)
		}
	case "CUT_OFF":
		var m struct {
			Msg string `json:"msg"`
		}
		json.Unmarshal(body, &m)
		log.Printf("直播被超管切断：%s", m.Msg)
	}
}

// countCmd 在调试模式下每 30 秒汇总一次收到的消息类型。
func (c *Client) countCmd(cmd string) {
	c.dbgMu.Lock()
	defer c.dbgMu.Unlock()
	if c.dbgCounts == nil {
		c.dbgCounts, c.dbgSince = map[string]int{}, time.Now()
	}
	c.dbgCounts[cmd]++
	if time.Since(c.dbgSince) >= 30*time.Second {
		log.Printf("[调试] 近 30 秒收到的消息：%v", c.dbgCounts)
		c.dbgCounts, c.dbgSince = map[string]int{}, time.Now()
	}
}

type sendGiftMsg struct {
	Data struct {
		UID       flexInt `json:"uid"`
		Uname     string  `json:"uname"`
		GiftID    flexInt `json:"giftId"`
		GiftName  string  `json:"giftName"`
		Num       flexInt `json:"num"`
		Price     flexInt `json:"price"`
		Timestamp flexInt `json:"timestamp"`
		Tid       flexStr `json:"tid"`
		Rnd       flexStr `json:"rnd"`
		BlindGift *struct {
			OriginalGiftName  string  `json:"original_gift_name"`
			OriginalGiftPrice flexInt `json:"original_gift_price"`
			GiftTipPrice      flexInt `json:"gift_tip_price"`
		} `json:"blind_gift"`
		SenderUinfo *struct {
			Base struct {
				Name string `json:"name"`
			} `json:"base"`
		} `json:"sender_uinfo"`
	} `json:"data"`
}

func (c *Client) handleGift(body []byte) {
	var m sendGiftMsg
	if json.Unmarshal(body, &m) != nil {
		return
	}
	d := m.Data
	if Debug {
		box := ""
		if d.BlindGift != nil {
			box = fmt.Sprintf("（%s 开出，单价 %d 金瓜子）", d.BlindGift.OriginalGiftName, d.BlindGift.OriginalGiftPrice)
		}
		log.Printf("[调试] 礼物：%s 送出 %s ×%d，单价 %d 金瓜子%s", d.Uname, d.GiftName, d.Num, d.Price, box)
	}
	if d.BlindGift == nil {
		return
	}
	c.sink.AppendRaw(body)

	value := int64(d.BlindGift.GiftTipPrice)
	if value <= 0 {
		value = int64(d.Price)
	}
	num := int(d.Num)
	if num <= 0 {
		num = 1
	}
	name := d.Uname
	if name == "" && d.SenderUinfo != nil {
		name = d.SenderUinfo.Base.Name
	}
	id := string(d.Tid)
	if id == "" {
		id = fmt.Sprintf("%d-%d-%d-%s", d.UID, d.GiftID, d.Timestamp, d.Rnd)
	}
	c.sink.AddEvent(store.Event{
		ID:        id,
		TS:        time.Now().UnixMilli(),
		UID:       int64(d.UID),
		Uname:     name,
		Box:       d.BlindGift.OriginalGiftName,
		BoxPrice:  int64(d.BlindGift.OriginalGiftPrice),
		GiftID:    int64(d.GiftID),
		Gift:      d.GiftName,
		GiftPrice: value,
		Num:       num,
	})
}

// ---------- 工具函数 ----------

func keyFromURL(u string) string {
	base := path.Base(u)
	return strings.TrimSuffix(base, path.Ext(base))
}

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
