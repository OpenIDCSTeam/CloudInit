// Package nic 网卡管理模块
package nic

import (
	"fmt"
	"strconv"
	"strings"

	"CloudInit/logger"
)

// NCConfig 单个网卡的配置信息
type NCConfig struct {
	MacAddr string `json:"mac_addr"`
	NicType string `json:"nic_type"`
	Ip4Addr string `json:"ip4_addr"`
	Ip6Addr string `json:"ip6_addr"`
	Ip4Gate string `json:"ip4_gate"`
	Ip6Gate string `json:"ip6_gate"`
}

// NewNCConfig 创建网卡配置，未提供MAC时按IPv4地址生成虚拟MAC
func NewNCConfig(macAddr, nicType, ip4Addr, ip6Addr, ip4Gate, ip6Gate string) *NCConfig {
	c := &NCConfig{
		MacAddr: macAddr,
		NicType: nicType,
		Ip4Addr: ip4Addr,
		Ip6Addr: ip6Addr,
		Ip4Gate: ip4Gate,
		Ip6Gate: ip6Gate,
	}
	if c.MacAddr == "" {
		c.MacAddr = c.generateMAC()
	}
	return c
}

// ToDict 转换为字典
func (c *NCConfig) ToDict() map[string]string {
	return map[string]string{
		"mac_addr": c.MacAddr,
		"nic_type": c.NicType,
		"ip4_addr": c.Ip4Addr,
		"ip6_addr": c.Ip6Addr,
		"ip4_gate": c.Ip4Gate,
		"ip6_gate": c.Ip6Gate,
	}
}

// generateMAC 根据IPv4地址生成虚拟MAC地址
func (c *NCConfig) generateMAC() string {
	if c.Ip4Addr == "" {
		return "00:00:00:00:00:00"
	}

	ip4Parts := strings.Split(c.Ip4Addr, ".")
	suffixParts := make([]string, 0, len(ip4Parts))
	for _, part := range ip4Parts {
		value, err := strconv.Atoi(part)
		if err != nil {
			logger.Warnf("[NCConfig] MAC地址生成失败: %v", err)
			return "00:00:00:00:00:00"
		}
		suffixParts = append(suffixParts, fmt.Sprintf("%02x", value))
	}
	macSuffix := strings.Join(suffixParts, ":")

	switch {
	case strings.HasPrefix(c.Ip4Addr, "192"):
		return "00:1C:" + macSuffix
	case strings.HasPrefix(c.Ip4Addr, "172"):
		return "CC:D9:" + macSuffix
	case strings.HasPrefix(c.Ip4Addr, "100"):
		return "00:1E:" + macSuffix
	case strings.HasPrefix(c.Ip4Addr, "10"):
		return "10:F6:" + macSuffix
	default:
		return "00:00:" + macSuffix
	}
}
