//go:build !windows

package rdpdump

// 非 Windows 平台没有这些远程桌面客户端，返回空信息

func ToDesk(string) Info { return Info{} }

func SunLogin(string) Info { return Info{} }

func AnyDesk(string) Info { return Info{} }

func ProcessRunning(names ...string) bool { return false }
