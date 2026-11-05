//go:build !windows

package platform

// WindowsVersion 非 Windows 平台返回空字符串
func WindowsVersion() string { return "" }

// IsLegacyWindows 非 Windows 平台恒为 false
func IsLegacyWindows() bool { return false }
