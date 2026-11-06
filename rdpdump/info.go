// Package rdpdump 内置远程桌面软件信息采集
//
// 原 PasswdDump 独立工具的能力已合并到主程序：
//   - ToDesk：配置文件读取设备代码，内存读取临时密码
//   - 向日葵：注册表/配置文件/内存读取识别码与验证码（适配 V12 便携版）
//   - AnyDesk：CLI 读取地址，配置读取固定密码
//
// 采集函数仅在 Windows 上有效，其它平台返回空信息。
package rdpdump

// Info 远程桌面连接信息
type Info struct {
	Code     string // 识别码/设备地址
	Password string // 密码/验证码
	Version  string // 软件版本
}

// Empty 是否未采集到任何信息
func (i Info) Empty() bool {
	return i.Code == "" && i.Password == ""
}
