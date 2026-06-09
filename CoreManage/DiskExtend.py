import threading

from loguru import logger
from OSPlatform.PlatformBase import PlatformBase


class DiskExtend:
    """磁盘智能扩容 - 非阻塞执行"""

    def __init__(self, platform: PlatformBase):
        self._platform = platform

    def extend(self):
        """非阻塞执行磁盘扩容"""
        logger.info("[磁盘扩容] 启动后台扩容任务")
        threading.Thread(target=self._do_extend, daemon=True, name="DiskExtend").start()

    def _do_extend(self):
        """实际执行扩容（在后台线程中运行）"""
        try:
            self._platform.extend_disk()
            logger.info("[磁盘扩容] 扩容任务完成")
        except Exception as e:
            logger.error("[磁盘扩容] 扩容失败: {}", e)