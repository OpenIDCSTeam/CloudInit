"""
VMManage - 虚拟机配置管理模块
负责非阻塞地设置主机名、用户密码等虚拟机初始化配置
"""

import threading

from loguru import logger
from OSPlatform.PlatformBase import PlatformBase


class VMManage:
    """
    虚拟机配置管理器

    通过后台线程非阻塞执行配置任务，确保不影响主循环上报。
    每次收到宿主机下发的配置信息后触发。
    """

    def __init__(self, platform: PlatformBase):
        self._platform = platform
        self._lock = threading.Lock()  # 防止并发配置冲突
        self.vm_config = {
            "hs_name": "",
            "vm_uuid": "",
            "vm_pass": "",
        }

    def manage(self):
        """
        触发虚拟机配置（非阻塞）

        检查配置是否有效，有效则启动后台线程执行。
        使用锁机制避免多次并发配置。
        """
        if not self.vm_config.get("vm_uuid") or not self.vm_config.get("vm_pass"):
            return

        # 避免重复启动配置线程
        if not self._lock.acquire(blocking=False):
            logger.debug("[VM配置] 上一次配置尚未完成，跳过")
            return

        threading.Thread(
            target=self._do_manage,
            args=(self.vm_config["vm_uuid"], self.vm_config["vm_pass"]),
            daemon=True,
            name="VMManageThread"
        ).start()

    def _do_manage(self, vm_uuid: str, vm_pass: str):
        """实际执行配置（后台线程）"""
        try:
            # 设置主机名（包含hosts更新）
            try:
                self._platform.set_hostname(vm_uuid)
            except Exception as e:
                logger.error("[VM配置] 设置主机名失败: {}", e)

            # 设置root密码
            try:
                self._platform.set_password("root", vm_pass)
            except Exception as e:
                logger.error("[VM配置] 设置root密码失败: {}", e)

            # 设置user密码
            try:
                self._platform.set_password("user", vm_pass)
            except Exception as e:
                logger.error("[VM配置] 设置user密码失败: {}", e)

        except Exception as e:
            logger.error("[VM配置] 配置过程异常: {}", e)
        finally:
            self._lock.release()
