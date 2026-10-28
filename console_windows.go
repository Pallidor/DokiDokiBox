package main

import "syscall"

// setupConsole 把控制台切到 UTF-8，避免中文日志乱码。
func setupConsole() {
	k := syscall.NewLazyDLL("kernel32.dll")
	k.NewProc("SetConsoleOutputCP").Call(65001)
	k.NewProc("SetConsoleCP").Call(65001)
}
