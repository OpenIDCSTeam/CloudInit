//go:build linux

package nic

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

const rtfGateway = 0x0002

// discoverGateways 读取 /proc 路由表获取网关信息
func discoverGateways() gatewayInfo {
	info := newGatewayInfo()

	// IPv4: /proc/net/route
	if data, err := os.ReadFile("/proc/net/route"); err == nil {
		lines := strings.Split(string(data), "\n")
		for index, line := range lines {
			if index == 0 {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 4 {
				continue
			}

			iface := fields[0]
			destination := fields[1]
			gatewayHex := fields[2]
			if gatewayHex == "00000000" {
				continue
			}

			gateway := hexToIPv4(gatewayHex)
			if gateway == "" {
				continue
			}

			if _, exists := info.V4[iface]; !exists {
				info.V4[iface] = gateway
			}

			flags, errFlags := strconv.ParseUint(fields[3], 16, 32)
			isDefault := destination == "00000000" && errFlags == nil && flags&rtfGateway != 0
			if isDefault && info.DefaultV4 == "" {
				info.DefaultV4 = gateway
			}
		}
	}

	// IPv6: /proc/net/ipv6_route
	if data, err := os.ReadFile("/proc/net/ipv6_route"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 10 {
				continue
			}

			destination := fields[0]
			prefixLen := fields[1]
			nextHop := fields[4]
			iface := fields[9]

			if nextHop == strings.Repeat("0", 32) {
				continue
			}

			ip := parseIPv6Hex(nextHop)
			if ip == nil {
				continue
			}

			if _, exists := info.V6[iface]; !exists {
				info.V6[iface] = ip.String()
			}

			if destination == strings.Repeat("0", 32) && prefixLen == "00" && info.DefaultV6 == "" {
				info.DefaultV6 = ip.String()
			}
		}
	}

	return info
}

// hexToIPv4 将 /proc/net/route 中的小端十六进制网关转换为点分十进制
func hexToIPv4(hex string) string {
	if len(hex) != 8 {
		return ""
	}
	value, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d.%d.%d.%d", byte(value), byte(value>>8), byte(value>>16), byte(value>>24))
}

// parseIPv6Hex 解析 /proc/net/ipv6_route 中的 IPv6 地址（4 组小端 32 位字）
func parseIPv6Hex(hex string) net.IP {
	if len(hex) != 32 {
		return nil
	}

	ip := make(net.IP, net.IPv6len)
	for i := 0; i < 4; i++ {
		value, err := strconv.ParseUint(hex[i*8:(i+1)*8], 16, 32)
		if err != nil {
			return nil
		}
		binary.LittleEndian.PutUint32(ip[i*4:], uint32(value))
	}
	return ip
}
