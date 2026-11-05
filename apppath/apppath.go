// Package apppath 提供程序运行目录相关的路径工具。
//
// 对应 Python 版本中 getattr(sys, 'frozen', False) 的判定：
// 打包环境使用可执行文件所在目录，开发环境回退到工作目录。
package apppath

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	once    sync.Once
	baseDir string
	exePath string
)

// isTempBuildPath 判断是否为 go run 产生的临时构建目录
func isTempBuildPath(dir string) bool {
	return strings.Contains(strings.ToLower(dir), "go-build")
}

// BaseDir 返回程序运行根目录（配置文件、版本文件、ToDesk 组件所在目录）
func BaseDir() string {
	initPaths()
	return baseDir
}

// ExePath 返回当前可执行文件的绝对路径
func ExePath() string {
	initPaths()
	return exePath
}

// ToolDir 返回工具目录（程序目录下的 tool 文件夹）
func ToolDir() string {
	return filepath.Join(BaseDir(), "tool")
}

// EnsureToolDir 创建并返回工具目录
func EnsureToolDir() string {
	dir := ToolDir()
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

func initPaths() {
	once.Do(func() {
		exe, err := os.Executable()
		if err == nil && !isTempBuildPath(filepath.Dir(exe)) {
			exePath = exe
			baseDir = filepath.Dir(exe)
			return
		}

		// 开发环境（go run）：使用工作目录
		if wd, err2 := os.Getwd(); err2 == nil {
			baseDir = wd
		} else {
			baseDir = "."
		}

		if len(os.Args) > 0 {
			if abs, err3 := filepath.Abs(os.Args[0]); err3 == nil {
				exePath = abs
				return
			}
		}
		exePath = filepath.Join(baseDir, "CloudInit")
	})
}
