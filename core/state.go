package core

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/shirou/gopsutil/v3/host"

	"CloudInit/apppath"
	"CloudInit/logger"
)

// runtimeState 持久化的运行状态（用于避免重复改主机名/改密码）
type runtimeState struct {
	BootID       string `json:"boot_id"`       // 本次开机的标识（开机时间）
	Hostname     string `json:"hostname"`      // 上次已处理的主机名
	Salt         string `json:"salt"`          // 密码哈希盐
	PasswordHash string `json:"password_hash"` // 上次已设置的密码（加盐哈希）
}

var (
	stateMu    sync.Mutex
	stateCache *runtimeState
)

// statePath 状态文件路径（程序目录，与 .version 同级）
func statePath() string {
	return filepath.Join(apppath.BaseDir(), ".state.json")
}

// loadState 读取状态（不存在时创建默认值）
func loadState() *runtimeState {
	stateMu.Lock()
	defer stateMu.Unlock()

	if stateCache != nil {
		return stateCache
	}

	state := &runtimeState{}
	if data, err := os.ReadFile(statePath()); err == nil {
		_ = json.Unmarshal(data, state)
	}
	if state.Salt == "" {
		state.Salt = randomHex(16)
	}

	stateCache = state
	return state
}

// saveState 写入状态文件
func saveState(state *runtimeState) {
	stateMu.Lock()
	defer stateMu.Unlock()

	stateCache = state

	data, err := json.Marshal(state)
	if err != nil {
		return
	}
	if err = os.WriteFile(statePath(), data, 0o600); err != nil {
		logger.Debugf("[状态] 保存运行状态失败: %v", err)
	}
}

// currentBootID 返回本次开机标识（开机时间戳）；取不到时回退到当天日期
func currentBootID() string {
	bootTime, err := host.BootTime()
	if err == nil && bootTime > 0 {
		return strconv.FormatUint(bootTime, 10)
	}
	return "unknown"
}

// passwordHash 计算加盐哈希
func passwordHash(salt, password string) string {
	sum := sha256.Sum256([]byte(salt + ":" + password))
	return hex.EncodeToString(sum[:])
}

// passwordMatches 判断密码是否与上次设置的一致
func (s *runtimeState) passwordMatches(password string) bool {
	if s.PasswordHash == "" {
		return false
	}
	return subtle.ConstantTimeCompare(
		[]byte(s.PasswordHash),
		[]byte(passwordHash(s.Salt, password)),
	) == 1
}

// randomHex 生成随机 hex 字符串
func randomHex(length int) string {
	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return "0123456789abcdef0123456789abcdef"
	}
	return hex.EncodeToString(buf)
}
