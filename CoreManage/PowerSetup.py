from loguru import logger
from OSPlatform.PlatformBase import PlatformBase


class PowerCtrl:
    """系统电源控制（关机/重启）"""

    def __init__(self, platform: PlatformBase):
        self._platform = platform

    def shutdown_system(self):
        """执行系统关机命令"""
        logger.warning("[电源控制] 执行关机操作")
        self._platform.shutdown()

    def reboot_system(self):
        """执行系统重启命令"""
        logger.warning("[电源控制] 执行重启操作")
        self._platform.reboot()