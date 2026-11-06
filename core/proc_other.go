//go:build !windows

package core

import "os/exec"

// startDetachedBatch 非 Windows 平台的占位实现（重启流程不会走到这里）
func startDetachedBatch(batchPath string) error {
	cmd := exec.Command(batchPath)
	return cmd.Start()
}

// cleanupStaleUpdateArtifacts 非 Windows 平台无需清理
func cleanupStaleUpdateArtifacts() {}
