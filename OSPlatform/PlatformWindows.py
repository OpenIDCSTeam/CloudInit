import os
import subprocess

from loguru import logger
from .PlatformBase import PlatformBase


class PlatformWindows(PlatformBase):
    """Windows平台操作实现"""

    # ==================== 电源控制 ====================

    def shutdown(self):
        """Windows关机"""
        try:
            logger.info("[系统关机] 执行 Windows 关机命令")
            subprocess.run(["shutdown", "/s", "/t", "0", "/f"], check=True)
        except Exception as e:
            logger.error("[系统关机] 关机命令执行失败: {}", e)

    def reboot(self):
        """Windows重启"""
        try:
            logger.info("[系统重启] 执行 Windows 重启命令")
            subprocess.run(["shutdown", "/r", "/t", "0", "/f"], check=True)
        except Exception as e:
            logger.error("[系统重启] 重启命令执行失败: {}", e)

    # ==================== 主机配置 ====================

    def get_hostname(self) -> str:
        """获取当前主机名"""
        result = self._run_cmd(["hostname"], shell=True)
        return result.stdout.strip()

    def set_hostname(self, hostname: str):
        """设置Windows主机名"""
        current = self.get_hostname()
        if current.lower() == hostname.lower():
            logger.info("[Windows主机名] 当前主机名已经是: {}，无需修改", hostname)
            return

        logger.info("[Windows主机名] 当前主机名: {}，需要修改为: {}", current, hostname)

        # 方案1: 使用 netdom (兼容 Windows 7)
        netdom_cmd = f'netdom renamecomputer %computername% /newname:{hostname} /force'
        result = self._run_cmd([netdom_cmd], shell=True)

        if result.returncode == 0:
            logger.info("[Windows主机名] netdom 设置成功，需要重启后生效: {}", hostname)
        else:
            # 方案2: PowerShell Rename-Computer (Windows 8+)
            logger.warning("[Windows主机名] netdom 设置失败: {}，尝试 Rename-Computer", result.stderr)
            powershell_cmd = f'Rename-Computer -NewName "{hostname}" -Force'
            result = subprocess.run(
                ["powershell", "-Command", powershell_cmd],
                capture_output=True, text=True, shell=True
            )
            if result.returncode == 0:
                logger.info("[Windows主机名] Rename-Computer 设置成功，需要重启后生效: {}", hostname)
            else:
                logger.error("[Windows主机名] 两种方式均设置失败: {}", result.stderr)

        # 更新hosts
        self.update_hosts(hostname)

    def set_password(self, username: str, password: str):
        """设置Windows用户密码"""
        logger.info("[Windows密码] 设置{}密码", username)
        result = self._run_cmd(["net", "user", username, password])
        if result.returncode == 0:
            logger.info("[Windows密码] {}密码设置成功", username)
        else:
            logger.error("[Windows密码] {}密码设置失败: {}", username, result.stderr)

    # ==================== 磁盘扩容 ====================

    def extend_disk(self):
        """Windows磁盘扩容"""
        logger.info("[磁盘扩容] 执行 Windows 磁盘扩容")
        os.system(
            'mshta vbscript:Execute("CreateObject(""WScript.Shell"").Run '
            '""cmd /c (echo select volume C&&echo extend)|diskpart"",0,True:close")'
        )
        logger.info("[磁盘扩容] Windows 磁盘扩容完成")

    # ==================== hosts ====================

    def _get_hosts_path(self) -> str:
        return r"C:\Windows\System32\drivers\etc\hosts"
