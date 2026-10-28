// 心动盲盒数据台：B 站直播间「心动盲盒」统计工具。
//
// 代码结构：
//
//	main.go           入口：命令行参数、读取配置、启动网页服务
//	internal/config   config.json
//	internal/store    盲盒记录和直播场次（每个直播间一个目录）
//	internal/bili     B 站接口：弹幕长连接、礼物解析、扫码登录
//	internal/app      运行状态、统计、叠加层设置、网页接口、演示模式
//	web/              主播后台和 OBS 叠加层页面（编译时打包进 exe）
package main

import (
	"bufio"
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Pallidor/DokiDokiBox/internal/app"
	"github.com/Pallidor/DokiDokiBox/internal/bili"
	"github.com/Pallidor/DokiDokiBox/internal/config"
)

// version 是当前版本号，显示在程序窗口和主播后台底部。
const version = "0.0.1-beta"

//go:embed web
var webFS embed.FS

func main() {
	setupConsole()
	room := flag.Int64("room", 0, "直播间号（覆盖 config.json）")
	demo := flag.Bool("demo", false, "示例模式：不连接 B 站，生成模拟数据预览界面")
	port := flag.Int("port", 0, "本地网页端口（覆盖 config.json）")
	noBrowser := flag.Bool("no-browser", false, "启动后不自动打开浏览器")
	flag.BoolVar(&bili.Debug, "debug", false, "打印收到的每一条礼物消息，用于排查问题")
	flag.Parse()
	app.Version = version

	base := baseDir()
	removeOldExe()
	cfgPath := filepath.Join(base, "config.json")
	cfg, exists, err := config.Load(cfgPath)
	if err != nil {
		fatal("读取 config.json 失败：%v", err)
	}
	if *room > 0 {
		cfg.RoomID = *room
	}
	if *port > 0 {
		host, _, _ := net.SplitHostPort(cfg.Listen)
		cfg.Listen = net.JoinHostPort(host, strconv.Itoa(*port))
	}
	if !*demo && !exists {
		if err := config.Save(cfgPath, cfg); err != nil {
			log.Printf("保存 config.json 失败：%v", err)
		}
	}

	dataDir := filepath.Join(base, "data")
	if *demo {
		dataDir = filepath.Join(base, "data-demo")
	}
	a, err := app.New(cfg, cfgPath, dataDir, *demo)
	if err != nil {
		fatal("打开数据目录失败：%v", err)
	}
	defer a.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		fatal("无法监听 %s（端口可能被占用，可用 -port 换一个）：%v", cfg.Listen, err)
	}
	static, _ := fs.Sub(webFS, "web")
	srv := &http.Server{Handler: a.Routes(static)}
	go func() {
		<-ctx.Done()
		srv.Close()
	}()
	a.Start(ctx, stop)

	url := "http://" + displayAddr(ln.Addr().String())
	fmt.Println()
	fmt.Println("  心动盲盒数据台 v" + version + " 已启动" + map[bool]string{true: "（示例模式）", false: ""}[*demo])
	fmt.Println("  主播后台：    " + url + "/")
	fmt.Println("  OBS 叠加层：  " + url + "/overlays")
	fmt.Println("  数据目录：    " + dataDir)
	if !*demo && cfg.RoomID <= 0 {
		fmt.Println("  首次使用：请在浏览器打开的主播后台里设置直播间")
	}
	fmt.Println("  关闭此窗口、按 Ctrl+C 或在主播后台点「关闭程序」退出")
	fmt.Println()
	if !*noBrowser {
		exec.Command("rundll32", "url.dll,FileProtocolHandler", url+"/").Start()
	}

	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fatal("网页服务出错：%v", err)
	}
	a.StopClient()
}

// baseDir 返回程序所在目录；go run 时退回当前目录。
func baseDir() string {
	exe, err := os.Executable()
	if err != nil || strings.HasPrefix(exe, os.TempDir()) || strings.Contains(exe, "go-build") {
		wd, _ := os.Getwd()
		return wd
	}
	return filepath.Dir(exe)
}

// removeOldExe 删除更新时留下的旧版程序（DokiDokiBox.exe~）。
// 程序运行时 Windows 不允许覆盖它，重新编译会把正在运行的 exe 改名成 .exe~；新版启动时旧版已经退出，就能删掉了。
// 如果旧版还在运行，删除会失败，忽略即可。
func removeOldExe() {
	if exe, err := os.Executable(); err == nil {
		os.Remove(exe + "~")
	}
}

func displayAddr(addr string) string {
	host, port, _ := net.SplitHostPort(addr)
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "\n错误："+format+"\n\n按回车键退出…", args...)
	bufio.NewReader(os.Stdin).ReadString('\n')
	os.Exit(1)
}
