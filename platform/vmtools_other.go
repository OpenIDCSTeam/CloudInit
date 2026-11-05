//go:build !windows

package platform

// 非 Windows 平台的占位实现（相关流程只会在 Windows 上执行）

func findVMwareToolsProductCode() string { return "" }

func vmwareToolsInstalled() bool { return false }

func stopVMwareServices() {}

func uninstallVMwareTools(productCode string) (bool, int) { return false, 0 }

func removeVMwareServices() {}
