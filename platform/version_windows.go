//go:build windows

package platform

import (
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// WindowsVersion 返回 Windows 版本号字符串（如 "10.0.19045"）
func WindowsVersion() string {
	const path = `SOFTWARE\Microsoft\Windows NT\CurrentVersion`

	key, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer key.Close()

	major, minor := uint64(0), uint64(0)

	if majorVal, _, errMajor := key.GetIntegerValue("CurrentMajorVersionNumber"); errMajor == nil {
		major = majorVal
		if minorVal, _, errMinor := key.GetIntegerValue("CurrentMinorVersionNumber"); errMinor == nil {
			minor = minorVal
		}
	} else if verStr, _, errVer := key.GetStringValue("CurrentVersion"); errVer == nil {
		parts := strings.Split(verStr, ".")
		if len(parts) > 0 {
			major, _ = strconv.ParseUint(parts[0], 10, 64)
		}
		if len(parts) > 1 {
			minor, _ = strconv.ParseUint(parts[1], 10, 64)
		}
	}

	build := uint64(0)
	if buildStr, _, errBuild := key.GetStringValue("CurrentBuildNumber"); errBuild == nil {
		build, _ = strconv.ParseUint(buildStr, 10, 64)
	}

	return fmt.Sprintf("%d.%d.%d", major, minor, build)
}

// IsLegacyWindows 判断当前 Windows 是否为旧版本（Win7/Win8/Win8.1）
func IsLegacyWindows() bool {
	ver := WindowsVersion()
	parts := strings.Split(ver, ".")
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return false
	}
	return major < 10
}
