// Package core 核心管理模块：虚拟机配置、磁盘扩容、电源控制、命令执行、自动更新
package core

import (
	"sync"
	"sync/atomic"

	"CloudInit/logger"
	"CloudInit/platform"
)

// VMManage 虚拟机配置管理器
//
// 通过后台协程非阻塞执行配置任务，确保不影响主循环上报。
type VMManage struct {
	platform platform.Platform
	busy     int32 // 防止并发配置冲突
	mu       sync.Mutex
	vmConfig map[string]string
}

// NewVMManage 创建虚拟机配置管理器
func NewVMManage(p platform.Platform) *VMManage {
	return &VMManage{
		platform: p,
		vmConfig: map[string]string{
			"hs_name": "",
			"vm_uuid": "",
			"vm_pass": "",
		},
	}
}

// SetConfig 更新配置项
func (v *VMManage) SetConfig(key, value string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.vmConfig[key] = value
}

// GetConfig 读取配置项
func (v *VMManage) GetConfig(key string) string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.vmConfig[key]
}

// Manage 触发虚拟机配置（非阻塞）
func (v *VMManage) Manage() {
	v.mu.Lock()
	vmUUID := v.vmConfig["vm_uuid"]
	vmPass := v.vmConfig["vm_pass"]
	v.mu.Unlock()

	if vmUUID == "" || vmPass == "" {
		return
	}

	// 避免重复启动配置任务
	if !atomic.CompareAndSwapInt32(&v.busy, 0, 1) {
		logger.Debugf("[VM配置] 上一次配置尚未完成，跳过")
		return
	}

	go v.doManage(vmUUID, vmPass)
}

// doManage 实际执行配置（后台协程）
func (v *VMManage) doManage(vmUUID, vmPass string) {
	defer atomic.StoreInt32(&v.busy, 0)

	// 设置主机名（包含hosts更新）
	func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf("[VM配置] 设置主机名失败: %v", r)
			}
		}()
		v.platform.SetHostname(vmUUID)
	}()

	// 设置root密码
	func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf("[VM配置] 设置root密码失败: %v", r)
			}
		}()
		v.platform.SetPassword("root", vmPass)
	}()

	// 设置user密码
	func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf("[VM配置] 设置user密码失败: %v", r)
			}
		}()
		v.platform.SetPassword("user", vmPass)
	}()
}
