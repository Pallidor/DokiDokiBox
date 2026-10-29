package app

// OBS 叠加层（本场战报）的外观设置：在 /overlays 页面上调整，保存在 data/overlay.json，
// 修改后通过 SSE 推送，OBS 里的叠加层会立即刷新，不需要重新复制地址。

import (
	"encoding/json"
	"errors"
	"github.com/Pallidor/DokiDokiBox/internal/config"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

type OverlaySettings struct {
	Layout       string  `json:"layout"`  // full 完全版 / simple 简易版（只显示盲盒数和观众净盈亏）
	Accent       string  `json:"accent"`  // 主题色，空表示跟随 config.json 的 accent
	Opacity      float64 `json:"opacity"` // 背景不透明度 0–1
	Title        string  `json:"title"`
	ShowDuration bool    `json:"showDuration"` // 直播时长
	ShowWins     bool    `json:"showWins"`     // 最近大奖
	ShowLucky    bool    `json:"showLucky"`    // 欧皇榜
	ShowSince    bool    `json:"showSince"`    // 距上次大奖
}

const defaultOverlayTitle = "盲盒 · 本场战报"

func defaultOverlaySettings() OverlaySettings {
	return OverlaySettings{
		Layout: "full", Opacity: 0.82, Title: defaultOverlayTitle,
		ShowDuration: true, ShowWins: true, ShowLucky: true, ShowSince: true,
	}
}

var hexColor = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

// normalize 修正超出范围的取值，保证写入文件和推送给页面的设置总是合法的。
func (s *OverlaySettings) normalize() {
	if s.Layout != "simple" {
		s.Layout = "full"
	}
	if !hexColor.MatchString(s.Accent) {
		s.Accent = ""
	}
	s.Accent = strings.ToUpper(s.Accent)
	if math.IsNaN(s.Opacity) {
		s.Opacity = defaultOverlaySettings().Opacity
	}
	s.Opacity = math.Round(min(1, max(0, s.Opacity))*100) / 100
	s.Title = strings.TrimSpace(s.Title)
	if s.Title == "" {
		s.Title = defaultOverlayTitle
	}
	if utf8.RuneCountInString(s.Title) > 24 {
		s.Title = string([]rune(s.Title)[:24])
	}
}

func overlayPath(dir string) string { return filepath.Join(dir, "overlay.json") }

func loadOverlaySettings(dir string) OverlaySettings {
	s := defaultOverlaySettings()
	if b, err := os.ReadFile(overlayPath(dir)); err == nil {
		json.Unmarshal(config.TrimBOM(b), &s)
	}
	s.normalize()
	return s
}

func (a *App) OverlaySettings() OverlaySettings {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.overlay
}

func (a *App) SetOverlaySettings(s OverlaySettings) (OverlaySettings, error) {
	s.normalize()
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return s, err
	}
	// 还没设置直播间时 data/ 可能还不存在
	if err := os.MkdirAll(a.dataDir, 0o755); err != nil {
		return s, err
	}
	p := overlayPath(a.dataDir)
	if err := os.WriteFile(p+".tmp", b, 0o644); err != nil {
		return s, err
	}
	if err := os.Rename(p+".tmp", p); err != nil {
		return s, errors.New("保存失败：" + err.Error())
	}
	a.mu.Lock()
	a.overlay = s
	a.mu.Unlock()
	a.hub.Broadcast("overlay", s)
	return s, nil
}
