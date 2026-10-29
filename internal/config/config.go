// Package config 读写程序旁边的 config.json。
package config

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
)

type Config struct {
	RoomID           int64   `json:"roomId"`           // 直播间号（短号、长号都可以）
	Cookie           string  `json:"cookie,omitempty"` // 扫码登录后自动保存，只含 SESSDATA 和 DedeUserID
	Listen           string  `json:"listen"`           // 本地网页服务地址
	BigPrizeMultiple float64 `json:"bigPrizeMultiple"` // 开出礼物价值 ≥ 盲盒单价 × 该倍数算大奖，不同价位的盲盒都适用
	Accent           string  `json:"accent"`           // 主题色
}

func Default() *Config {
	return &Config{
		Listen:           "127.0.0.1:11124",
		BigPrizeMultiple: 5,
		Accent:           "#FF6F9C",
	}
}

// Load 读取配置；文件不存在时返回默认配置和 exists=false。
func Load(path string) (cfg *Config, exists bool, err error) {
	cfg = Default()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	b = TrimBOM(b)
	if err := json.Unmarshal(b, cfg); err != nil {
		return nil, true, err
	}
	cfg.Cookie = MinimizeCookie(cfg.Cookie) // 手动填写的完整 Cookie 也只使用登录必需的几项
	if cfg.Listen == "" {
		cfg.Listen = Default().Listen
	}
	if !(cfg.BigPrizeMultiple > 0) {
		cfg.BigPrizeMultiple = Default().BigPrizeMultiple
	}
	if cfg.Accent == "" {
		cfg.Accent = Default().Accent
	}
	return cfg, true, nil
}

func Save(path string, cfg *Config) error {
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// IsBig 判断单价 boxPrice 的盲盒开出价值 giftPrice 的礼物是否算大奖。
func (c *Config) IsBig(giftPrice, boxPrice int64) bool {
	return boxPrice > 0 && float64(giftPrice) >= float64(boxPrice)*c.BigPrizeMultiple
}

// MinimizeCookie 只保留登录必需的 SESSDATA 和 DedeUserID，丢掉其余字段（如可用于发起操作的 bili_jct）。
func MinimizeCookie(raw string) string {
	keep := []string{}
	for _, part := range strings.Split(raw, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && v != "" && (k == "SESSDATA" || k == "DedeUserID") {
			keep = append(keep, k+"="+v)
		}
	}
	return strings.Join(keep, "; ")
}

func TrimBOM(b []byte) []byte {
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return b[3:]
	}
	return b
}
