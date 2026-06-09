import os
import re
import subprocess

from loguru import logger
from .PlatformBase import PlatformBase


class PlatformLinux(PlatformBase):
    """Linux平台操作实现"""

    # ==================== 电源控制 ====================

    def shutdown(self):
        """Linux关机"""
        try:
            logger.info("[系统关机] 执行 Linux 关机命令")
            subprocess.run(["shutdown", "-h", "now"], check=True)
        except Exception as e:
            logger.error("[系统关机] 关机命令执行失败: {}", e)

    def reboot(self):
        """Linux重启"""
        try:
            logger.info("[系统重启] 执行 Linux 重启命令")
            subprocess.run(["reboot"], check=True)
        except Exception as e:
            logger.error("[系统重启] 重启命令执行失败: {}", e)

    # ==================== 主机配置 ====================

    def get_hostname(self) -> str:
        """获取当前主机名"""
        result = self._run_cmd(["hostname"])
        return result.stdout.strip()

    def set_hostname(self, hostname: str):
        """设置Linux主机名"""
        current = self.get_hostname()
        if current == hostname:
            logger.info("[Linux主机名] 当前主机名已经是: {}，无需修改", hostname)
            return

        logger.info("[Linux主机名] 当前主机名: {}，需要修改为: {}", current, hostname)
        result = self._run_cmd(["hostnamectl", "set-hostname", hostname])
        if result.returncode == 0:
            logger.info("[Linux主机名] 主机名设置成功: {}", hostname)
        else:
            # 备用方式：直接写文件
            try:
                with open("/etc/hostname", "w") as f:
                    f.write(hostname + "\n")
                subprocess.run(["hostname", hostname], check=True)
                logger.info("[Linux主机名] 传统方式设置成功: {}", hostname)
            except Exception as e:
                logger.error("[Linux主机名] 设置失败: {}", e)

        # 更新hosts
        self.update_hosts(hostname)

    def set_password(self, username: str, password: str):
        """设置Linux用户密码"""
        logger.info("[Linux密码] 设置{}密码", username)
        process = subprocess.Popen(
            ["chpasswd"], stdin=subprocess.PIPE,
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True
        )
        _, stderr = process.communicate(input=f"{username}:{password}")
        if process.returncode == 0:
            logger.info("[Linux密码] {}密码设置成功", username)
        else:
            logger.error("[Linux密码] {}密码设置失败: {}", username, stderr)

    # ==================== 磁盘扩容 ====================

    def extend_disk(self):
        """Linux智能磁盘扩容 - 自动检测根分区并扩容"""
        logger.info("[磁盘扩容] 执行 Linux 磁盘扩容")

        root_device = self._get_root_partition()
        if not root_device:
            logger.error("[磁盘扩容] 无法检测根分区设备")
            return

        logger.info("[磁盘扩容] 检测到根分区: {}", root_device)

        # 解析磁盘和分区号
        disk, part_num = self._parse_partition(root_device)
        if not disk or not part_num:
            logger.error("[磁盘扩容] 无法解析分区信息: {}", root_device)
            return

        logger.info("[磁盘扩容] 磁盘: {}, 分区号: {}", disk, part_num)

        # 执行 growpart 扩展分区
        result = self._run_cmd(["growpart", disk, str(part_num)])
        if result.returncode == 0:
            logger.info("[磁盘扩容] growpart 扩展分区成功")
        elif "NOCHANGE" in result.stdout or "NOCHANGE" in result.stderr:
            logger.info("[磁盘扩容] 分区已是最大，无需扩展")
        else:
            logger.warning("[磁盘扩容] growpart 执行结果: {} {}", result.stdout, result.stderr)

        # 检测文件系统类型并执行对应的扩容命令
        fs_type = self._get_fs_type(root_device)
        logger.info("[磁盘扩容] 文件系统类型: {}", fs_type)

        if fs_type in ("ext2", "ext3", "ext4"):
            result = self._run_cmd(["resize2fs", root_device])
            if result.returncode == 0:
                logger.info("[磁盘扩容] resize2fs 扩容成功")
            else:
                logger.error("[磁盘扩容] resize2fs 失败: {}", result.stderr)
        elif fs_type == "xfs":
            result = self._run_cmd(["xfs_growfs", "/"])
            if result.returncode == 0:
                logger.info("[磁盘扩容] xfs_growfs 扩容成功")
            else:
                logger.error("[磁盘扩容] xfs_growfs 失败: {}", result.stderr)
        elif fs_type == "btrfs":
            result = self._run_cmd(["btrfs", "filesystem", "resize", "max", "/"])
            if result.returncode == 0:
                logger.info("[磁盘扩容] btrfs 扩容成功")
            else:
                logger.error("[磁盘扩容] btrfs 扩容失败: {}", result.stderr)
        else:
            logger.warning("[磁盘扩容] 不支持的文件系统类型: {}", fs_type)

    # ==================== hosts ====================

    def _get_hosts_path(self) -> str:
        return "/etc/hosts"

    # ==================== 内部方法 ====================

    def _get_root_partition(self) -> str:
        """获取根分区设备路径"""
        try:
            result = self._run_cmd(["findmnt", "-n", "-o", "SOURCE", "/"])
            if result.returncode == 0 and result.stdout.strip():
                source = result.stdout.strip()
                if "/mapper/" in source:
                    logger.info("[磁盘扩容] 检测到LVM设备: {}，尝试扩展LV", source)
                    self._run_cmd(["lvextend", "-l", "+100%FREE", source])
                return source

            with open("/proc/mounts", "r") as f:
                for line in f:
                    parts = line.split()
                    if len(parts) >= 2 and parts[1] == "/":
                        return parts[0]
        except Exception as e:
            logger.error("[磁盘扩容] 获取根分区失败: {}", e)
        return ""

    @staticmethod
    def _parse_partition(device: str) -> tuple:
        """解析分区设备为磁盘路径和分区号"""
        # NVMe设备: /dev/nvme0n1p2
        match = re.match(r'^(/dev/nvme\d+n\d+)p(\d+)$', device)
        if match:
            return match.group(1), int(match.group(2))
        # 普通设备: /dev/sda2, /dev/vda1
        match = re.match(r'^(/dev/[a-z]+)(\d+)$', device)
        if match:
            return match.group(1), int(match.group(2))
        return None, None

    def _get_fs_type(self, device: str) -> str:
        """获取设备的文件系统类型"""
        try:
            result = self._run_cmd(["blkid", "-s", "TYPE", "-o", "value", device])
            if result.returncode == 0:
                return result.stdout.strip()
        except Exception as e:
            logger.error("[磁盘扩容] 获取文件系统类型失败: {}", e)
        return ""
