//go:build linux

package status

import (
	"os"
	"strconv"
	"strings"
)

// nicLinkSpeed 获取网卡速率（Mbps），读取 /sys 下的 speed 属性
func nicLinkSpeed(name string) int {
	data, err := os.ReadFile("/sys/class/net/" + name + "/speed")
	if err != nil {
		return 0
	}
	speed, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || speed < 0 {
		return 0
	}
	return speed
}
