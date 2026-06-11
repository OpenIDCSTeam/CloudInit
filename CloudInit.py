"""
CloudInit 服务入口
OpenIDC 虚拟机初始化与状态上报服务

功能：
- 定时采集虚拟机硬件状态并上报至宿主机
- 接收宿主机下发的控制指令（关机/重启/改密/改主机名）
- 后台自动磁盘扩容
- 后台自动版本更新（24h周期）
"""

import sys
import signal
import time

import requests
from loguru import logger

from NICManager.NCManage import NCManage
from VMUploader.VMStatus import VMStatus
from CoreManage.CoreConfig import config
from CoreManage import VMManage, DiskExtend, PowerCtrl, AutoUpdate
from CoreManage.CmdExecutor import CmdExecutor
from OSPlatform.PlatformFactory import get_platform
from RemoteDesk.ToDeskManager import ToDeskManager

# ==================== 日志配置 ====================
logger.remove()
logger.add(
    sys.stdout,
    format="<green>{time:YYYY-MM-DD HH:mm:ss}</green> | <level>{level:<7}</level> | <cyan>{message}</cyan>",
    level=config.log_level,
    colorize=True,
)
logger.add(
    "logs/cloudinit_{time:YYYY-MM-DD}.log",
    format="{time:YYYY-MM-DD HH:mm:ss} | {level:<7} | {message}",
    level="DEBUG",
    rotation="1 day",
    retention=config.log_retention,
    encoding="utf-8",
)


