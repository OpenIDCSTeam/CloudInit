//go:build windows

package remotedesk

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows/registry"

	"CloudInit/logger"
)

// toDeskUpdateProcesses 需要结束的 ToDesk 更新相关进程
var toDeskUpdateProcesses = []string{
	"ToDeskUpd.exe",
	"ToDeskUpdate.exe",
	"ToDeskUpdater.exe",
	"ToDeskSetup.exe",
	"ToDesk_Setup.exe",
}

// disableToDeskAutoUpdate 禁止 ToDesk 自动更新
//
// 1. 把 config.ini 中 [ConfigInfo] 段的 isUpdate 置为 0（ToDesk 自动更新开关）
// 2. 结束可能存在的更新进程
func (m *Manager) disableToDeskAutoUpdate() {
	for _, path := range toDeskConfigPaths(m.toolDir) {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		patchToDeskConfig(path)
	}

	// 更新进程清理限频，避免频繁创建 taskkill 进程
	m.mu.Lock()
	shouldKill := time.Since(m.lastUpdaterKill) >= 10*time.Minute
	if shouldKill {
		m.lastUpdaterKill = time.Now()
	}
	shouldScanTasks := time.Since(m.lastTaskScan) >= time.Hour
	if shouldScanTasks {
		m.lastTaskScan = time.Now()
	}
	m.mu.Unlock()

	if shouldKill {
		killToDeskUpdaters()
	}
	if shouldScanTasks {
		disableToDeskUpdateTasks()
	}
}

// disableToDeskUpdateTasks 禁用与 ToDesk 更新相关的计划任务
func disableToDeskUpdateTasks() {
	list := exec.Command("schtasks", "/query", "/fo", "csv", "/nh")
	list.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	out, err := list.Output()
	if err != nil {
		return
	}

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if !strings.HasPrefix(line, `"`) {
			continue
		}

		end := strings.Index(line[1:], `"`)
		if end <= 0 {
			continue
		}
		name := line[1 : 1+end]
		lower := strings.ToLower(name)

		if !strings.Contains(lower, "todesk") {
			continue
		}
		if !strings.Contains(lower, "upd") && !strings.Contains(lower, "update") {
			continue
		}

		change := exec.Command("schtasks", "/change", "/tn", name, "/disable")
		change.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if err := change.Run(); err == nil {
			logger.Infof("[ToDesk] 已禁用更新计划任务: %s", name)
		}
	}
}

// toDeskConfigPaths 返回可能的 ToDesk 配置文件路径（去重）
func toDeskConfigPaths(toolDir string) []string {
	candidates := make([]string, 0, 4)

	if dir := toDeskInstallDir(); dir != "" {
		candidates = append(candidates, filepath.Join(dir, "config.ini"))
	}
	if toolDir != "" {
		candidates = append(candidates, filepath.Join(toolDir, "config.ini"))
	}
	if programFiles := os.Getenv("ProgramFiles"); programFiles != "" {
		candidates = append(candidates, filepath.Join(programFiles, "ToDesk", "config.ini"))
	}
	if programFilesX86 := os.Getenv("ProgramFiles(x86)"); programFilesX86 != "" {
		candidates = append(candidates, filepath.Join(programFilesX86, "ToDesk", "config.ini"))
	}

	seen := make(map[string]struct{}, len(candidates))
	paths := make([]string, 0, len(candidates))
	for _, path := range candidates {
		cleaned := filepath.Clean(path)
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		paths = append(paths, cleaned)
	}

	return paths
}

var imagePathRe = regexp.MustCompile(`"([^"]+)"`)

// toDeskInstallDir 从 ToDesk_Service 注册表读取安装目录
func toDeskInstallDir() string {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SYSTEM\CurrentControlSet\Services\ToDesk_Service`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer key.Close()

	if dir, _, errDir := key.GetStringValue("Dir"); errDir == nil && dir != "" {
		return dir
	}

	imagePath, _, errImage := key.GetStringValue("ImagePath")
	if errImage != nil || imagePath == "" {
		return ""
	}

	if match := imagePathRe.FindStringSubmatch(imagePath); match != nil {
		return filepath.Dir(match[1])
	}
	if fields := strings.Fields(imagePath); len(fields) > 0 {
		return filepath.Dir(fields[0])
	}

	return ""
}

// patchToDeskConfig 把 [ConfigInfo] 段的 isUpdate 置为 0（保持原有行尾与其它内容）
func patchToDeskConfig(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	content := string(data)
	crlf := strings.Contains(content, "\r\n")
	suffix := ""
	insertLine := "isUpdate=0"
	if crlf {
		suffix = "\r"
		insertLine = "isUpdate=0\r"
	}

	lines := strings.Split(content, "\n")
	section := ""
	sectionIndex := -1
	updated := false

	for index, raw := range lines {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))

		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.Trim(line, "[]"))
			if section == "configinfo" && sectionIndex < 0 {
				sectionIndex = index
			}
			continue
		}

		if section != "configinfo" {
			continue
		}

		keyText, valueText, found := strings.Cut(line, "=")
		if !found || !strings.EqualFold(strings.TrimSpace(keyText), "isUpdate") {
			continue
		}

		if strings.TrimSpace(valueText) == "0" {
			return // 已是关闭状态，无需改写
		}

		indent := raw[:len(raw)-len(strings.TrimLeft(raw, " \t"))]
		lines[index] = indent + strings.TrimSpace(keyText) + "=0" + suffix
		updated = true
		break
	}

	// 配置里没有 isUpdate 时，在 [ConfigInfo] 段首插入
	if !updated {
		if sectionIndex < 0 {
			return
		}
		tail := append([]string{}, lines[sectionIndex+1:]...)
		lines = append(lines[:sectionIndex+1], append([]string{insertLine}, tail...)...)
	}

	if err = os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		logger.Debugf("[ToDesk] 写入配置文件失败 %s: %v", path, err)
		return
	}

	logger.Infof("[ToDesk] 已关闭自动更新: %s", path)
}

// killToDeskUpdaters 结束 ToDesk 更新进程（不存在时忽略）
func killToDeskUpdaters() {
	for _, name := range toDeskUpdateProcesses {
		cmd := exec.Command("taskkill", "/F", "/IM", name)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

		if err := cmd.Run(); err == nil {
			logger.Infof("[ToDesk] 已结束更新进程: %s", name)
		}
	}
}
