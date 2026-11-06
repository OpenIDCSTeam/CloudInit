//go:build windows

package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"CloudInit/apppath"
	"CloudInit/logger"
)

const (
	createNewConsole       = 0x00000010
	createBreakawayFromJob = 0x01000000
)

// startDetachedBatch 以独立进程启动更新脚本
//
// 使用 CREATE_NEW_CONSOLE + SW_HIDE：
//   - 与父进程完全脱离，父进程退出后继续执行；
//   - 拥有自己的（隐藏）控制台，脚本里的 ping/sc 等控制台程序
//     复用该控制台，不会再各弹一个窗口。
func startDetachedBatch(batchPath string) error {
	cmd := exec.Command("cmd.exe", "/c", batchPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNewConsole | createBreakawayFromJob,
	}
	return cmd.Start()
}

// cleanupStaleUpdateArtifacts 清理旧版本遗留的更新脚本与临时文件
//
// 老版本的 _update.bat 会在“删除文件失败”时无限循环，并反复弹出
// `timeout /t 1 /nobreak` 控制台窗口。升级到新版本后，这里负责：
//  1. 结束仍在执行 _update.bat 的 cmd.exe；
//  2. 结束遗留的 timeout.exe（立即止住弹窗）；
//  3. 删除超过 30 分钟的 _update.bat / .update 残留。
func cleanupStaleUpdateArtifacts() {
	killStaleUpdateProcesses()

	exePath := apppath.ExePath()
	batchPath := filepath.Join(filepath.Dir(exePath), "_update.bat")
	tempPath := exePath + ".update"

	if info, err := os.Stat(batchPath); err == nil && time.Since(info.ModTime()) > 30*time.Minute {
		_ = os.Remove(batchPath)
	}
	if info, err := os.Stat(tempPath); err == nil && time.Since(info.ModTime()) > 30*time.Minute {
		_ = os.Remove(tempPath)
	}
}

// killStaleUpdateProcesses 结束卡死的更新脚本进程
func killStaleUpdateProcesses() {
	// 先结束 timeout.exe，弹窗会立刻停止（之后即使脚本继续也会被下面结束）
	killByImageName("timeout.exe")

	out, err := exec.Command("wmic", "process", "where", "name='cmd.exe'",
		"get", "CommandLine,ProcessId", "/format:csv").Output()
	if err != nil {
		return
	}

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || !strings.Contains(strings.ToLower(line), "_update.bat") {
			continue
		}

		fields := strings.Split(line, ",")
		pidText := strings.TrimSpace(fields[len(fields)-1])
		pid, errPID := strconv.Atoi(pidText)
		if errPID != nil || pid <= 0 {
			continue
		}

		cmd := exec.Command("taskkill", "/F", "/PID", strconv.Itoa(pid))
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if errKill := cmd.Run(); errKill == nil {
			logger.Warnf("[自动更新] 已结束卡死的旧更新脚本(PID %d)", pid)
		}
	}
}

// killByImageName 按进程名结束进程（不存在时忽略）
func killByImageName(name string) {
	cmd := exec.Command("taskkill", "/F", "/IM", name)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = cmd.Run()
}
