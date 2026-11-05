//go:build windows

package nic

import (
	"errors"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	afUnspec               = 0
	gaaFlagIncludeGateways = 0x0080
)

// discoverGateways 通过 GetAdaptersAddresses 获取各网卡的默认网关
func discoverGateways() gatewayInfo {
	info := newGatewayInfo()

	first, buf, err := adapterAddresses()
	if err != nil {
		return info
	}

	for adapter := first; adapter != nil; adapter = adapter.Next {
		name := windows.UTF16PtrToString(adapter.FriendlyName)
		if name == "" {
			continue
		}

		for gateway := adapter.FirstGatewayAddress; gateway != nil; gateway = gateway.Next {
			ip := gateway.Address.IP()
			if ip == nil {
				continue
			}

			if v4 := ip.To4(); v4 != nil {
				if info.V4[name] == "" {
					info.V4[name] = v4.String()
				}
				if info.DefaultV4 == "" {
					info.DefaultV4 = v4.String()
				}
			} else {
				if info.V6[name] == "" {
					info.V6[name] = ip.String()
				}
				if info.DefaultV6 == "" {
					info.DefaultV6 = ip.String()
				}
			}
		}
	}

	runtime.KeepAlive(buf)
	return info
}

// adapterAddresses 分配缓冲区并调用 GetAdaptersAddresses
func adapterAddresses() (*windows.IpAdapterAddresses, []byte, error) {
	size := uint32(64 * 1024)

	for attempt := 0; attempt < 4; attempt++ {
		buf := make([]byte, size)
		first := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0]))

		err := windows.GetAdaptersAddresses(afUnspec, gaaFlagIncludeGateways, 0, first, &size)
		if err == nil {
			return first, buf, nil
		}
		if err == windows.ERROR_BUFFER_OVERFLOW {
			continue
		}
		return nil, nil, err
	}

	return nil, nil, errors.New("GetAdaptersAddresses: 缓冲区不足")
}
