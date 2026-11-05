// Package config 从 IDCConfig.ini 读取可覆盖的运行参数。
//
// 行为与原 configparser 实现保持一致：
//   - 配置文件不存在时使用默认值
//   - 解析失败时记录警告并保留默认值
//   - 键名大小写不敏感，值会被去除首尾空白
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"CloudInit/apppath"
	"CloudInit/logger"
)

// Config IDC 配置管理器
type Config struct {
	// [server] 上报相关
	ReportInterval int    // 上报间隔（秒）
	ReportPort     int    // 上报端口
	ReportPath     string // 上报API路径
	ReportHost     string // 上报地址（留空则自动从网关推算）
	GatewayOffset  int    // 网关偏移量（默认.2，即网关.1时上报到.2）

	// [update] 自动更新相关
	UpdateEnabled  bool   // 是否启用自动更新
	UpdateInterval int    // 更新检查间隔（秒）
	UpdateRepo     string // Release API 地址

	// [log] 日志相关
	LogLevel     string // 日志级别
	LogRetention string // 日志保留时间
}

// Cfg 全局配置单例
var Cfg = newConfig()

// Path 返回配置文件路径（与可执行文件同目录）
func Path() string {
	return filepath.Join(apppath.BaseDir(), "IDCConfig.ini")
}

func newConfig() *Config {
	c := &Config{
		ReportInterval: 60,
		ReportPort:     1880,
		ReportPath:     "/api/client/upload",
		ReportHost:     "",
		GatewayOffset:  2,

		UpdateEnabled:  true,
		UpdateInterval: 86400,
		UpdateRepo:     "https://api.github.com/repos/OpenIDCSTeam/CloudInit/releases/latest",

		LogLevel:     "INFO",
		LogRetention: "7 days",
	}
	c.load()
	return c
}

// load 加载配置文件，不存在或解析失败则使用默认值
func (c *Config) load() {
	path := Path()

	data, err := os.ReadFile(path)
	if err != nil {
		logger.Debugf("[配置] 未找到 IDCConfig.ini，使用默认配置")
		return
	}

	sections, err := parseINI(string(data))
	if err != nil {
		logger.Warnf("[配置] 配置文件解析失败，使用默认配置: %v", err)
		return
	}

	if err = c.apply(sections); err != nil {
		logger.Warnf("[配置] 配置文件解析失败，使用默认配置: %v", err)
		return
	}

	logger.Infof("[配置] 已加载配置文件: %s", path)
}

// apply 将配置文件中的值应用到属性
func (c *Config) apply(sections map[string]map[string]string) error {
	if s, ok := sections["server"]; ok {
		if err := setInt(s, "report_interval", &c.ReportInterval); err != nil {
			return err
		}
		if err := setInt(s, "report_port", &c.ReportPort); err != nil {
			return err
		}
		if v, ok2 := s["report_path"]; ok2 {
			c.ReportPath = v
		}
		if v, ok2 := s["report_host"]; ok2 {
			c.ReportHost = v
		}
		if err := setInt(s, "gateway_offset", &c.GatewayOffset); err != nil {
			return err
		}
	}

	if u, ok := sections["update"]; ok {
		if v, ok2 := u["enabled"]; ok2 {
			b, err := parseBool(v)
			if err != nil {
				return err
			}
			c.UpdateEnabled = b
		}
		if err := setInt(u, "interval", &c.UpdateInterval); err != nil {
			return err
		}
		if v, ok2 := u["repo_url"]; ok2 {
			c.UpdateRepo = v
		}
	}

	if l, ok := sections["log"]; ok {
		if v, ok2 := l["level"]; ok2 {
			c.LogLevel = strings.ToUpper(v)
		}
		if v, ok2 := l["retention"]; ok2 {
			c.LogRetention = v
		}
	}

	return nil
}

func setInt(section map[string]string, key string, target *int) error {
	v, ok := section[key]
	if !ok {
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("section 值无效: %s = %q", key, v)
	}
	*target = n
	return nil
}

func parseBool(v string) (bool, error) {
	switch strings.ToLower(v) {
	case "1", "yes", "true", "on":
		return true, nil
	case "0", "no", "false", "off":
		return false, nil
	default:
		return false, fmt.Errorf("布尔值无效: %q", v)
	}
}

// parseINI 极简 INI 解析，等价于 configparser 默认行为
func parseINI(content string) (map[string]map[string]string, error) {
	sections := map[string]map[string]string{}
	var current map[string]string

	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		if strings.HasPrefix(line, "[") {
			end := strings.Index(line, "]")
			if end < 0 {
				return nil, fmt.Errorf("节头格式错误: %s", line)
			}
			name := line[1:end]
			if _, exists := sections[name]; exists {
				return nil, fmt.Errorf("节重复: %s", name)
			}
			current = map[string]string{}
			sections[name] = current
			continue
		}

		if current == nil {
			return nil, fmt.Errorf("缺少节头: %s", line)
		}

		sep := -1
		for i := 0; i < len(line); i++ {
			if line[i] == '=' || line[i] == ':' {
				sep = i
				break
			}
		}
		if sep <= 0 {
			return nil, fmt.Errorf("配置行格式错误: %s", line)
		}

		key := strings.ToLower(strings.TrimSpace(line[:sep]))
		if key == "" {
			return nil, fmt.Errorf("配置行格式错误: %s", line)
		}
		if _, exists := current[key]; exists {
			return nil, fmt.Errorf("配置项重复: %s", key)
		}
		current[key] = strings.TrimSpace(line[sep+1:])
	}

	return sections, nil
}
