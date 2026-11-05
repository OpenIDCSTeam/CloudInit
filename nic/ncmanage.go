package nic

import (
	"fmt"
	"net"
	"strings"

	gnet "github.com/shirou/gopsutil/v3/net"

	"CloudInit/logger"
)

const zeroMAC = "00:00:00:00:00:00"

// NCManage 网络接口管理器
type NCManage struct {
	NICList []*NCConfig
	byKey   map[string]*NCConfig
}

// New 创建网卡管理器并扫描接口
func New() (*NCManage, error) {
	m := &NCManage{byKey: map[string]*NCConfig{}}

	if err := m.getNIC(); err != nil {
		return m, err
	}
	return m, nil
}

// getNIC 扫描并获取所有有效网络接口
func (m *NCManage) getNIC() error {
	m.NICList = nil
	m.byKey = map[string]*NCConfig{}

	gw := discoverGateways()

	interfaces, err := gnet.Interfaces()
	if err != nil {
		return err
	}

	processed := map[string]bool{}

	for _, iface := range interfaces {
		nicKey := strings.ToLower(iface.Name)
		if processed[nicKey] {
			continue
		}
		processed[nicKey] = true

		// 获取MAC地址（无链路层地址的接口直接跳过，等价于 netifaces 的 KeyError）
		mac := iface.HardwareAddr
		if mac == "" || mac == zeroMAC {
			continue
		}

		// 获取IPv4/IPv6地址信息
		ip4Addr, ip6Addr := firstAddresses(iface.Addrs)

		// 获取IPv4网关（Linux/macOS 以接口名为键，Windows 以接口地址为键）
		ip4Gate := gw.V4[iface.Name]
		if ip4Gate == "" && ip4Addr != "" {
			ip4Gate = gw.V4[ip4Addr]
		}
		if ip4Gate == "" && gw.DefaultV4 != "" {
			ip4Gate = gw.DefaultV4
		}

		// 获取IPv6网关
		ip6Gate := gw.V6[iface.Name]
		if ip6Gate == "" && gw.DefaultV6 != "" {
			ip6Gate = gw.DefaultV6
		}

		// 创建NCConfig对象
		nicConfig := NewNCConfig(mac, iface.Name, ip4Addr, ip6Addr, ip4Gate, ip6Gate)

		m.NICList = append(m.NICList, nicConfig)
		m.byKey[nicKey] = nicConfig
		logger.Debugf("[网卡] %s MAC=%s IPv4=%s GW=%s", iface.Name, mac, ip4Addr, ip4Gate)
	}

	logger.Infof("[网卡] 扫描完成，发现 %d 个有效接口", len(m.NICList))
	return nil
}

// firstAddresses 提取接口的第一个 IPv4 与 IPv6 地址（去除掩码与作用域）
func firstAddresses(addrs []gnet.InterfaceAddr) (string, string) {
	var ip4, ip6 string

	for _, addr := range addrs {
		value := addr.Addr
		if idx := strings.IndexByte(value, '%'); idx >= 0 {
			value = value[:idx]
		}
		if idx := strings.IndexByte(value, '/'); idx >= 0 {
			value = value[:idx]
		}

		ip := net.ParseIP(value)
		if ip == nil {
			continue
		}

		if ip.To4() != nil {
			if ip4 == "" {
				ip4 = value
			}
		} else if ip6 == "" {
			ip6 = value
		}
	}

	return ip4, ip6
}

// GetNICInfo 返回格式化的网卡信息字典
func (m *NCManage) GetNICInfo() map[string]map[string]string {
	result := make(map[string]map[string]string, len(m.byKey))
	for name, config := range m.byKey {
		result[name] = config.ToDict()
	}
	return result
}

// FindByMAC 按MAC查找网卡配置
func (m *NCManage) FindByMAC(mac string) *NCConfig {
	for _, config := range m.NICList {
		if strings.EqualFold(config.MacAddr, mac) {
			return config
		}
	}
	return nil
}

// String 便于调试输出
func (m *NCManage) String() string {
	return fmt.Sprintf("NCManage(%d interfaces)", len(m.NICList))
}
