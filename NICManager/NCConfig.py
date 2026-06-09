"""网卡配置数据模型"""

from loguru import logger


class NCConfig:
    """单个网卡的配置信息"""

    def __init__(self, **kwargs):
        self.mac_addr: str = ""
        self.nic_type: str = ""
        self.ip4_addr: str = ""
        self.ip6_addr: str = ""
        self.ip4_gate: str = ""
        self.ip6_gate: str = ""
        self._load(**kwargs)

    def to_dict(self) -> dict:
        """转换为字典"""
        return {
            "mac_addr": self.mac_addr,
            "nic_type": self.nic_type,
            "ip4_addr": self.ip4_addr,
            "ip6_addr": self.ip6_addr,
            "ip4_gate": self.ip4_gate,
            "ip6_gate": self.ip6_gate,
        }

    def _load(self, **kwargs):
        """加载传入的参数"""
        for key, value in kwargs.items():
            if hasattr(self, key):
                setattr(self, key, value)
        if not self.mac_addr:
            self.mac_addr = self._generate_mac()

    def _generate_mac(self) -> str:
        """根据IPv4地址生成虚拟MAC地址"""
        if not self.ip4_addr:
            return "00:00:00:00:00:00"
        try:
            ip4_parts = self.ip4_addr.split(".")
            mac_suffix = ":".join(format(int(part), '02x') for part in ip4_parts)
            if self.ip4_addr.startswith("192"):
                return "00:1C:" + mac_suffix
            elif self.ip4_addr.startswith("172"):
                return "CC:D9:" + mac_suffix
            elif self.ip4_addr.startswith("100"):
                return "00:1E:" + mac_suffix
            elif self.ip4_addr.startswith("10"):
                return "10:F6:" + mac_suffix
            else:
                return "00:00:" + mac_suffix
        except (ValueError, IndexError) as e:
            logger.warning("[NCConfig] MAC地址生成失败: {}", e)
            return "00:00:00:00:00:00"