//go:build darwin

package nic

import (
	"os/exec"
	"strings"
)

// discoverGateways 解析 netstat 路由表获取默认网关
func discoverGateways() gatewayInfo {
	info := newGatewayInfo()

	parse := func(family string, v4 bool) {
		out, err := exec.Command("netstat", "-rn", "-f", family).Output()
		if err != nil {
			return
		}

		for _, line := range strings.Split(string(out), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 4 || fields[0] != "default" {
				continue
			}

			gateway := fields[1]
			iface := fields[len(fields)-1]

			if v4 {
				if info.V4[iface] == "" {
					info.V4[iface] = gateway
				}
				if info.DefaultV4 == "" {
					info.DefaultV4 = gateway
				}
			} else {
				if info.V6[iface] == "" {
					info.V6[iface] = gateway
				}
				if info.DefaultV6 == "" {
					info.DefaultV6 = gateway
				}
			}
		}
	}

	parse("inet", true)
	parse("inet6", false)

	return info
}
