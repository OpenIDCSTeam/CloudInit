package attack

import (
	"bytes"
	"golang.org/x/sys/windows/registry"
	"os/exec"
	"strings"
)

// IsRunning 是否运行
func IsRunning(processKeywords string) bool {
	cmd := exec.Command("tasklist")
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		return false
	}
	return strings.Contains(out.String(), processKeywords)
}

// IsInstalled 是否安装
func IsInstalled(appKeywords string) bool {
	_, err := registry.OpenKey(registry.LOCAL_MACHINE, appKeywords, registry.READ)
	if err != nil {
		return false
	}
	return true
}
