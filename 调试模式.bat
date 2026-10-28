@echo off
chcp 65001 >nul
cd /d "%~dp0"
echo 调试模式：收到的所有礼物消息会记录到 data\debug-gifts.jsonl
DokiDokiBox.exe -debug
