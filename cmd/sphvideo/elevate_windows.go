//go:build windows

package main

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	seeMaskNoAsync = 0x00000100
	swShowNormal   = 1
)

type shellExecuteInfo struct {
	cbSize       uint32
	fMask        uint32
	hwnd         uintptr
	lpVerb       *uint16
	lpFile       *uint16
	lpParameters *uint16
	lpDirectory  *uint16
	nShow        int32
	hInstApp     uintptr
	lpIDList     uintptr
	lpClass      *uint16
	hkeyClass    uintptr
	dwHotKey     uint32
	hIcon        uintptr
	hProcess     uintptr
}

var procShellExecuteExW = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")

// requireAdmin restarts the app with the elevated token when it runs without
// administrator rights, and reports whether main should exit. The CA goes into
// the machine's root store, which needs the elevated token and installs and
// removes it without prompts.
func requireAdmin() bool {
	if windows.GetCurrentProcessToken().IsElevated() {
		return false
	}
	exe, err := os.Executable()
	if err != nil {
		notifyAdminRequired("")
		return true
	}
	args := ""
	for _, arg := range os.Args[1:] {
		if args != "" {
			args += " "
		}
		args += windows.EscapeArg(arg)
	}
	dir, _ := os.Getwd()
	info := shellExecuteInfo{
		fMask:        seeMaskNoAsync,
		lpVerb:       utf16Ptr("runas"),
		lpFile:       utf16Ptr(exe),
		lpParameters: utf16Ptr(args),
		lpDirectory:  utf16Ptr(dir),
		nShow:        swShowNormal,
	}
	info.cbSize = uint32(unsafe.Sizeof(info))
	if ok, _, callErr := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&info))); ok == 0 {
		if callErr == windows.ERROR_CANCELLED {
			notifyAdminRequired("已取消管理员授权，程序未启动。")
		} else {
			notifyAdminRequired("请右键程序，选择「以管理员身份运行」。")
		}
	}
	return true
}

func utf16Ptr(s string) *uint16 {
	p, _ := windows.UTF16PtrFromString(s)
	return p
}

func notifyAdminRequired(detail string) {
	msg := "本程序需要管理员权限，用于安装临时证书和修改系统代理。"
	if detail != "" {
		msg += "\n\n" + detail
	}
	_, _ = windows.MessageBox(0, utf16Ptr(msg), utf16Ptr("视频号视频下载"),
		windows.MB_OK|windows.MB_ICONERROR)
}
