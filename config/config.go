// Package config 读取运行配置。
//
// 优先读取 config.json（默认取程序目录，找不到则扫描各磁盘根目录，
// 兼容挂载光盘上的 NoCloud/ConfigDrive 配置）；没有 JSON 时回退到
// 旧版 IDCConfig.ini，保证存量部署无需改动。
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"CloudInit/apppath"
	"CloudInit/logger"
)

// Config 运行配置
type Config struct {
	// 上报
	ReportEnabled      bool   // 是否启用状态上报
	ReportInterval     int    // 上报间隔（秒）
	ReportPort         int    // 上报端口（默认 1881）
	ReportFallbackPort int    // 兜底端口（默认 1880，0 表示不启用）
	ReportPath         string // 上报 API 路径
	ReportHost         string // 上报地址（IP/域名，留空则由网关推算）
	GatewayOffset      int    // 网关偏移量（默认 2，即 x.x.x.2）
	ScanSubnet         bool   // 是否允许网段扫描（默认关闭，风险高）

	// 功能开关
	PasswordChangeEnabled bool // 是否允许宿主机下发改密
	DiskExtendEnabled     bool // 是否自动扩容磁盘
	ActivationEnabled     bool // 是否自动激活系统
	VirtioEnabled         bool // 是否自动安装 virtio 驱动
	ToDeskEnabled         bool // 是否启用 ToDesk 管理
	AnyDeskEnabled        bool // 是否启用 AnyDesk 管理
	SunLoginEnabled       bool // 是否启用向日葵管理

	// 更新
	UpdateEnabled  bool   // 是否启用自动更新
	UpdateInterval int    // 更新检查间隔（秒）
	UpdateRepo     string // Release API 地址
	MirrorURL      string // 镜像站地址（EdgeOne），留空则只走 GitHub

	// virtio 驱动包地址
	VirtioURLX64 string
	VirtioURLX86 string

	// 日志
	LogLevel     string
	LogRetention string

	// SourcePath 实际加载的配置文件路径（空表示使用默认值）
	SourcePath string
}

// Cfg 全局配置单例
var Cfg = newConfig()

// Path 返回默认配置路径（程序目录下的 config.json）
func Path() string {
	return filepath.Join(apppath.BaseDir(), "config.json")
}

// LegacyPath 返回旧版 INI 配置路径
func LegacyPath() string {
	return filepath.Join(apppath.BaseDir(), "IDCConfig.ini")
}

func newConfig() *Config {
	c := defaultConfig()
	c.load()
	return c
}

// defaultConfig 返回默认配置（未读取文件）
func defaultConfig() *Config {
	return &Config{
		ReportEnabled:      true,
		ReportInterval:     60,
		ReportPort:         1881,
		ReportFallbackPort: 1880,
		ReportPath:         "/api/client/upload",
		ReportHost:         "",
		GatewayOffset:      2,
		ScanSubnet:         false, // 默认不扫描网段：避免内网主机冒充主控

		PasswordChangeEnabled: true,
		DiskExtendEnabled:     true,
		ActivationEnabled:     true,
		VirtioEnabled:         true,
		ToDeskEnabled:         true,
		AnyDeskEnabled:        true,
		SunLoginEnabled:       true,

		UpdateEnabled:  true,
		UpdateInterval: 86400,
		UpdateRepo:     "https://api.github.com/repos/OpenIDCSTeam/CloudInit/releases/latest",
		MirrorURL:      "https://eo-idc.pika.net.cn/cloudinit",

		VirtioURLX64: "https://fedorapeople.org/groups/virt/virtio-win/direct-downloads/archive-virtio/virtio-win-0.1.302-1/virtio-win-gt-x64.msi",
		VirtioURLX86: "https://fedorapeople.org/groups/virt/virtio-win/direct-downloads/archive-virtio/virtio-win-0.1.302-1/virtio-win-gt-x86.msi",

		LogLevel:     "INFO",
		LogRetention: "7 days",
	}
}

