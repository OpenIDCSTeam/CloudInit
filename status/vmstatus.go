package status

import (
	"encoding/json"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
	gnet "github.com/shirou/gopsutil/v3/net"

	"CloudInit/logger"
)

// VMStatus 虚拟机硬件状态采集
type VMStatus struct {
	VM *HWStatus
}

// NewVMStatus 创建状态采集器
func NewVMStatus() *VMStatus {
	return &VMStatus{VM: NewHWStatus()}
}

// ToDict 转换为字典
func (s *VMStatus) ToDict() *HWStatus {
	return s.VM
}

// String 返回 JSON 字符串
func (s *VMStatus) String() string {
	data, err := json.Marshal(s.ToDict())
	if err != nil {
		return "{}"
	}
	return string(data)
}

// Status 采集当前硬件状态
func (s *VMStatus) Status() *HWStatus {
	s.VM.AcStatus = PowerStarted.String()
	s.collectCPU()
	s.collectMemory()
	s.collectDisk()
	s.collectGPU()
	s.collectNetwork()
	return s.VM
}

// collectCPU 采集CPU信息
func (s *VMStatus) collectCPU() {
	total, err := cpu.Counts(true)
	if err != nil {
		logger.Errorf("[状态采集] CPU信息获取失败: %v", err)
		return
	}
	s.VM.CPUTotal = total

	percent, err := cpu.Percent(time.Second, false)
	if err != nil {
		logger.Errorf("[状态采集] CPU信息获取失败: %v", err)
		return
	}
	if len(percent) > 0 {
		s.VM.CPUUsage = int(percent[0])
	}
}

// collectMemory 采集内存信息
func (s *VMStatus) collectMemory() {
	vm, err := mem.VirtualMemory()
	if err != nil {
		logger.Errorf("[状态采集] 内存信息获取失败: %v", err)
		return
	}
	s.VM.MemTotal = int(vm.Total / (1024 * 1024))
	s.VM.MemUsage = int(vm.Used / (1024 * 1024))
}

// collectDisk 采集磁盘信息
func (s *VMStatus) collectDisk() {
	diskPath := "/"
	if runtime.GOOS == "windows" {
		// 使用系统盘环境变量，兼容性最佳
		drive := os.Getenv("SystemDrive")
		if drive == "" {
			drive = "C:"
		}
		diskPath = drive + string(os.PathSeparator)
	}

	usage, err := disk.Usage(diskPath)
	if err != nil {
		logger.Errorf("[状态采集] 磁盘信息获取失败: %v", err)
		return
	}
	s.VM.HDDTotal = int(usage.Total / (1024 * 1024))
	s.VM.HDDUsage = int(usage.Used / (1024 * 1024))
}

// collectGPU 采集GPU信息
func (s *VMStatus) collectGPU() {
	gpus, err := readGPUs()
	if err != nil {
		logger.Debugf("[状态采集] GPU信息获取失败（可能无GPU）: %v", err)
		return
	}

	s.VM.GpuTotal = len(gpus)
	for _, gpu := range gpus {
		s.VM.GpuUsage[strconv.Itoa(gpu.Index)] = gpu.Load
	}
}

// collectNetwork 采集网络带宽信息
func (s *VMStatus) collectNetwork() {
	counters, err := gnet.IOCounters(true)
	if err != nil {
		logger.Errorf("[状态采集] 网络信息获取失败: %v", err)
		return
	}

	maxName := ""
	var totalTx, totalRx float64

	for _, nic := range counters {
		txMB := float64(nic.BytesSent) / (1024 * 1024)
		if txMB > totalTx {
			totalTx = txMB
			totalRx = float64(nic.BytesRecv) / (1024 * 1024)
			maxName = nic.Name
		}
	}

	s.VM.FluUsage = int(totalTx + totalRx)
	s.VM.NetworkU = int(totalTx / 60 * 8)
	s.VM.NetworkD = int(totalRx / 60 * 8)

	logger.Debugf("[状态采集] 网络流量: %dMB | 上行: %dMbps | 下行: %dMbps",
		s.VM.FluUsage, s.VM.NetworkU, s.VM.NetworkD)

	// 获取物理网卡速率
	if maxName != "" {
		s.VM.NetworkA = nicLinkSpeed(maxName)
	}
}
