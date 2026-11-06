//go:build windows

package remotedesk

import (
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"CloudInit/logger"
)

var (
	user32                       = windows.NewLazySystemDLL("user32.dll")
	procEnumWindows              = user32.NewProc("EnumWindows")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procShowWindow               = user32.NewProc("ShowWindow")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procGetWindowTextW           = user32.NewProc("GetWindowTextW")
	procGetWindowTextLengthW     = user32.NewProc("GetWindowTextLengthW")
)

const swHide = 0

// hideWindowsOfProcess 延迟隐藏指定进程的主窗口
//
// 只调用 ShowWindow(SW_HIDE)，绝不发送 WM_CLOSE / 结束进程：
// 窗口关闭会导致临时密码不可见，隐藏后仍可从托盘恢复。
func hideWindowsOfProcess(exeName string, waitSeconds int) {
	for i := 0; i < waitSeconds; i++ {
		time.Sleep(time.Second)

		pids := findProcessIDs(exeName)
		if len(pids) == 0 {
			continue
		}

		if hidden := hideProcessWindows(pids); hidden > 0 {
			logger.Infof("[远程桌面] 已隐藏 %s 主窗口（%d 个）", exeName, hidden)
			return
		}
	}

	logger.Debugf("[远程桌面] 未找到 %s 窗口，跳过隐藏", exeName)
}

// findProcessIDs 查找指定进程名的所有 PID
func findProcessIDs(exeName string) []uint32 {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snapshot)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))

	pids := make([]uint32, 0, 2)
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if strings.EqualFold(syscall.UTF16ToString(entry.ExeFile[:]), exeName) {
			pids = append(pids, entry.ProcessID)
		}
	}

	return pids
}

// hideProcessWindows 隐藏指定进程的所有可见顶层窗口，返回隐藏数量
func hideProcessWindows(pids []uint32) int {
	pidSet := make(map[uint32]struct{}, len(pids))
	for _, pid := range pids {
		pidSet[pid] = struct{}{}
	}

	hidden := 0
	callback := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		var pid uint32
		procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		if _, ok := pidSet[pid]; !ok {
			return 1
		}

		if visible, _, _ := procIsWindowVisible.Call(hwnd); visible == 0 {
			return 1
		}

		// 跳过提示类窗口，仅隐藏真正的界面窗口
		if title := windowTitle(hwnd); title == "" {
			return 1
		}

		procShowWindow.Call(hwnd, swHide)
		hidden++
		return 1
	})

	procEnumWindows.Call(callback, 0)
	return hidden
}

// windowTitle 读取窗口标题
func windowTitle(hwnd uintptr) string {
	length, _, _ := procGetWindowTextLengthW.Call(hwnd)
	if length == 0 {
		return ""
	}

	buf := make([]uint16, length+1)
	procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))

	return syscall.UTF16ToString(buf)
}
