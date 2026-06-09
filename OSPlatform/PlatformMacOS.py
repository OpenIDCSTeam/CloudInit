import subprocess

from loguru import logger
from .PlatformBase import PlatformBase


class PlatformMacOS(PlatformBase):
    """macOS平台操作实现"""

    # ==================== 电源控制 ====================

    def shutdown(self):
        """macOS关机"""
        try:
            logger.info("[系统关机] 执行 macOS 关机命令")
            subprocess.run(["shutdown", "-h", "now"], check=True)
        except Exception as e:
            logger.error("[系统关机] 关机命令执行失败: {}", e)

    def reboot(self):
        """macOS重启"""
        try:
            logger.info("[系统重启] 执行 macOS 重启命令")
            subprocess.run(["shutdown", "-r", "now"], check=True)
        except Exception as e:
            logger.error("[系统重启] 重启命令执行失败: {}", e)

    # ==================== 主机配置 ====================

    def get_hostname(self) -> str:
        """获取当前主机名"""
        result = self._run_cmd(["scutil", "--get", "ComputerName"])
        if result.returncode == 0:
            return result.stdout.strip()
        # 备用
        result = self._run_cmd(["hostname"])
        return result.stdout.strip()

    def set_hostname(self, hostname: str):
        """设置macOS主机名（ComputerName + HostName + LocalHostName）"""
        current = self.get_hostname()
        if current == hostname:
            logger.info("[macOS主机名] 当前主机名已经是: {}，无需修改", hostname)
            return

        logger.info("[macOS主机名] 当前主机名: {}，需要修改为: {}", current, hostname)

        success = True
        # 设置 ComputerName
        result = self._run_cmd(["scutil", "--set", "ComputerName", hostname])
        if result.returncode != 0:
            logger.error("[macOS主机名] 设置ComputerName失败: {}", result.stderr)
            success = False

        # 设置 HostName
        result = self._run_cmd(["scutil", "--set", "HostName", hostname])
        if result.returncode != 0:
            logger.error("[macOS主机名] 设置HostName失败: {}", result.stderr)
            success = False

        # 设置 LocalHostName（去掉特殊字符）
        local_hostname = hostname.replace(" ", "-").replace(".", "-")
        result = self._run_cmd(["scutil", "--set", "LocalHostName", local_hostname])
        if result.returncode != 0:
            logger.error("[macOS主机名] 设置LocalHostName失败: {}", result.stderr)
            success = False

        if success:
            logger.info("[macOS主机名] 主机名设置成功: {}", hostname)
        else:
            logger.warning("[macOS主机名] 部分主机名设置失败")

        # 更新hosts
        self.update_hosts(hostname)

    def set_password(self, username: str, password: str):
        """设置macOS用户密码"""
        logger.info("[macOS密码] 设置{}密码", username)
        # macOS使用dscl修改密码
        result = self._run_cmd([
            "dscl", ".", "-passwd", f"/Users/{username}", password
        ])
        if result.returncode == 0:
            logger.info("[macOS密码] {}密码设置成功", username)
        else:
            logger.error("[macOS密码] {}密码设置失败: {}", username, result.stderr)

    # ==================== 磁盘扩容 ====================

    def extend_disk(self):
        """macOS磁盘扩容 - APFS自动扩容"""
        logger.info("[磁盘扩容] 执行 macOS 磁盘扩容")

        # 获取APFS容器信息
        result = self._run_cmd(["diskutil", "apfs", "list"])
        if result.returncode != 0:
            logger.error("[磁盘扩容] 获取APFS信息失败: {}", result.stderr)
            return

        # 尝试对根卷执行resize
        result = self._run_cmd(["diskutil", "apfs", "resizeContainer", "disk1", "0"])
        if result.returncode == 0:
            logger.info("[磁盘扩容] macOS APFS 扩容成功")
        else:
            logger.warning("[磁盘扩容] APFS扩容结果: {}", result.stderr)
            # 备用：尝试repairDisk
            result = self._run_cmd(["diskutil", "repairDisk", "disk0"])
            if result.returncode == 0:
                logger.info("[磁盘扩容] diskutil repairDisk 完成")
            else:
                logger.error("[磁盘扩容] macOS 磁盘扩容失败: {}", result.stderr)

    # ==================== hosts ====================

    def _get_hosts_path(self) -> str:
        return "/etc/hosts"
