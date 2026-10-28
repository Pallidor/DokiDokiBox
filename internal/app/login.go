package app

// 可选的 B 站扫码登录：不登录时 B 站几乎不推送礼物消息，盲盒统计不到。
// 登录后只在 config.json 保存登录必需的 SESSDATA 和 DedeUserID，不保存可用于发起操作的 bili_jct，
// 并且只发送给 B 站自己的接口。接口格式见 bilibili-API-collect 的 docs/login/login_action/QR.md。

import (
	"context"
	"errors"
	"fmt"
	"github.com/Pallidor/DokiDokiBox/internal/bili"
	"github.com/Pallidor/DokiDokiBox/internal/config"
	"log"

	"rsc.io/qr"
)

// LoginStart 申请一个新的登录二维码。
func (a *App) LoginStart(ctx context.Context) error {
	if a.demo {
		return errors.New("示例模式不需要登录")
	}
	u, key, err := bili.GenerateQR(ctx)
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.qrKey, a.qrURL = key, u
	a.mu.Unlock()
	return nil
}

// LoginQR 返回当前登录二维码的 PNG 图片。
func (a *App) LoginQR() ([]byte, error) {
	a.mu.RLock()
	u := a.qrURL
	a.mu.RUnlock()
	if u == "" {
		return nil, errors.New("还没有申请二维码")
	}
	c, err := qr.Encode(u, qr.M)
	if err != nil {
		return nil, err
	}
	c.Scale = 6
	return c.PNG(), nil
}

// LoginPoll 查询扫码状态；扫码确认后保存登录信息并重新连接直播间。
// 返回的 state 为 waiting（未扫码）、scanned（已扫码待确认）、expired（二维码过期）或 done。
func (a *App) LoginPoll(ctx context.Context) (state, uname string, err error) {
	a.mu.RLock()
	key := a.qrKey
	a.mu.RUnlock()
	if key == "" {
		return "expired", "", nil
	}
	state, cookie, err := bili.PollQR(ctx, key)
	if err != nil || state != "done" {
		return state, "", err
	}
	if uname, err = bili.LoginName(ctx, cookie); err != nil {
		return "", "", err
	}
	if err := a.SetCookie(cookie); err != nil {
		return "", "", err
	}
	a.mu.Lock()
	a.qrKey, a.qrURL = "", ""
	a.mu.Unlock()
	log.Printf("已扫码登录 B 站账号：%s", uname)
	return "done", uname, nil
}

// SetCookie 保存（或清除）登录信息并重新连接直播间。
func (a *App) SetCookie(cookie string) error {
	a.mu.Lock()
	cfg := *a.cfg
	cfg.Cookie = cookie
	if err := config.Save(a.cfgPath, &cfg); err != nil {
		a.mu.Unlock()
		return fmt.Errorf("保存 config.json 失败：%v", err)
	}
	a.cfg.Cookie = cookie
	room := a.roomID
	if cookie == "" {
		a.login = ""
	}
	a.mu.Unlock()
	if room != 0 {
		a.startClient(room)
	}
	return nil
}

func (a *App) SetLogin(uname string) {
	a.mu.Lock()
	changed := a.login != uname
	a.login = uname
	a.mu.Unlock()
	if changed {
		a.hub.Broadcast("status", map[string]string{"login": uname})
	}
}
