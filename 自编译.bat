@echo off
chcp 65001 >nul
setlocal
cd /d "%~dp0"

echo 盲盒数据台：从源码自己编译 DokiDokiBox.exe
echo.

where go >nul 2>nul
if errorlevel 1 goto nogo

rem 固定 Go 版本和编译参数，编出来的 exe 与发布版逐字节相同，可以用 SHA256 对比
set "GOTOOLCHAIN=go1.27.0"
set "CGO_ENABLED=0"
set "GOOS=windows"
set "GOARCH=amd64"
set "GOFLAGS="

rem 没有自己设置过下载源时：先用 Go 官方源，连不上再用国内镜像。依赖和 Go 本身都会用 go.sum 和官方校验数据库验证
for /f "delims=" %%i in ('go env GOPROXY') do set "CURPROXY=%%i"
if "%CURPROXY%"=="https://proxy.golang.org,direct" set "GOPROXY=https://proxy.golang.org|https://goproxy.cn|direct"

echo 第一次编译会自动下载 Go 1.27.0 和依赖，可能要等几分钟……
echo.
go build -trimpath -buildvcs=false -ldflags "-s -w" -o DokiDokiBox.exe .
if errorlevel 1 goto fail

echo.
echo 编译完成：%cd%\DokiDokiBox.exe
echo.
echo 它的 SHA256 是：
certutil -hashfile DokiDokiBox.exe SHA256 | findstr /v ":"
echo.
echo 和发布页 SHA256SUMS.txt 里 DokiDokiBox.exe 的值对比，相同就说明发布的 exe 正是这份源码编译出来的。
goto end

:nogo
echo 没有找到 Go。请先安装 Go（任意较新的版本都可以，脚本会自动使用 1.27.0 编译）：
echo   https://go.dev/dl/  或国内镜像  https://golang.google.cn/dl/
echo 安装后重新双击本脚本。
goto end

:fail
echo.
echo 编译失败，请把上面的错误信息反馈给作者。

:end
echo.
pause
