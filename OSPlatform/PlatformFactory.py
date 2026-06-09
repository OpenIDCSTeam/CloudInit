import platform

from loguru import logger
from .PlatformBase import PlatformBase
from .PlatformLinux import PlatformLinux
from .PlatformWindows import PlatformWindows
from .PlatformMacOS import PlatformMacOS


def get_platform() -> PlatformBase:
    """根据当前操作系统返回对应的平台实现实例"""
    system = platform.system().lower()
    if system == "linux":
        return PlatformLinux()
    elif system == "windows":
        return PlatformWindows()
    elif system == "darwin":
        return PlatformMacOS()
    else:
        logger.error("[平台工厂] 不支持的操作系统: {}", system)
        raise RuntimeError(f"不支持的操作系统: {system}")
