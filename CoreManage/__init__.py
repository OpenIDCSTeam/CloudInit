"""CoreManage 核心管理模块"""

from .CoreManage import VMManage
from .DiskExtend import DiskExtend
from .PowerSetup import PowerCtrl
from .AutoUpdate import AutoUpdate
from .CmdExecutor import CmdExecutor
from OSPlatform.PlatformFactory import get_platform

__all__ = ["VMManage", "DiskExtend", "PowerCtrl", "AutoUpdate", "CmdExecutor", "get_platform"]
