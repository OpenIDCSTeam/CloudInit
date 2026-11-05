package status

import "errors"

// VMPower 虚拟机电源/运行状态枚举
type VMPower int

// 基本状态
const (
	PowerStopped VMPower = 0x0 // 停止
	PowerStarted VMPower = 0x1 // 启动
	PowerSuspend VMPower = 0x2 // 暂停
)

// 进行状态
const (
	PowerOnStop VMPower = 0x3 // 停止
	PowerOnOpen VMPower = 0x4 // 打开
	PowerOnSave VMPower = 0x5 // 保存
	PowerOnWake VMPower = 0x6 // 唤醒
)

// 命令状态
const (
	PowerSStart VMPower = 0x7 // 打开
	PowerSReset VMPower = 0x8 // 重置
	PowerHReset VMPower = 0x8 // 重置
	PowerSClose VMPower = 0x9 // 关闭
	PowerHClose VMPower = 0xa // 关闭
	PowerAPause VMPower = 0xb // 暂停
	PowerAWaked VMPower = 0xc // 唤醒
)

// 其他状态
const (
	PowerCrashed VMPower = 0xe // 崩溃
	PowerUnknown VMPower = 0xf // 未知
)

// 执行状态
const (
	PowerCreate VMPower = 0x10 // 创建
	PowerDelete VMPower = 0x11 // 销毁
	PowerConfig VMPower = 0x12 // 配置
	PowerBackup VMPower = 0x13 // 备份
	PowerStored VMPower = 0x14 // 还原
	PowerPasswd VMPower = 0x15 // 改密
	PowerSystem VMPower = 0x16 // 重装
	PowerSnNew  VMPower = 0x17 // 还原
	PowerSnBak  VMPower = 0x18 // 还原
	PowerOnHDD  VMPower = 0x19 // 硬盘
	PowerOnPCI  VMPower = 0x1a // 设备
	PowerOnUSB  VMPower = 0x1b // 设备
	PowerOnNet  VMPower = 0x1c // 网卡
	PowerOnNat  VMPower = 0x1d // 端口
	PowerOnWeb  VMPower = 0x1e // 反代
	PowerOnUFW  VMPower = 0x1f // 安全
)

var powerNames = map[VMPower]string{
	PowerStopped: "STOPPED",
	PowerStarted: "STARTED",
	PowerSuspend: "SUSPEND",
	PowerOnStop:  "ON_STOP",
	PowerOnOpen:  "ON_OPEN",
	PowerOnSave:  "ON_SAVE",
	PowerOnWake:  "ON_WAKE",
	PowerSStart:  "S_START",
	// H_RESET(0x8) 与 H_CLOSE(0xa) 是 S_RESET/S_CLOSE 的别名，字符串名称取先定义者
	PowerSReset:  "S_RESET",
	PowerSClose:  "S_CLOSE",
	PowerAPause:  "A_PAUSE",
	PowerAWaked:  "A_WAKED",
	PowerCrashed: "CRASHED",
	PowerUnknown: "UNKNOWN",
	PowerCreate:  "CREATE",
	PowerDelete:  "DELETE",
	PowerConfig:  "CONFIG",
	PowerBackup:  "BACKUP",
	PowerStored:  "STORED",
	PowerPasswd:  "PASSWD",
	PowerSystem:  "SYSTEM",
	PowerSnNew:   "SN_NEW",
	PowerSnBak:   "SN_BAK",
	PowerOnHDD:   "ON_HDD",
	PowerOnPCI:   "ON_PCI",
	PowerOnUSB:   "ON_USB",
	PowerOnNet:   "ON_NET",
	PowerOnNat:   "ON_NAT",
	PowerOnWeb:   "ON_WEB",
	PowerOnUFW:   "ON_UFW",
}

// String 返回枚举名称
func (p VMPower) String() string {
	if name, ok := powerNames[p]; ok {
		return name
	}
	return "UNKNOWN"
}

// ToJSON 将枚举值转换为 JSON 格式
func (p VMPower) ToJSON() string {
	return p.String()
}

// FromJSON 从 JSON 格式还原为枚举值
func FromJSON(jsonValue string) (VMPower, error) {
	for value, name := range powerNames {
		if name == jsonValue {
			return value, nil
		}
	}
	return PowerUnknown, errors.New("unknown VMPower: " + jsonValue)
}
