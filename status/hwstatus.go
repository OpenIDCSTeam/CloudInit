package status

import "encoding/json"

// HWStatus 硬件状态数据模型
type HWStatus struct {
	// 基础数据
	AcStatus string                 `json:"ac_status"`
	CPUModel string                 `json:"cpu_model"`
	CPUTotal int                    `json:"cpu_total"`
	CPUUsage int                    `json:"cpu_usage"`
	MemTotal int                    `json:"mem_total"`
	MemUsage int                    `json:"mem_usage"`
	HDDTotal int                    `json:"hdd_total"`
	HDDUsage int                    `json:"hdd_usage"`
	ExtUsage map[string]interface{} `json:"ext_usage"`

	// 网络信息
	FluTotal int `json:"flu_total"`
	FluUsage int `json:"flu_usage"`
	// 以下字段宿主机上报接口不支持（会被判为非法字段），不参与上报序列化
	NatTotal int `json:"-"`
	NatUsage int `json:"-"`
	WebTotal int `json:"-"`
	WebUsage int `json:"-"`

	// 其他信息
	GpuUsage map[string]int `json:"gpu_usage"`
	GpuTotal int            `json:"gpu_total"`
	NetworkU int            `json:"network_u"`
	NetworkD int            `json:"network_d"`
	NetworkA int            `json:"network_a"`
	CPUHeats int            `json:"cpu_heats"`
	CPUPower int            `json:"cpu_power"`

	// 虚拟机信息（由宿主机下发，不上报）
	VMName string `json:"-"`
	VMPass string `json:"-"`
}

// NewHWStatus 创建带默认值的硬件状态
func NewHWStatus() *HWStatus {
	return &HWStatus{
		AcStatus: PowerUnknown.String(),
		ExtUsage: map[string]interface{}{},
		GpuUsage: map[string]int{},
	}
}

// ToDict 转换为字典（用于JSON序列化）
func (h *HWStatus) ToDict() map[string]interface{} {
	return map[string]interface{}{
		"ac_status": h.AcStatus,
		"cpu_model": h.CPUModel,
		"cpu_total": h.CPUTotal,
		"cpu_usage": h.CPUUsage,
		"mem_total": h.MemTotal,
		"mem_usage": h.MemUsage,
		"hdd_total": h.HDDTotal,
		"hdd_usage": h.HDDUsage,
		"ext_usage": h.ExtUsage,
		"flu_total": h.FluTotal,
		"flu_usage": h.FluUsage,
		"nat_total": h.NatTotal,
		"nat_usage": h.NatUsage,
		"web_total": h.WebTotal,
		"web_usage": h.WebUsage,
		"gpu_usage": h.GpuUsage,
		"gpu_total": h.GpuTotal,
		"network_u": h.NetworkU,
		"network_d": h.NetworkD,
		"network_a": h.NetworkA,
		"cpu_heats": h.CPUHeats,
		"cpu_power": h.CPUPower,
		"vm_name":   h.VMName,
		"vm_pass":   h.VMPass,
	}
}

// String 返回 JSON 字符串
func (h *HWStatus) String() string {
	data, err := json.Marshal(h.ToDict())
	if err != nil {
		return "{}"
	}
	return string(data)
}
