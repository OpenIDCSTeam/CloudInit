"""
IDCConfig 配置加载模块
从 IDCConfig.ini 文件读取可覆盖的运行参数
"""

import os
import configparser

from loguru import logger

# 配置文件路径（与可执行文件同目录）
_CONFIG_DIR = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
CONFIG_PATH = os.path.join(_CONFIG_DIR, "IDCConfig.ini")


class IDCConfig:
    """IDC 配置管理器，支持从 INI 文件覆盖默认参数"""

    # ==================== 默认值 ====================
    # [server] 上报相关
    report_interval: int = 60          # 上报间隔（秒）
    report_port: int = 1880            # 上报端口
    report_path: str = "/api/client/upload"  # 上报API路径
    report_host: str = ""              # 上报地址（留空则自动从网关推算）
    gateway_offset: int = 2            # 网关偏移量（默认.2，即网关.1时上报到.2）

    # [update] 自动更新相关
    update_enabled: bool = True        # 是否启用自动更新
    update_interval: int = 86400       # 更新检查间隔（秒）
    update_repo: str = "https://api.github.com/repos/OpenIDCSTeam/CloudInit/releases/latest"

    # [log] 日志相关
    log_level: str = "INFO"            # 日志级别
    log_retention: str = "7 days"      # 日志保留时间

    def __init__(self):
        self._parser = configparser.ConfigParser()
        self.load()

    def load(self):
        """加载配置文件，不存在则使用默认值"""
        if not os.path.exists(CONFIG_PATH):
            logger.debug("[配置] 未找到 IDCConfig.ini，使用默认配置")
            return

        try:
            self._parser.read(CONFIG_PATH, encoding="utf-8")
            self._apply()
            logger.info("[配置] 已加载配置文件: {}", CONFIG_PATH)
        except Exception as e:
            logger.warning("[配置] 配置文件解析失败，使用默认配置: {}", e)

    def _apply(self):
        """将配置文件中的值应用到属性"""
        # [server]
        if self._parser.has_section("server"):
            s = self._parser["server"]
            self.report_interval = s.getint("report_interval", self.report_interval)
            self.report_port = s.getint("report_port", self.report_port)
            self.report_path = s.get("report_path", self.report_path)
            self.report_host = s.get("report_host", self.report_host)
            self.gateway_offset = s.getint("gateway_offset", self.gateway_offset)

        # [update]
        if self._parser.has_section("update"):
            u = self._parser["update"]
            self.update_enabled = u.getboolean("enabled", self.update_enabled)
            self.update_interval = u.getint("interval", self.update_interval)
            self.update_repo = u.get("repo_url", self.update_repo)

        # [log]
        if self._parser.has_section("log"):
            l = self._parser["log"]
            self.log_level = l.get("level", self.log_level).upper()
            self.log_retention = l.get("retention", self.log_retention)


# 全局单例
config = IDCConfig()
