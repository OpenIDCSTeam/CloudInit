//go:build windows

package rdpdump

import (
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"golang.org/x/sys/windows/registry"
	"golang.org/x/text/encoding/simplifiedchinese"
)

// firstNonEmpty 返回第一个非空字符串
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// dedupePaths 路径去重
func dedupePaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		cleaned := filepath.Clean(path)
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		result = append(result, cleaned)
	}
	return result
}

// readRegistryStrings 读取注册表键下的所有字符串值（键名转小写）
func decodeLocalText(data []byte) string {
	if utf8.Valid(data) {
		return string(data)
	}
	if decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes(data); err == nil {
		return string(decoded)
	}
	return string(data)
}

func readRegistryStrings(root registry.Key, path string) map[string]string {
	values := make(map[string]string)

	key, err := registry.OpenKey(root, path, registry.QUERY_VALUE)
	if err != nil {
		return values
	}
	defer key.Close()

	names, err := key.ReadValueNames(-1)
	if err != nil {
		return values
	}

	for _, name := range names {
		value, _, errValue := key.GetStringValue(name)
		if errValue != nil {
			continue
		}
		values[strings.ToLower(name)] = value
	}

	return values
}

// programDirs 返回 Program Files / Program Files (x86)
func programDirs() []string {
	dirs := make([]string, 0, 2)
	for _, name := range []string{"ProgramFiles", "ProgramFiles(x86)"} {
		if dir := os.Getenv(name); dir != "" {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

// extractAlnum 提取字符串中的字母数字片段
func extractAlnum(value string, minLen int) string {
	builder := make([]byte, 0, len(value))
	best := ""
	for _, ch := range []byte(value) {
		isAlnum := (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
		if isAlnum {
			builder = append(builder, ch)
			continue
		}
		if len(builder) >= minLen {
			best = string(builder)
			break
		}
		builder = builder[:0]
	}
	if best == "" && len(builder) >= minLen {
		best = string(builder)
	}
	return best
}
