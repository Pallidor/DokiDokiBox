// Package config 读写程序旁边的 config.json。
package config

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"slices"
	"strings"
)

type Config struct {
	RoomID        int64    `json:"roomId"`           // 直播间号（短号、长号都可以）
	Cookie        string   `json:"cookie,omitempty"` // 扫码登录后自动保存，只含 SESSDATA 和 DedeUserID
	Listen        string   `json:"listen"`           // 本地网页服务地址
	BoxNames      []string `json:"boxNames"`         // 统计哪些盲盒，留空表示全部盲盒
	BigPrizeValue float64  `json:"bigPrizeValue"`    // 单个价值 ≥ 该值（电池）算大奖
	Accent        string   `json:"accent"`           // 主题色
}

func Default() *Config {
	return &Config{
		Listen:        "127.0.0.1:11124",
		BoxNames:      []string{"心动盲盒"},
		BigPrizeValue: 1000,
		Accent:        "#FF6F9C",
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
	if cfg.BigPrizeValue <= 0 {
		cfg.BigPrizeValue = Default().BigPrizeValue
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

func (c *Config) MatchBox(name string) bool {
	return len(c.BoxNames) == 0 || slices.Contains(c.BoxNames, name)
}

// BigGold 返回大奖阈值，单位金瓜子（1 电池 = 100 金瓜子）。
func (c *Config) BigGold() int64 {
	return int64(math.Round(c.BigPrizeValue * 100))
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
