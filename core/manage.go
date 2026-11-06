// Package core 核心管理模块：虚拟机配置、磁盘扩容、电源控制、命令执行、自动更新
package core

import (
	"strings"
	"sync"
	"sync/atomic"

	"CloudInit/config"
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
//
// 两个幂等约束：
//  1. 主机名：每次开机只尝试修改一次（改完需重启才生效，重复改没有意义）；
//  2. 密码：只有上游下发的密码与上次设置的**加盐哈希**不一致时才修改。
func (v *VMManage) Manage() {
	v.mu.Lock()
	vmUUID := v.vmConfig["vm_uuid"]
	vmPass := v.vmConfig["vm_pass"]
	v.mu.Unlock()

	if vmUUID == "" {
		return
	}

	state := loadState()
	bootID := currentBootID()

	// 开机后首次上报：允许修改一次主机名
	needHostname := false
	if state.BootID != bootID {
		state.BootID = bootID
		current := strings.TrimSpace(v.platform.GetHostname())
		needHostname = !strings.EqualFold(current, vmUUID)
		state.Hostname = vmUUID
		saveState(state)

		if !needHostname {
			logger.Debugf("[VM配置] 主机名已一致(%s)，本次开机不再修改", vmUUID)
		}
	}

	needPassword := vmPass != "" && config.Cfg.PasswordChangeEnabled && !state.passwordMatches(vmPass)

	if !needHostname && !needPassword {
		return
	}

	// 避免重复启动配置任务
	if !atomic.CompareAndSwapInt32(&v.busy, 0, 1) {
		logger.Debugf("[VM配置] 上一次配置尚未完成，跳过")
		return
	}

	go v.doManage(vmUUID, vmPass, needHostname, needPassword)
}

// doManage 实际执行配置（后台协程）
func (v *VMManage) doManage(vmUUID, vmPass string, needHostname, needPassword bool) {
	defer atomic.StoreInt32(&v.busy, 0)

	if needHostname {
		func() {
			defer func() {
				if r := recover(); r != nil {
					logger.Errorf("[VM配置] 设置主机名失败: %v", r)
				}
			}()
			logger.Infof("[VM配置] 修改主机名为 %s（重启后生效，本次开机不再重复修改）", vmUUID)
			v.platform.SetHostname(vmUUID)
		}()
	}

	if !needPassword {
		return
	}

	if !config.Cfg.PasswordChangeEnabled {
		logger.Infof("[VM配置] 已在配置中禁用密码修改")
		return
	}

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

	// 记录已设置的密码哈希（加盐），后续相同密码不再重复修改
	state := loadState()
	state.PasswordHash = passwordHash(state.Salt, vmPass)
	saveState(state)
	logger.Infof("[VM配置] 已更新系统密码（加密指纹已记录）")
}
