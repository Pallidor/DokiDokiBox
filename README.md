# DokiDokiBox · 心动盲盒数据台

B 站直播间「心动盲盒」统计工具：实时记录观众开出的每一个盲盒，提供主播后台数据台和 OBS 叠加层（本场战报）。

单个 `DokiDokiBox.exe`，双击即可运行，不需要安装任何环境，数据只保存在本机。到 [Releases](https://github.com/Pallidor/DokiDokiBox/releases) 下载最新版。

当前版本：**v0.0.1-beta**（测试版，欢迎在 [Issues](https://github.com/Pallidor/DokiDokiBox/issues) 反馈问题）。

> 本工具是个人开发的第三方工具，与哔哩哔哩官方无关。

许可：[MIT](LICENSE)

## 快速开始

1. 双击 `DokiDokiBox.exe`，浏览器会自动打开主播后台 `http://127.0.0.1:11124/`。
2. 第一次使用时，页面上会提示填写直播间号（也可以直接粘贴直播间链接），点「开始使用」。
3. **扫码登录 B 站**：点右上角的「扫码登录」，用手机 B 站 App 扫一扫，在手机上确认（建议用小号，见下文）。
4. 在 OBS 中添加叠加层：点主播后台标题旁边的「OBS 叠加层」，复制地址，在 OBS 里「来源 → + → 浏览器」粘贴，宽 420、高 600。

想先看看效果？双击 `演示模式.bat`，会生成模拟数据并持续模拟送盲盒（不连接 B 站，数据存放在 `data-demo/`，每次启动都会重置）。演示用的奖池（礼物名称、价值、概率）来自社区文档 [bilibili-API-collect](https://github.com/pskdje/bilibili-API-collect) 在 2025 年 6 月收录的官方概率公示，当前奖池可能已调整。

程序运行期间才会记录数据，直播时请保持它开着。

- **换直播间**：点主播后台右上角的「切换直播间」，输入新的直播间号或粘贴直播间链接。程序会先确认直播间存在，再保存到 `config.json` 并重新连接，不用重启。每个直播间的记录分开保存，切回原来的直播间还能看到之前的数据。
- **关闭程序**：点主播后台右上角的「关闭程序」；也可以直接关掉程序窗口。

## 扫码登录与安全

程序按直播间号连接。B 站只把礼物消息推送给已登录的观众（实测未登录时 1 分钟收不到一条礼物），所以需要扫码登录一次。

**用什么账号**：随便注册一个普通 B 站小号就行，不用充值、不用开大会员，也不用是主播本人。为了安心，建议不要用存有 B 币或电池余额的主号。

- 点主播后台右上角的「扫码登录」（没登录时页面顶部也有黄色提示），用手机 B 站 App 的「扫一扫」扫码，在手机上点「确认登录」。登录成功后自动重新连接，不用重启。
- 登录信息只保存在本机 `config.json` 的 `cookie` 里，并且只保存登录必需的 `SESSDATA` 和 `DedeUserID`，不保存可用来发弹幕、送礼物的 `bili_jct`。不要把 `config.json` 发给别人。
- 程序只访问 B 站自己的接口（`bilibili.com`），不连接任何第三方服务器；登录信息只用来接收直播间的礼物消息。
- 登录过期后，后台会提示「登录已失效」，重新扫码即可。点「已登录 · 昵称」可以退出登录或换账号；也可以在手机 B 站 App 的登录设备管理里让它下线。
- 主播后台和叠加层网页默认只监听本机 `127.0.0.1`，局域网里的其他设备打不开；数据只存在程序旁边的 `data/` 文件夹。

## 配置文件 config.json

首次运行会自动生成，修改后需重启程序：

| 字段 | 说明 | 默认值 |
| --- | --- | --- |
| `roomId` | 直播间号，短号、长号都可以；第一次使用时在主播后台填写，之后也可以在主播后台修改 | 空 |
| `cookie` | 扫码登录后自动保存，不需要手动填写 | 空 |
| `boxNames` | 统计哪些盲盒，按盲盒名称匹配；填 `[]` 表示统计所有盲盒 | `["心动盲盒"]` |
| `bigPrizeValue` | 单个礼物价值 ≥ 该值（电池）算「大奖」 | `1000` |
| `accent` | 主播后台的主题色（叠加层的颜色在叠加层设置页调整） | `#FF6F9C` |
| `listen` | 本地网页服务地址；局域网内其他电脑上的 OBS 要访问时改为 `0.0.0.0:11124` | `127.0.0.1:11124` |

## OBS 叠加层

打开 `http://127.0.0.1:11124/overlays`（主播后台标题旁边的「OBS 叠加层」按钮），可以选择版式（完全版，或只显示标题、盲盒数和观众净盈亏的简易版，简易版在 OBS 里高度填 200 即可），调整本场战报的标题、主题色、背景不透明度，以及显示哪些内容（直播时长、最近大奖、欧皇榜、距上次大奖盒数）。修改后自动保存在 `data/overlay.json`，OBS 里的叠加层会立即刷新，不需要重新粘贴地址。

## 命令行参数

收不到盲盒时，可以先关掉程序，双击 `调试模式.bat` 打开（等同于 `-debug`），再送一个礼物，然后查看程序窗口和 `data/room-直播间号/debug-gifts.jsonl`。

```
DokiDokiBox.exe -room 123456     临时指定直播间（会覆盖 config.json）
DokiDokiBox.exe -port 11125      换一个端口（11124 被占用时）
DokiDokiBox.exe -demo            演示模式
DokiDokiBox.exe -no-browser      启动后不自动打开浏览器
DokiDokiBox.exe -debug           打印收到的每条礼物消息，以及每 30 秒的消息类型统计；
                               收到的所有礼物类消息原文记录到 data/room-直播间号/debug-gifts.jsonl
```

## 数据说明

- 单位：电池（1 电池 = 100 金瓜子 = 0.1 元）。
- 投入 = 盲盒单价 × 数量；开出价值 = 开出礼物的单价 × 数量；净盈亏为观众视角（开出 − 投入）。
- 场次：程序根据开播 / 下播状态自动划分（每分钟检查一次，并监听开播、下播消息）。「本场」= 正在直播的这一场，下播后显示上一场。
- 未开播时收到的盲盒会单独算一段「未开播」，这时「本场」显示的就是这一段；开播时它自动结束，换成正式的直播场次。超过 2 小时没有新记录，下一条另起一段。
- 数据目录 `data/`：
  - `room-直播间号/`：每个直播间一个文件夹，里面有
    - `events.jsonl`：每个盲盒开出记录一行，可备份；
    - `sessions.json`：直播场次；
    - `raw-gifts.jsonl`：B 站推送的原始盲盒消息，用于核对字段含义；
  - `overlay.json`：OBS 叠加层的外观设置（所有直播间共用）。
- 旧版本直接放在 `data/` 里的记录，会在第一次启动新版本时自动移到当前直播间的文件夹；之前切换直播间留下的错乱场次也会自动整理。
- 导出 CSV 可直接用 Excel 打开。

B 站 2026 年起把礼物消息改成了 `SEND_GIFT_V2`（内容为 protobuf），程序同时支持新旧两种格式；十连盲盒等一条消息里的多个开出结果都会分别记录。

价格字段的取法：盲盒单价取 `blind_gift.original_gift_price`，开出礼物价值取 `blind_gift.gift_tip_price`（缺失时取 `price`）。如果统计结果和你在直播间看到的不一致，请对照 `raw-gifts.jsonl` 里的原始消息检查。

## 更新日志

### v0.0.1-beta

第一个测试版。

- 实时记录直播间心动盲盒：开出的礼物、价值、观众盈亏，支持 B 站新版礼物消息（SEND_GIFT_V2）和十连盲盒。
- 主播后台：本场 / 今日 / 近 7 天 / 近 30 天统计，档位爆率、送礼者排行、历史趋势，导出 CSV。
- OBS 叠加层「本场战报」：完全版 / 简易版，标题、主题色、透明度、显示内容可在网页上调整，实时生效。
- 扫码登录后连接直播间（用小号即可）。
- 每个直播间的数据分开保存；未开播时收到的盲盒单独记为「未开播」时段。
- 全部操作都在网页上完成：首次使用填写直播间、切换直播间、扫码登录、关闭程序；演示模式不连接 B 站即可预览全部界面。

已知限制：程序运行期间才会记录；礼物消息来自 B 站网页接口，B 站调整格式时可能需要更新程序。

## 安全校验与自己编译

[发布页](https://github.com/Pallidor/DokiDokiBox/releases)提供程序包 `DokiDokiBox-版本号.zip` 和 `SHA256SUMS.txt`（exe 与程序包的 SHA256 校验值），源码包就是发布页上 GitHub 自动生成的 `Source code (zip)`。

- **确认下载的文件没被篡改**：在下载目录打开命令行，运行 `certutil -hashfile DokiDokiBox.exe SHA256`，结果应与 `SHA256SUMS.txt` 里 `DokiDokiBox.exe` 那一行相同。
- **对 exe 不放心，可以自己编译**：下载发布页上的 `Source code (zip)` 并解压，安装 [Go](https://go.dev/dl/)（国内可用 [golang.google.cn](https://golang.google.cn/dl/)，任意较新版本都行），双击源码里的 `自编译.bat`。脚本会自动使用和发布版相同的 Go 1.27.0 与相同的编译参数，编出来的 `DokiDokiBox.exe` 与发布版**逐字节相同**，脚本最后会显示它的 SHA256 方便对比。

程序不需要管理员权限，不改系统设置，只访问 B 站自己的接口和本机网页。

## 许可

代码使用 [MIT 许可](LICENSE)，作者 Pallidor。

## 从源码构建

B 站接口与弹幕协议参考了社区文档 [bilibili-API-collect](https://github.com/pskdje/bilibili-API-collect)（`docs/live/message_stream.md`、`docs/live/gift.md`）。

双击 `自编译.bat` 即可编译。手动编译时请使用相同的参数（这样结果才能与发布版逐字节相同）：

```
set GOTOOLCHAIN=go1.27.0
set CGO_ENABLED=0
go test ./...
go build -trimpath -buildvcs=false -ldflags "-s -w" -o DokiDokiBox.exe .
```

exe 的图标和文件信息（产品名、版本、说明）在 `icon_windows.syso` 里，`go build` 会自动带上（文件名结尾的 `_windows` 表示只在编译 Windows 版时使用）。修改版本号或图标后需要重新生成：

```
go run github.com/tc-hib/go-winres@v0.3.3 simply --arch amd64 --out icon_windows --no-suffix --manifest cli --icon assets/icon.ico --product-name "心动盲盒数据台" --product-version "0.0.1-beta" --file-version "0.0.1.0" --file-description "心动盲盒数据台 - B 站直播间心动盲盒统计工具" --original-filename "DokiDokiBox.exe"
ren icon_windows icon_windows.syso
```

同时修改 `main.go` 里的 `version`。

代码结构：

| 位置 | 内容 |
| --- | --- |
| `main.go` | 入口：命令行参数、读取配置、启动网页服务 |
| `internal/config/` | 配置文件 `config.json` 的读写 |
| `internal/store/` | 盲盒记录与直播场次（每个直播间一个目录、未开播时段、旧数据迁移） |
| `internal/bili/client.go` | B 站直播间连接：房间信息、wbi 签名、弹幕 WebSocket 协议、旧版礼物消息 |
| `internal/bili/giftv2.go` | 新版礼物消息 SEND_GIFT_V2（protobuf）解析 |
| `internal/bili/login.go` | 扫码登录接口 |
| `internal/app/app.go` | 运行状态与 SSE 实时推送；实现 `bili.Sink`，接收连接产生的事件 |
| `internal/app/stats.go` | 各项统计 |
| `internal/app/server.go` | 网页服务与接口 |
| `internal/app/overlay.go` | OBS 叠加层外观设置 |
| `internal/app/control.go` | 切换直播间、关闭程序 |
| `internal/app/login.go` | 扫码登录流程 |
| `internal/app/demo.go` | 演示模式数据生成 |
| `web/` | 主播后台、OBS 叠加层页面（编译时打包进 exe） |
| `assets/icon.png`、`assets/icon.ico`、`icon_windows.syso` | 程序图标和 exe 文件信息 |
| `自编译.bat` | 从源码编译，结果与发布版逐字节相同 |
| `LICENSE` | 开源许可 |

`internal/bili` 只通过 `Sink` 接口回报事件，不依赖程序其他部分，可以单独测试。
