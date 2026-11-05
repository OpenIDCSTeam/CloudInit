//go:build darwin

package status

import (
	"os/exec"
	"regexp"
	"strconv"
)

var mediaSpeedRe = regexp.MustCompile(`\(([0-9]+)(G?)base`)

// nicLinkSpeed 获取网卡速率（Mbps），解析 ifconfig 的 media 行
func nicLinkSpeed(name string) int {
	out, err := exec.Command("ifconfig", name).Output()
	if err != nil {
		return 0
	}

	match := mediaSpeedRe.FindStringSubmatch(string(out))
	if match == nil {
		return 0
	}

	speed, err := strconv.Atoi(match[1])
	if err != nil {
		return 0
	}
	if match[2] == "G" {
		speed *= 1000
	}
	return speed
}
