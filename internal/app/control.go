package app

// 网页上的程序控制：切换直播间、关闭程序。

import (
	"context"
	"errors"
	"fmt"
	"github.com/Pallidor/DokiDokiBox/internal/bili"
	"github.com/Pallidor/DokiDokiBox/internal/config"
	"log"
	"strconv"
	"strings"
	"time"
)

// parseRoomInput 解析直播间号，也接受直接粘贴的直播间链接。
func parseRoomInput(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	for _, p := range []string{"https://", "http://", "live.bilibili.com/", "h5/"} {
		s = strings.TrimPrefix(s, p)
	}
	if i := strings.IndexAny(s, "?/#"); i >= 0 {
		s = s[:i]
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && n > 0
}

// startClient 按直播间号连接直播间，已有连接时先断开并等它收尾。
func (a *App) startClient(room int64) {
	a.clientMu.Lock()
	defer a.clientMu.Unlock()
	a.stopClientLocked()
	a.mu.RLock()
	cfg := *a.cfg
	a.mu.RUnlock()

	if room <= 0 {
		a.SetStatus("setup", "请先设置直播间")
		return
	}
	a.SetRoomID(room, 0)
	client := bili.NewClient(a, room, cfg.Cookie)
	ctx, cancel := context.WithCancel(a.ctx)
	done := make(chan struct{})
	a.clientStop, a.clientDone = cancel, done
	go func() {
		defer close(done)
		client.Run(ctx)
	}()
}

func (a *App) stopClientLocked() {
	if a.clientStop == nil {
		return
	}
	a.clientStop()
	select {
	case <-a.clientDone:
	case <-time.After(5 * time.Second):
	}
	a.clientStop, a.clientDone = nil, nil
}

// StopClient 断开直播间连接，程序退出前调用。
func (a *App) StopClient() {
	a.clientMu.Lock()
	defer a.clientMu.Unlock()
	a.stopClientLocked()
}

// resetRoom 结束当前直播间正在进行的场次并清空它的资料，切换直播间时调用。
func (a *App) resetRoom() {
	a.LiveEnded()
	a.mu.Lock()
	a.room, a.title, a.live, a.realRoom = bili.RoomInfo{}, "", false, 0
	a.mu.Unlock()
}

// SwitchRoom 切换到另一个直播间：先确认直播间存在，再写入 config.json 并重新连接。
func (a *App) SwitchRoom(ctx context.Context, room int64) error {
	if a.demo {
		return errors.New("示例模式不能修改直播间")
	}
	a.mu.RLock()
	cur := a.roomID
	a.mu.RUnlock()
	if room == cur {
		return nil
	}
	if _, err := bili.RoomInit(ctx, room); err != nil {
		return err
	}

	a.mu.Lock()
	cfg := *a.cfg
	cfg.RoomID = room
	if err := config.Save(a.cfgPath, &cfg); err != nil {
		a.mu.Unlock()
		return fmt.Errorf("保存 config.json 失败：%v", err)
	}
	a.cfg.RoomID = room
	a.mu.Unlock()

	a.resetRoom()
	log.Printf("切换到直播间 %d", room)
	a.startClient(room)
	a.SetStatus("connecting", "正在切换直播间…")
	return nil
}

// Shutdown 在响应发出后关闭程序。
func (a *App) Shutdown() {
	log.Printf("已从网页关闭程序")
	time.AfterFunc(300*time.Millisecond, a.quit)
}