class CloudInitService:
    """
    CloudInit 主服务类

    职责：
    1. 初始化平台适配层、状态采集器、管理模块
    2. 启动后台任务（自动更新、磁盘扩容）
    3. 主循环定时上报状态、接收并执行控制指令
    """

    def __init__(self):
        self._running = False

        # 初始化平台适配层
        try:
            self._platform = get_platform()
        except RuntimeError as e:
            logger.critical("平台初始化失败: {}", e)
            sys.exit(1)

        # 初始化各功能模块
        self.vm_status = VMStatus()
        self.vm_manage = VMManage(self._platform)
        self.power_ctrl = PowerCtrl(self._platform)
        self.disk_extend = DiskExtend(self._platform)
        self.auto_update = AutoUpdate()
        self.cmd_executor = CmdExecutor()
        self.toDesk_manager = ToDeskManager()

        # 网络增量计算缓存
        self._last_network_u = 0
        self._last_network_d = 0
        self._last_flu_usage = 0

    def start(self):
        """启动服务主流程"""
        self._running = True
        self._register_signals()

        logger.info("=" * 50)
        logger.info("CloudInit 服务启动")
        logger.info("平台: {} | 上报间隔: {}s | 端口: {}",
                    self._platform.__class__.__name__,
                    config.report_interval,
                    config.report_port)
        if config.report_host:
            logger.info("上报地址: {}（固定配置）", config.report_host)
        else:
            logger.info("上报地址: 自动推算（网关偏移: +{}）", config.gateway_offset)
        logger.info("=" * 50)

        # 启动后台任务（非阻塞，失败不影响主流程）
        try:
            if config.update_enabled:
                self.auto_update.start()
            else:
                logger.info("[自动更新] 已通过配置禁用")
        except Exception as e:
            logger.error("自动更新启动失败（不影响主服务）: {}", e)

        try:
            self.disk_extend.extend()
        except Exception as e:
            logger.error("磁盘扩容启动失败（不影响主服务）: {}", e)

        # 启动ToDesk远程桌面管理（仅Windows）
        try:
            self.toDesk_manager.start()
        except Exception as e:
            logger.error("ToDesk管理启动失败（不影响主服务）: {}", e)

        # 非VMware环境下卸载VMware Tools（仅Windows平台）
        try:
            if hasattr(self._platform, 'uninstall_vmtools_if_not_vmware'):
                self._platform.uninstall_vmtools_if_not_vmware()
        except Exception as e:
            logger.error("VMTools检测启动失败（不影响主服务）: {}", e)

        # 异步激活Windows（仅Windows平台）
        try:
            if hasattr(self._platform, 'activate_windows'):
                self._platform.activate_windows()
        except Exception as e:
            logger.error("Windows激活启动失败（不影响主服务）: {}", e)

        # 进入主循环
        self._main_loop()

    def stop(self):
        """优雅停止服务"""
        self._running = False
        try:
            self.auto_update.stop()
        except Exception:
            pass
        try:
            self.toDesk_manager.stop()
        except Exception:
            pass
        logger.info("CloudInit 服务已停止")

    def _register_signals(self):
        """注册系统信号，支持优雅退出"""
        signal.signal(signal.SIGINT, self._signal_handler)
        signal.signal(signal.SIGTERM, self._signal_handler)

    def _signal_handler(self, signum, _frame):
        """信号处理回调"""
        logger.info("收到退出信号({}), 正在停止服务...", signum)
        self.stop()

    def _main_loop(self):
        """
        主循环 - 定时采集并上报虚拟机状态

        流程：
        1. 等待上报间隔
        2. 采集硬件状态
        3. 计算网络增量
        4. 遍历网卡，向宿主机上报
        5. 处理宿主机返回的控制指令
        """
        time_last = 0

        # 初始化网卡管理器
        try:
            nic_manager = NCManage()
        except Exception as e:
            logger.critical("网卡管理器初始化失败: {}", e)
            return

        while self._running:
            time.sleep(1)
            now = time.time()

            if now - time_last < config.report_interval:
                continue

            # 采集硬件状态（失败不中断循环）
            try:
                self.vm_status.status()
                self._calc_network_delta()
            except Exception as e:
                logger.error("状态采集异常: {}", e)
                time_last = now
                continue

            status_data = self.vm_status.to_dict()

            # 附加ToDesk远程桌面信息
            toDesk_info = self.toDesk_manager.get_toDesk_info()
            if toDesk_info:
                status_data["rdp_info"] = {
                    "todesk": toDesk_info
                }

            # 确定上报目标并发送
            if config.report_host:
                # 使用固定配置的上报地址
                url = f"http://{config.report_host}:{config.report_port}{config.report_path}"
                self._report_status(url, status_data, nic_mac="")
            else:
                # 自动从网卡网关推算上报地址
                for nic_name, nic_config in nic_manager.nic_list.items():
                    gateway = nic_config.ip4_gate
                    if not gateway or not gateway.endswith(".1"):
                        continue
                    if nic_config.mac_addr == "00:00:00:00:00:00":
                        continue

                    # 根据网关偏移量计算目标IP
                    parts = gateway.split(".")
                    parts[-1] = str(config.gateway_offset)
                    target_ip = ".".join(parts)

                    url = f"http://{target_ip}:{config.report_port}{config.report_path}"
                    url += f"?nic={nic_config.mac_addr}"
                    self._report_status(url, status_data, nic_mac=nic_config.mac_addr)

            time_last = now

    def _calc_network_delta(self):
        """计算网络增量数据（本周期相对上周期的变化量）"""
        current_u = self.vm_status.vm_status.network_u
        current_d = self.vm_status.vm_status.network_d
        current_flu = self.vm_status.vm_status.flu_usage

        self.vm_status.vm_status.network_u = current_u - self._last_network_u
        self.vm_status.vm_status.network_d = current_d - self._last_network_d
        self.vm_status.vm_status.flu_usage = current_flu - self._last_flu_usage

        self._last_network_u = current_u
        self._last_network_d = current_d
        self._last_flu_usage = current_flu

    def _report_status(self, url: str, data: dict, nic_mac: str = ""):
        """
        上报状态到宿主机，并处理返回的控制指令

        Args:
            url: 上报目标URL
            data: 状态数据字典
            nic_mac: 当前网卡MAC（用于日志标识）
        """
        try:
            resp = requests.post(url=url, json=data, timeout=5)
            if resp.status_code != 200:
                logger.warning("上报失败 [{}] HTTP {}", url, resp.status_code)
                return

            vm_data = resp.json().get("data")
            if not vm_data:
                return

            # 更新虚拟机配置信息
            self.vm_manage.vm_config["vm_uuid"] = vm_data.get("vm_uuid", "")
            self.vm_manage.vm_config["vm_pass"] = vm_data.get("vm_pass", "")

            # 处理控制指令
            vm_flag = vm_data.get("vm_flag", "")
            if vm_flag in ("ON_STOP", "S_CLOSE"):
                logger.warning("收到关机指令, 执行关机...")
                self.power_ctrl.shutdown_system()
                self._running = False
                return
            elif vm_flag == "S_RESET":
                logger.warning("收到重启指令, 执行重启...")
                self.power_ctrl.reboot_system()
                self._running = False
                return

            # 非阻塞执行虚拟机配置（改主机名、改密码）
            self.vm_manage.manage()

            # 处理远程命令下发（兼容服务端无此字段的情况）
            vm_cmd = vm_data.get("vm_cmd")
            if vm_cmd and isinstance(vm_cmd, dict) and vm_cmd.get("command"):
                # 构建回传基础URL（去掉路径部分）
                base_url = url.split("/api/")[0] if "/api/" in url else url.rsplit("/", 1)[0]
                self.cmd_executor.execute_command(vm_cmd, base_url)

        except requests.exceptions.ConnectionError:
            logger.debug("上报连接失败: {}", url)
        except requests.exceptions.Timeout:
            logger.debug("上报超时: {}", url)
        except Exception as e:
            logger.error("上报异常: {}", e)


# ==================== 入口 ====================
if __name__ == "__main__":
    service = CloudInitService()
    service.start()