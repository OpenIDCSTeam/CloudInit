package config

import (
	"os"
	"path/filepath"

	"CloudInit/apppath"
)

// discoverConfigPaths 返回配置文件候选路径（按优先级）
//
// 顺序：
//  1. 程序目录 config.json / IDCConfig.ini
//  2. 各磁盘根目录 config.json（含光驱、挂载的云盘）
//  3. 云初始化数据盘：<root>/cloud-init/config.json、<root>/cidata/config.json
//
// cidata 是 cloud-init NoCloud 数据源的卷标，ConfigDrive 也常挂在这个目录下。
func discoverConfigPaths() []string {
	base := apppath.BaseDir()

	paths := []string{
		filepath.Join(base, "config.json"),
		filepath.Join(base, "IDCConfig.ini"),
	}

	for _, root := range volumeRoots() {
		paths = append(paths,
			filepath.Join(root, "config.json"),
			filepath.Join(root, "cloud-init", "config.json"),
			filepath.Join(root, "cidata", "config.json"),
		)
	}

	return dedupe(paths)
}

// volumeRoots 枚举存在的磁盘根目录（Windows 为 A:\..Z:\，其它平台返回空）
func volumeRoots() []string {
	roots := make([]string, 0, 4)

	for ch := 'A'; ch <= 'Z'; ch++ {
		root := string(ch) + ":\\"
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			roots = append(roots, root)
		}
	}

	return roots
}

// dedupe 去重
func dedupe(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	result := make([]string, 0, len(paths))

	for _, path := range paths {
		cleaned := filepath.Clean(path)
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		result = append(result, cleaned)
	}

	return result
}
