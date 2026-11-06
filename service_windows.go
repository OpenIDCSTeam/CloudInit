//go:build windows

package main

import (
	"os"
	"time"

	"golang.org/x/sys/windows/svc"

	"CloudInit/logger"
)

// serviceName Windows 服务名（与安装脚本一致）
const serviceName = "cloudinit"

// runAsService 若当前进程由 SCM 启动，则按 Windows 服务方式运行
// 返回 true 表示已作为服务运行并结束，调用方无需再执行前台逻辑
func runAsService(app *CloudInitService) bool {
	isService, err := svc.IsWindowsService()
	if err != nil {
		logger.Debugf("[服务] 服务状态检测失败: %v", err)
		return false
	}
	if !isService {
		return false
	}

	if err = svc.Run(serviceName, &cloudInitWindowsService{app: app}); err != nil {
		logger.Errorf("[服务] 以 Windows 服务方式运行失败: %v", err)
		os.Exit(1)
	}

	return true
}

// cloudInitWindowsService Windows 服务回调
type cloudInitWindowsService struct {
	app *CloudInitService
}

// Execute 服务主函数
func (s *cloudInitWindowsService) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown | svc.AcceptParamChange

	status <- svc.Status{State: svc.StartPending}

	go s.app.Start()

	status <- svc.Status{State: svc.Running, Accepts: accepted}

	for request := range requests {
		switch request.Cmd {
		case svc.Interrogate:
			status <- request.CurrentStatus

		case svc.ParamChange:
			status <- request.CurrentStatus

		case svc.Stop, svc.Shutdown:
			status <- svc.Status{State: svc.StopPending}
			s.app.Stop()

			// 等待主循环退出，最多 10 秒
			select {
			case <-s.app.done:
			case <-time.After(10 * time.Second):
				logger.Warnf("[服务] 等待主循环退出超时")
			}

			return false, 0
		}
	}

	return false, 0
}
