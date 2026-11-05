package core

import (
	"CloudInit/logger"
	"CloudInit/platform"
)

// DiskExtend 磁盘智能扩容 - 非阻塞执行
type DiskExtend struct {
	platform platform.Platform
}

// NewDiskExtend 创建磁盘扩容器
func NewDiskExtend(p platform.Platform) *DiskExtend {
	return &DiskExtend{platform: p}
}

// Extend 非阻塞执行磁盘扩容
func (d *DiskExtend) Extend() {
	logger.Infof("[磁盘扩容] 启动后台扩容任务")
	go func() {
		if err := d.platform.ExtendDisk(); err != nil {
			logger.Errorf("[磁盘扩容] 扩容失败: %v", err)
			return
		}
		logger.Infof("[磁盘扩容] 扩容任务完成")
	}()
}
