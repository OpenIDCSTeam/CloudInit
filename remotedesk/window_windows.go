//go:build windows

package remotedesk

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"CloudInit/logger"
)

var (
	user32          = windows.NewLazySystemDLL("user32.dll")
	procFindWindowW = user32.NewProc("FindWindowW")
	procShowWindow  = user32.NewProc("ShowWindow")
)

// hideToDeskWindow 延迟隐藏ToDesk主窗口（保留托盘图标）
func hideToDeskWindow() {
	titles := []string{"ToDesk", "ToDesk远程控制"}

	// 等待ToDesk窗口创建
	for i := 0; i < 15; i++ {
		time.Sleep(time.Second)

		for _, title := range titles {
			titlePtr, err := windows.UTF16PtrFromString(title)
			if err != nil {
				continue
			}

			hwnd, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(titlePtr)))
			if hwnd != 0 {
				const swHide = 0
				_, _, _ = procShowWindow.Call(hwnd, swHide)
				logger.Infof("[ToDesk] 已隐藏ToDesk主窗口")
				return
			}
		}
	}

	logger.Debugf("[ToDesk] 未找到ToDesk窗口，跳过隐藏")
}
