package nic

// gatewayInfo 网关信息（对应 netifaces.gateways() 的返回结构）
type gatewayInfo struct {
	DefaultV4 string
	DefaultV6 string
	V4        map[string]string // 接口名 -> IPv4 网关
	V6        map[string]string // 接口名 -> IPv6 网关
}

func newGatewayInfo() gatewayInfo {
	return gatewayInfo{
		V4: map[string]string{},
		V6: map[string]string{},
	}
}