// load 按优先级加载配置文件
func (c *Config) load() {
	for _, path := range discoverConfigPaths() {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		if strings.HasSuffix(strings.ToLower(path), ".json") {
			if err = c.applyJSON(data); err != nil {
				logger.Warnf("[配置] %s 解析失败，继续查找其它配置: %v", path, err)
				continue
			}
		} else {
			sections, errINI := parseINI(string(data))
			if errINI != nil {
				logger.Warnf("[配置] %s 解析失败，继续查找其它配置: %v", path, errINI)
				continue
			}
			if err = c.applyINI(sections); err != nil {
				logger.Warnf("[配置] %s 应用失败，继续查找其它配置: %v", path, err)
				continue
			}
		}

		c.SourcePath = path
		logger.Infof("[配置] 已加载配置文件: %s", path)
		return
	}

	logger.Infof("[配置] 未找到 config.json / IDCConfig.ini，使用默认配置")
}

// applyJSON 应用 config.json
func (c *Config) applyJSON(data []byte) error {
	var file fileConfig
	if err := json.Unmarshal(data, &file); err != nil {
		return err
	}

	if s := file.Server; s != nil {
		setBoolPtr(s.Enabled, &c.ReportEnabled)
		setIntPtr(s.Interval, &c.ReportInterval)
		setIntPtr(s.Port, &c.ReportPort)
		setIntPtr(s.FallbackPort, &c.ReportFallbackPort)
		setStringPtr(s.Path, &c.ReportPath)
		setStringPtr(s.Host, &c.ReportHost)
		setIntPtr(s.GatewayOffset, &c.GatewayOffset)
		setBoolPtr(s.ScanSubnet, &c.ScanSubnet)
	}

	if f := file.Features; f != nil {
		setBoolPtr(f.Report, &c.ReportEnabled)
		setBoolPtr(f.PasswordChange, &c.PasswordChangeEnabled)
		setBoolPtr(f.DiskExtend, &c.DiskExtendEnabled)
		setBoolPtr(f.Activation, &c.ActivationEnabled)
		setBoolPtr(f.Virtio, &c.VirtioEnabled)
		setBoolPtr(f.ToDesk, &c.ToDeskEnabled)
		setBoolPtr(f.AnyDesk, &c.AnyDeskEnabled)
		setBoolPtr(f.SunLogin, &c.SunLoginEnabled)
	}

	if u := file.Update; u != nil {
		setBoolPtr(u.Enabled, &c.UpdateEnabled)
		setIntPtr(u.Interval, &c.UpdateInterval)
		setStringPtr(u.Repo, &c.UpdateRepo)
		setStringPtr(u.Mirror, &c.MirrorURL)
	}

	if v := file.Virtio; v != nil {
		setStringPtr(v.URLX64, &c.VirtioURLX64)
		setStringPtr(v.URLX86, &c.VirtioURLX86)
	}

	if l := file.Log; l != nil {
		if l.Level != nil {
			c.LogLevel = strings.ToUpper(*l.Level)
		}
		setStringPtr(l.Retention, &c.LogRetention)
	}

	return c.validate()
}

