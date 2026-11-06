//go:build !windows

package remotedesk

// hideWindowsOfProcess 非 Windows 平台无需处理窗口
func hideWindowsOfProcess(exeName string, waitSeconds int) {}
