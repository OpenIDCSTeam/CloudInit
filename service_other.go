//go:build !windows

package main

// runAsService 非 Windows 平台由 systemd/launchd 托管，前台运行即可
func runAsService(app *CloudInitService) bool {
	return false
}
