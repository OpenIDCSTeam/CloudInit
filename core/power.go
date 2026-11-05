package core

import (
	"CloudInit/logger"
	"CloudInit/platform"
)

// PowerCtrl 系统电源控制（关机/重启）
type PowerCtrl struct {
	platform platform.Platform
}

// NewPowerCtrl 创建电源控制器
func NewPowerCtrl(p platform.Platform) *PowerCtrl {
	return &PowerCtrl{platform: p}
}

// ShutdownSystem 执行系统关机命令
func (p *PowerCtrl) ShutdownSystem() {
	logger.Warnf("[电源控制] 执行关机操作")
	p.platform.Shutdown()
}

// RebootSystem 执行系统重启命令
func (p *PowerCtrl) RebootSystem() {
	logger.Warnf("[电源控制] 执行重启操作")
	p.platform.Reboot()
}
