import json
from .VMPowers import VMPowers as VPower


class HWStatus:
    """硬件状态数据模型"""

    def __init__(self, config=None, /, **kwargs):
        # 基础数据
        self.ac_status: VPower = VPower.UNKNOWN
        self.cpu_model: str = ""
        self.cpu_total: int = 0
        self.cpu_usage: int = 0
        self.mem_total: int = 0
        self.mem_usage: int = 0
        self.hdd_total: int = 0
        self.hdd_usage: int = 0
        self.ext_usage: dict = {}
        # 网络信息
        self.flu_total: int = 0
        self.flu_usage: int = 0
        self.nat_total: int = 0
        self.nat_usage: int = 0
        self.web_total: int = 0
        self.web_usage: int = 0
        # 其他信息
        self.gpu_usage: dict = {}
        self.gpu_total: int = 0
        self.network_u: int = 0
        self.network_d: int = 0
        self.network_a: int = 0
        self.cpu_heats: int = 0
        self.cpu_power: int = 0
        # 虚拟机信息
        self.vm_name: str = ""
        self.vm_pass: str = ""
        # 加载传入的参数
        if config is not None:
            self._read(config)
        self._load(**kwargs)

    def _load(self, **kwargs):
        """从关键字参数加载数据"""
        for key, value in kwargs.items():
            if hasattr(self, key):
                setattr(self, key, value)

    def _read(self, data: dict):
        """从字典读取数据"""
        for key, value in data.items():
            if hasattr(self, key):
                setattr(self, key, value)

    def to_dict(self) -> dict:
        """转换为字典（用于JSON序列化）"""
        return {
            "ac_status": str(self.ac_status),
            "cpu_model": self.cpu_model,
            "cpu_total": self.cpu_total,
            "cpu_usage": self.cpu_usage,
            "mem_total": self.mem_total,
            "mem_usage": self.mem_usage,
            "hdd_total": self.hdd_total,
            "hdd_usage": self.hdd_usage,
            "ext_usage": self.ext_usage,
            "flu_total": self.flu_total,
            "flu_usage": self.flu_usage,
            "nat_total": self.nat_total,
            "nat_usage": self.nat_usage,
            "web_total": self.web_total,
            "web_usage": self.web_usage,
            "gpu_usage": self.gpu_usage,
            "gpu_total": self.gpu_total,
            "network_u": self.network_u,
            "network_d": self.network_d,
            "network_a": self.network_a,
            "cpu_heats": self.cpu_heats,
            "cpu_power": self.cpu_power,
            "vm_name": self.vm_name,
            "vm_pass": self.vm_pass,
        }

    def __str__(self):
        return json.dumps(self.to_dict())