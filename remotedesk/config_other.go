//go:build !windows

package remotedesk

// disableToDeskAutoUpdate 非 Windows 平台无 ToDesk 客户端，留空实现
func (m *ToDeskManager) disableToDeskAutoUpdate() {}
