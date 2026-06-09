"""CoreManage 核心管理模块"""

from .CoreManage import VMManage
from .DiskExtend import DiskExtend
from .PowerSetup import PowerCtrl
from .AutoUpdate import AutoUpdate
from OSPlatform.PlatformFactory import get_platform

__all__ = ["CoreManage.py", "DiskExtend", "PowerCtrl", "AutoUpdate", "get_platform"]
