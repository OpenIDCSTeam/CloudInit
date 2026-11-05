//go:build windows

package status

import (
	"strings"

	gnet "github.com/shirou/gopsutil/v3/net"
	"golang.org/x/sys/windows"
)

// nicLinkSpeed 获取网卡速率（Mbps），等价于 psutil.net_if_stats().speed
func nicLinkSpeed(name string) int {
	interfaces, err := gnet.Interfaces()
	if err != nil {
		return 0
	}

	for _, iface := range interfaces {
		if !strings.EqualFold(iface.Name, name) {
			continue
		}
		row := windows.MibIfRow{Index: uint32(iface.Index)}
		if err = windows.GetIfEntry(&row); err != nil {
			return 0
		}
		// 0xFFFFFFFF 表示驱动未上报速率
		const unknownSpeed = ^uint32(0)
		if row.Speed == unknownSpeed {
			return 0
		}
		return int(row.Speed / 1000000)
	}

	return 0
}
