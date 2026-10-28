package bili

// 网页扫码登录接口，见 bilibili-API-collect 的 docs/login/login_action/QR.md。
// 登录信息只保留 SESSDATA 和 DedeUserID（不保留可用于发起操作的 bili_jct）。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Pallidor/DokiDokiBox/internal/config"
)

// GenerateQR 申请一个登录二维码，返回二维码内容（网址）和查询用的 key。
func GenerateQR(ctx context.Context) (qrURL, key string, err error) {
	var d struct {
		URL string `json:"url"`
		Key string `json:"qrcode_key"`
	}
	code, msg, err := NewClient(nil, 0, "").getAPI(ctx, "https://passport.bilibili.com/x/passport-login/web/qrcode/generate", &d)
	if err != nil {
		return "", "", err
	}
	if code != 0 || d.Key == "" {
		return "", "", fmt.Errorf("获取二维码失败：%d %s", code, msg)
	}
	return d.URL, d.Key, nil
}

// PollQR 查询扫码状态：waiting（未扫码）、scanned（已扫码待确认）、expired（二维码过期）或 done。
// done 时返回只含登录必需项的 cookie。
func PollQR(ctx context.Context, key string) (state, cookie string, err error) {
	d, setCookies, err := pollQR(ctx, key)
	if err != nil {
		return "", "", err
	}
	switch d.Code {
	case 86101:
		return "waiting", "", nil
	case 86090:
		return "scanned", "", nil
	case 0:
	default: // 86038 二维码失效，其他情况也让用户重新扫码
		return "expired", "", nil
	}

	// 登录成功：登录信息在响应的 Set-Cookie 里；旧版接口也放在跳转链接的参数里，两处都看。
	// 都保留原始（未解码）的值，和浏览器里的 Cookie 一致
	parts := []string{}
	for _, ck := range setCookies {
		parts = append(parts, ck.Name+"="+ck.Value)
	}
	if pu, err := url.Parse(d.URL); err == nil && pu.RawQuery != "" {
		parts = append(parts, strings.Split(pu.RawQuery, "&")...)
	}
	cookie = config.MinimizeCookie(strings.Join(parts, ";"))
	if !strings.Contains(cookie, "SESSDATA=") {
		// 只记录字段名，不记录值，便于排查
		names := []string{}
		for _, p := range parts {
			k, _, _ := strings.Cut(p, "=")
			names = append(names, k)
		}
		log.Printf("扫码登录成功，但返回里没有 SESSDATA（返回的字段：%v）", names)
		return "", "", errors.New("登录成功但没有拿到登录信息，请重新扫码")
	}
	return "done", cookie, nil
}

type qrPollData struct {
	URL  string `json:"url"`
	Code int    `json:"code"`
}

// pollQR 查询一次扫码状态，同时返回响应里的 Set-Cookie（登录成功时登录信息在这里）。
func pollQR(ctx context.Context, key string) (qrPollData, []*http.Cookie, error) {
	var d qrPollData
	u := "https://passport.bilibili.com/x/passport-login/web/qrcode/poll?qrcode_key=" + url.QueryEscape(key)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return d, nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", "https://www.bilibili.com/")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return d, nil, err
	}
	defer resp.Body.Close()
	var env struct {
		Code    int        `json:"code"`
		Message string     `json:"message"`
		Data    qrPollData `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&env); err != nil {
		return d, nil, fmt.Errorf("扫码状态返回异常（HTTP %d）", resp.StatusCode)
	}
	if env.Code != 0 {
		return d, nil, fmt.Errorf("查询扫码状态失败：%d %s", env.Code, env.Message)
	}
	return env.Data, resp.Cookies(), nil
}
