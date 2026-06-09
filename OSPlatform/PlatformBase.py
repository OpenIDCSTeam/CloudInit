import subprocess
import threading
from abc import ABC, abstractmethod

from loguru import logger


class PlatformBase(ABC):
    """平台操作抽象基类，定义所有平台需要实现的操作接口"""

    # ==================== 电源控制 ====================

    @abstractmethod
    def shutdown(self):
        """关机"""
        pass

    @abstractmethod
    def reboot(self):
        """重启"""
        pass

    # ==================== 主机配置 ====================

    @abstractmethod
    def set_hostname(self, hostname: str):
        """设置主机名"""
        pass

    @abstractmethod
    def set_password(self, username: str, password: str):
        """设置用户密码"""
        pass

    @abstractmethod
    def get_hostname(self) -> str:
        """获取当前主机名"""
        pass

    # ==================== 磁盘扩容 ====================

    @abstractmethod
    def extend_disk(self):
        """磁盘扩容"""
        pass

    # ==================== hosts文件 ====================

    def update_hosts(self, hostname: str):
        """更新hosts文件（通用实现，子类可覆盖）"""
        hosts_path = self._get_hosts_path()
        try:
            hosts_entry = f"127.0.0.1\t{hostname}\n"

            with open(hosts_path, "r") as f:
                lines = f.readlines()

            found = False
            new_lines = []
            for line in lines:
                stripped = line.strip()
                if stripped and not stripped.startswith("#"):
                    parts = stripped.split()
                    if len(parts) >= 2 and parts[1].lower() == hostname.lower():
                        new_lines.append(hosts_entry)
                        found = True
                    else:
                        new_lines.append(line)
                else:
                    new_lines.append(line)

            if not found:
                for i, line in enumerate(new_lines):
                    stripped = line.strip()
                    if stripped and "127.0.0.1" in stripped and "localhost" in stripped.lower():
                        new_lines.insert(i + 1, hosts_entry)
                        break
                else:
                    new_lines.append(hosts_entry)

            with open(hosts_path, "w") as f:
                f.writelines(new_lines)

            logger.info("[hosts] hosts 文件更新成功: {}", hosts_path)

        except IOError as e:
            logger.error("[hosts] 读取或写入 hosts 文件失败: {}", e)
        except Exception as e:
            logger.error("[hosts] 更新 hosts 文件异常: {}", e)

    @abstractmethod
    def _get_hosts_path(self) -> str:
        """获取hosts文件路径"""
        pass

    # ==================== 非阻塞执行工具 ====================

    @staticmethod
    def run_async(func, *args, **kwargs):
        """非阻塞执行任务"""
        thread = threading.Thread(target=func, args=args, kwargs=kwargs, daemon=True)
        thread.start()
        return thread

    @staticmethod
    def _run_cmd(cmd: list, **kwargs) -> subprocess.CompletedProcess:
        """执行命令的通用封装"""
        return subprocess.run(cmd, capture_output=True, text=True, **kwargs)