// applyINI 应用旧版 IDCConfig.ini
func (c *Config) applyINI(sections map[string]map[string]string) error {
	if s, ok := sections["server"]; ok {
		if v, ok2 := s["report_enabled"]; ok2 {
			if b, err := parseBool(v); err == nil {
				c.ReportEnabled = b
			}
		}
		if err := setInt(s, "report_interval", &c.ReportInterval); err != nil {
			return err
		}
		if err := setInt(s, "report_port", &c.ReportPort); err != nil {
			return err
		}
		if err := setInt(s, "report_fallback_port", &c.ReportFallbackPort); err != nil {
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
		if v, ok2 := s["scan_subnet"]; ok2 {
			if b, err := parseBool(v); err == nil {
				c.ScanSubnet = b
			}
		}
	}

	if f, ok := sections["features"]; ok {
		boolKeys := map[string]*bool{
			"report":          &c.ReportEnabled,
			"password_change": &c.PasswordChangeEnabled,
			"disk_extend":     &c.DiskExtendEnabled,
			"activation":      &c.ActivationEnabled,
			"virtio":          &c.VirtioEnabled,
			"todesk":          &c.ToDeskEnabled,
			"anydesk":         &c.AnyDeskEnabled,
			"sunlogin":        &c.SunLoginEnabled,
		}
		for key, target := range boolKeys {
			if v, ok2 := f[key]; ok2 {
				if b, err := parseBool(v); err == nil {
					*target = b
				}
			}
		}
	}

	if u, ok := sections["update"]; ok {
		if v, ok2 := u["enabled"]; ok2 {
			if b, err := parseBool(v); err == nil {
				c.UpdateEnabled = b
			}
		}
		if err := setInt(u, "interval", &c.UpdateInterval); err != nil {
			return err
		}
		if v, ok2 := u["repo_url"]; ok2 {
			c.UpdateRepo = v
		}
		if v, ok2 := u["mirror_url"]; ok2 {
			c.MirrorURL = v
		}
	}

	if s, ok := sections["system"]; ok {
		if v, ok2 := s["virtio_enabled"]; ok2 {
			if b, err := parseBool(v); err == nil {
				c.VirtioEnabled = b
			}
		}
		if v, ok2 := s["virtio_url_x64"]; ok2 {
			c.VirtioURLX64 = v
		}
		if v, ok2 := s["virtio_url_x86"]; ok2 {
			c.VirtioURLX86 = v
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

	return c.validate()
}

// validate 修正明显不合法的取值
func (c *Config) validate() error {
	if c.ReportPort <= 0 || c.ReportPort > 65535 {
		return fmt.Errorf("上报端口无效: %d", c.ReportPort)
	}
	if c.ReportInterval < 5 {
		c.ReportInterval = 5
	}
	if c.GatewayOffset < 1 || c.GatewayOffset > 254 {
		c.GatewayOffset = 2
	}
	if c.ReportFallbackPort < 0 || c.ReportFallbackPort > 65535 {
		c.ReportFallbackPort = 0
	}
	return nil
}

// fileConfig config.json 结构（指针字段用于区分「未配置」与「零值」）
type fileConfig struct {
	Server *struct {
		Enabled       *bool   `json:"enabled"`
		Interval      *int    `json:"interval"`
		Port          *int    `json:"port"`
		FallbackPort  *int    `json:"fallback_port"`
		Path          *string `json:"path"`
		Host          *string `json:"host"`
		GatewayOffset *int    `json:"gateway_offset"`
		ScanSubnet    *bool   `json:"scan_subnet"`
	} `json:"server"`
	Features *struct {
		Report         *bool `json:"report"`
		PasswordChange *bool `json:"password_change"`
		DiskExtend     *bool `json:"disk_extend"`
		Activation     *bool `json:"activation"`
		Virtio         *bool `json:"virtio"`
		ToDesk         *bool `json:"todesk"`
		AnyDesk        *bool `json:"anydesk"`
		SunLogin       *bool `json:"sunlogin"`
	} `json:"features"`
	Update *struct {
		Enabled  *bool   `json:"enabled"`
		Interval *int    `json:"interval"`
		Repo     *string `json:"repo_url"`
		Mirror   *string `json:"mirror_url"`
	} `json:"update"`
	Virtio *struct {
		URLX64 *string `json:"url_x64"`
		URLX86 *string `json:"url_x86"`
	} `json:"virtio"`
	Log *struct {
		Level     *string `json:"level"`
		Retention *string `json:"retention"`
	} `json:"log"`
}

func setBoolPtr(src *bool, dst *bool) {
	if src != nil {
		*dst = *src
	}
}

func setIntPtr(src *int, dst *int) {
	if src != nil {
		*dst = *src
	}
}

func setStringPtr(src *string, dst *string) {
	if src != nil && strings.TrimSpace(*src) != "" {
		*dst = strings.TrimSpace(*src)
	}
}

func setInt(section map[string]string, key string, target *int) error {
	v, ok := section[key]
	if !ok {
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("配置值无效: %s = %q", key, v)
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

// parseINI 极简 INI 解析
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
			name := strings.ToLower(line[1:end])
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
