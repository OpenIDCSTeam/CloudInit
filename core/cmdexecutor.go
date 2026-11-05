// CmdExecutor - 远程命令执行模块
//
// 工作流程：
// 1. 主循环上报状态时，HostAgent 在响应中携带 vm_cmd 字段
// 2. CmdExecutor 解析命令，在后台协程中非阻塞执行
// 3. 执行完成后，将结果通过 HTTP POST 回传给 HostAgent
package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"CloudInit/logger"
)

// CmdExecutor 远程命令执行器
type CmdExecutor struct {
	client *http.Client
}

// NewCmdExecutor 创建命令执行器
func NewCmdExecutor() *CmdExecutor {
	return &CmdExecutor{client: &http.Client{}}
}

// cmdResultPayload 命令执行结果回传数据
type cmdResultPayload struct {
	CmdID    string  `json:"cmd_id"`
	Command  string  `json:"command"`
	ExitCode int     `json:"exit_code"`
	Stdout   string  `json:"stdout"`
	Stderr   string  `json:"stderr"`
	Success  bool    `json:"success"`
	Duration float64 `json:"duration"`
	Platform string  `json:"platform"`
}

// ExecuteCommand 非阻塞执行来自 HostAgent 的命令
func (e *CmdExecutor) ExecuteCommand(cmdData map[string]interface{}, reportURL string) {
	if len(cmdData) == 0 {
		logger.Warnf("[命令执行] 收到空命令，跳过")
		return
	}

	command := stringValue(cmdData["command"])
	if command == "" {
		logger.Warnf("[命令执行] 收到空命令，跳过")
		return
	}

	cmdID := stringValue(cmdData["cmd_id"])
	timeout := secondsValue(cmdData["timeout"], 60)

	logger.Infof("[命令执行] 收到命令 [%s]: %s", cmdID, command)

	go e.doExecute(cmdID, command, timeout, reportURL)
}

// doExecute 实际执行命令（后台协程）
func (e *CmdExecutor) doExecute(cmdID, command string, timeout float64, reportURL string) {
	startTime := time.Now()
	result := cmdResultPayload{
		CmdID:    cmdID,
		Command:  command,
		ExitCode: -1,
		Platform: systemName(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout*float64(time.Second)))
	defer cancel()

	logger.Infof("[命令执行] 开始执行 [%s]: %s", cmdID, command)

	cmd := newShellCommand(ctx, command)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	outText := sanitizeText(stdout.String())
	errText := sanitizeText(stderr.String())

	switch {
	case err == nil:
		result.ExitCode = 0
		result.Stdout = tailText(outText, 4096)
		result.Stderr = tailText(errText, 2048)
		result.Success = true
		logger.Infof("[命令执行] 命令 [%s] 执行成功, 退出码: 0", cmdID)

	case ctx.Err() == context.DeadlineExceeded:
		result.Stderr = fmt.Sprintf("命令执行超时（%s秒）", formatSeconds(timeout))
		result.ExitCode = -2
		logger.Errorf("[命令执行] 命令 [%s] 执行超时（%s秒）", cmdID, formatSeconds(timeout))

	default:
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
			result.Stdout = tailText(outText, 4096)
			result.Stderr = tailText(errText, 2048)
			result.Success = false
			logger.Warnf("[命令执行] 命令 [%s] 执行完成, 退出码: %d", cmdID, result.ExitCode)
		} else {
			result.Stderr = err.Error()
			result.ExitCode = -3
			logger.Errorf("[命令执行] 命令 [%s] 执行异常: %v", cmdID, err)
		}
	}

	if strings.TrimSpace(outText) != "" {
		logger.Debugf("[命令执行] stdout: %s", headText(outText, 500))
	}
	if strings.TrimSpace(errText) != "" {
		logger.Debugf("[命令执行] stderr: %s", headText(errText, 500))
	}

	// Python: round(time.time() - start_time, 2)
	duration := math.Round(float64(time.Since(startTime))/float64(time.Second)*100) / 100
	result.Duration = duration

	// 回传结果给 HostAgent
	e.reportResult(result, reportURL)
}

// reportResult 将命令执行结果回传给 HostAgent
func (e *CmdExecutor) reportResult(result cmdResultPayload, reportURL string) {
	if reportURL == "" {
		logger.Warnf("[命令执行] 无回传地址，结果仅记录本地日志")
		return
	}

	// 构建回传URL: /api/client/cmd_result
	callbackURL := reportURL + "/api/client/cmd_result"

	body, err := json.Marshal(result)
	if err != nil {
		logger.Errorf("[命令执行] 结果序列化失败: %v", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	logger.Infof("[命令执行] 回传结果到: %s", callbackURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, callbackURL, bytes.NewReader(body))
	if err != nil {
		logger.Errorf("[命令执行] 结果回传异常: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			logger.Errorf("[命令执行] 结果回传超时: %s", callbackURL)
		} else {
			logger.Errorf("[命令执行] 结果回传连接失败: %s", callbackURL)
		}
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		logger.Infof("[命令执行] 结果回传成功 [%s]", result.CmdID)
	} else {
		logger.Warnf("[命令执行] 结果回传失败, HTTP %d", resp.StatusCode)
	}
}

// systemName 对应 platform.system()
func systemName() string {
	switch runtime.GOOS {
	case "windows":
		return "Windows"
	case "darwin":
		return "Darwin"
	case "linux":
		return "Linux"
	default:
		return runtime.GOOS
	}
}

// sanitizeText 模拟 Python 以 utf-8/errors=replace 解码子进程输出
func sanitizeText(s string) string {
	return strings.ToValidUTF8(s, "\uFFFD")
}

// stringValue 将 JSON 值转换为字符串
func stringValue(value interface{}) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case float64:
		return formatSeconds(v)
	case json.Number:
		return v.String()
	case bool:
		if v {
			return "True"
		}
		return "False"
	default:
		return fmt.Sprint(v)
	}
}

// secondsValue 读取秒数配置（默认 60）
func secondsValue(value interface{}, defaultValue float64) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case json.Number:
		if f, err := v.Float64(); err == nil {
			return f
		}
	case string:
		var f float64
		if _, err := fmt.Sscanf(v, "%f", &f); err == nil {
			return f
		}
	}
	return defaultValue
}

// formatSeconds 按 Python 风格输出秒数（整数值不带小数点）
func formatSeconds(value float64) string {
	if value == math.Trunc(value) && math.Abs(value) < 1e15 {
		return fmt.Sprintf("%.0f", value)
	}
	return fmt.Sprintf("%v", value)
}

// headText 截取前 n 个字符
func headText(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

// tailText 截取最后 n 个字符
func tailText(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[len(runes)-n:])
}
