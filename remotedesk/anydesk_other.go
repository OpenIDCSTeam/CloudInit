//go:build !windows

package remotedesk

// 非 Windows 平台不安装 AnyDesk

func (m *Manager) ensureAnyDesk() string { return "" }

func (m *Manager) ensureAnyDeskPassword(string) string { return "" }
