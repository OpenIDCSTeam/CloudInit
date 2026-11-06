// 工具目录管理与在线更新
//
// 工具（MASAIO.cmd、ToDeskSunDump.exe 等）默认放在程序目录的 tool 子目录，
// 启动时会把老版本放在程序根目录下的同名文件迁移进去（.version 除外）。
// 工具版本独立于主程序：发布资产中的 CloudInit-tools.json 描述每个工具的
// 版本、资产名与 sha256，客户端按版本号比较后增量更新。
package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"CloudInit/apppath"
	"CloudInit/logger"
)

const (
	// toolsManifestName 发布资产中的工具清单文件名
	toolsManifestName = "CloudInit-tools.json"
	// toolVersionFileName 本地工具版本记录文件（位于 tool 目录）
	toolVersionFileName = ".tool_versions.json"
)

// legacyToolNames 老版本放在程序根目录、需要迁移到 tool 目录的工具
var legacyToolNames = []string{
	"MASAIO.cmd",
	"ToDesk.exe",
	"AnyDesk.exe",
}

// MigrateTools 把旧版本遗留在程序根目录的工具迁移到 tool 目录
func MigrateTools() {
	migrateTools(apppath.BaseDir(), apppath.EnsureToolDir())
}

// migrateTools 迁移逻辑（baseDir 为程序根目录，toolDir 为目标工具目录）
func migrateTools(baseDir, toolDir string) {
	_ = os.MkdirAll(toolDir, 0o755)

	for _, name := range legacyToolNames {
		oldPath := filepath.Join(baseDir, name)
		newPath := filepath.Join(toolDir, name)

		if _, err := os.Stat(oldPath); err != nil {
			continue
		}
		if _, err := os.Stat(newPath); err == nil {
			continue // 目标已存在，保留新文件
		}

		if err := os.Rename(oldPath, newPath); err != nil {
			// 跨卷或文件被占用时退化为复制后删除
			if copyErr := copyFile(oldPath, newPath); copyErr != nil {
				logger.Warnf("[工具] 迁移 %s 到 tool 目录失败: %v", name, copyErr)
				continue
			}
			_ = os.Remove(oldPath)
		}

		logger.Infof("[工具] 已迁移到 tool 目录: %s", name)
	}
}

// toolsManifest 工具清单
type toolsManifest struct {
	Tools []toolEntry `json:"tools"`
}

// toolEntry 单个工具的版本信息
type toolEntry struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Asset   string `json:"asset"`
	SHA256  string `json:"sha256"`
}

// readToolVersions 读取本地工具版本记录
func readToolVersions(toolDir string) map[string]string {
	versions := map[string]string{}

	data, err := os.ReadFile(filepath.Join(toolDir, toolVersionFileName))
	if err != nil {
		return versions
	}
	_ = json.Unmarshal(data, &versions)
	return versions
}

// writeToolVersions 保存本地工具版本记录
func writeToolVersions(toolDir string, versions map[string]string) {
	data, err := json.MarshalIndent(versions, "", "  ")
	if err != nil {
		return
	}
	if err = os.WriteFile(filepath.Join(toolDir, toolVersionFileName), data, 0o644); err != nil {
		logger.Warnf("[工具更新] 版本记录保存失败: %v", err)
	}
}

// syncTools 根据发布资产同步工具，按版本号判断是否需要更新
func (a *AutoUpdate) syncTools(release map[string]interface{}) {
	a.syncToolsTo(apppath.EnsureToolDir(), release)
}

// syncToolsTo 把工具同步到指定目录
func (a *AutoUpdate) syncToolsTo(toolDir string, release map[string]interface{}) {
	assets := mapSlice(release, "assets")
	manifestAsset := findAssetByName(assets, toolsManifestName)
	if manifestAsset == nil {
		return
	}

	manifestPath := filepath.Join(toolDir, toolsManifestName+".download")

	if !a.downloadWithProxy(mapString(manifestAsset, "browser_download_url"), manifestPath) {
		logger.Debugf("[工具更新] 工具清单下载失败")
		return
	}

	data, err := os.ReadFile(manifestPath)
	_ = os.Remove(manifestPath)
	if err != nil {
		logger.Warnf("[工具更新] 工具清单读取失败: %v", err)
		return
	}

	var manifest toolsManifest
	if err = json.Unmarshal(data, &manifest); err != nil {
		logger.Warnf("[工具更新] 工具清单解析失败: %v", err)
		return
	}

	versions := readToolVersions(toolDir)
	changed := false

	for _, tool := range manifest.Tools {
		if tool.Name == "" || tool.Version == "" {
			continue
		}

		local := versions[tool.Name]
		if local != "" && !isNewerVersion(tool.Version, local) {
			continue
		}

		asset := findAssetByName(assets, tool.Asset)
		if asset == nil {
			logger.Warnf("[工具更新] 未找到工具资产: %s", tool.Asset)
			continue
		}

		targetPath := filepath.Join(toolDir, tool.Name)
		tempPath := targetPath + ".download"

		if !a.downloadWithProxy(mapString(asset, "browser_download_url"), tempPath) {
			logger.Warnf("[工具更新] %s 下载失败", tool.Name)
			continue
		}

		expected := tool.SHA256
		if expected == "" {
			expected = mapString(asset, "sha256")
		}
		if expected != "" && !verifyFileSHA256(tempPath, expected) {
			logger.Warnf("[工具更新] %s 校验失败，已丢弃", tool.Name)
			_ = os.Remove(tempPath)
			continue
		}

		if err = os.Rename(tempPath, targetPath); err != nil {
			logger.Warnf("[工具更新] %s 替换失败: %v", tool.Name, err)
			_ = os.Remove(tempPath)
			continue
		}

		versions[tool.Name] = tool.Version
		changed = true
		logger.Infof("[工具更新] %s 已更新到 %s", tool.Name, tool.Version)
	}

	if changed {
		writeToolVersions(toolDir, versions)
	}
}

// findAssetByName 按资产名查找
func findAssetByName(assets []interface{}, name string) map[string]interface{} {
	if name == "" {
		return nil
	}
	for _, asset := range assets {
		item, ok := asset.(map[string]interface{})
		if !ok {
			continue
		}
		if mapString(item, "name") == name {
			return item
		}
	}
	return nil
}

// verifyFileSHA256 校验文件 sha256
func verifyFileSHA256(path, expected string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()

	hash := sha256.New()
	if _, err = io.Copy(hash, file); err != nil {
		return false
	}

	return hex.EncodeToString(hash.Sum(nil)) == toLowerHex(expected)
}

// toLowerHex 统一 sha256 字符串格式（去空格、转小写）
func toLowerHex(value string) string {
	result := make([]byte, 0, len(value))
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' {
			continue
		}
		if ch >= 'A' && ch <= 'F' {
			ch += 'a' - 'A'
		}
		result = append(result, ch)
	}
	return string(result)
}

// copyFile 复制文件
func copyFile(src, dst string) error {
	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()

	target, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer target.Close()

	if _, err = io.Copy(target, source); err != nil {
		return err
	}
	return target.Sync()
}
