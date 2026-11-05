//go:build windows

package nic

import (
	"os/exec"
	"strconv"
	"strings"
)

// discoverGateways 解析 route print 输出获取网关
//
// 不使用 GetAdaptersAddresses：该接口的结构体布局随 Windows 版本变化
// （Vista/7/8/10 长度不同），在 Win7/8 上容易取不到 FirstGatewayAddress。
// route print 的输出行为各版本一致，且只解析数字行，不受系统语言影响。
func discoverGateways() gatewayInfo {
	info := newGatewayInfo()

	out, err := exec.Command("route", "print", "-4").Output()
	if err != nil {
		// 极老版本系统不支持 -4 参数时退回完整路由表
		out, err = exec.Command("route", "print").Output()
		if err != nil {
			return info
		}
	}

	bestDefaultMetric := -1

	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}

		// 只处理数字行：目标地址、掩码、网关、接口地址
		if !isIPv4(fields[0]) || !isIPv4(fields[1]) || !isIPv4(fields[3]) {
			continue
		}
		gateway := fields[2]
		if !isIPv4(gateway) {
			continue // On-link（在链路上）等非网关行
		}

		interfaceIP := fields[3]

		if fields[0] == "0.0.0.0" && fields[1] == "0.0.0.0" {
			info.V4[interfaceIP] = gateway

			// 默认网关取 metric 最小的一条
			metric := 0
			if parsed, errMetric := strconv.Atoi(fields[4]); errMetric == nil {
				metric = parsed
			}
			if info.DefaultV4 == "" || bestDefaultMetric < 0 || metric < bestDefaultMetric {
				info.DefaultV4 = gateway
				bestDefaultMetric = metric
			}
			continue
		}

		if _, exists := info.V4[interfaceIP]; !exists {
			info.V4[interfaceIP] = gateway
		}
	}

	return info
}

// isIPv4 判断是否为点分十进制 IPv4 字符串
func isIPv4(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 4 {
		return false
	}
	for _, part := range parts {
		if part == "" || len(part) > 3 {
			return false
		}
		for _, ch := range part {
			if ch < '0' || ch > '9' {
				return false
			}
		}
	}
	return true
}
