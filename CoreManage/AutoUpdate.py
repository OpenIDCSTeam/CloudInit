"""
自动更新模块 - 定期从GitHub拉取最新tag版本并热更新

工作流程：
1. 后台守护线程每24h（可配置）检查一次GitHub Release
2. 发现新版本后下载对应平台的二进制文件
3. 替换当前可执行文件并重启服务
"""

import os
import sys
import time
import platform
import subprocess
import threading

import requests
from loguru import logger

from .CoreConfig import config


class AutoUpdate:
    """
    自动更新服务

    通过后台守护线程定期检查GitHub最新Release，
    发现新版本时自动下载、替换、重启，全程不阻塞主服务。
    """

    def __init__(self):
        self._running = False
        self._thread: threading.Thread | None = None

    @property
    def repo_url(self) -> str:
        """获取更新仓库地址（支持配置覆盖）"""
        return config.update_repo

    @property
    def check_interval(self) -> int:
        """获取检查间隔（支持配置覆盖）"""
        return config.update_interval

    @property
    def version_file(self) -> str:
        """版本文件路径"""
        return os.path.join(
            os.path.dirname(os.path.dirname(os.path.abspath(__file__))), ".version"
        )

    def start(self):
        """启动自动更新检查线程"""
        self._running = True
        self._thread = threading.Thread(
            target=self._update_loop, daemon=True, name="AutoUpdate"
        )
        self._thread.start()
        logger.info("[自动更新] 后台检查已启动，周期: {}h", self.check_interval // 3600)

    def stop(self):
        """停止自动更新检查"""
        self._running = False
        if self._thread and self._thread.is_alive():
            self._thread.join(timeout=5)

    def _update_loop(self):
        """更新检查主循环（运行在后台线程）"""
        while self._running:
            try:
                self._check_and_update()
            except Exception as e:
                logger.error("[自动更新] 本轮检查异常（将在下个周期重试）: {}", e)

            # 可中断的等待，每秒检查一次退出标志
            for _ in range(self.check_interval):
                if not self._running:
                    return
                time.sleep(1)

    def _check_and_update(self):
        """检查远程版本并决定是否更新"""
        current_version = self._get_current_version()
        latest_info = self._get_latest_release()

        if not latest_info:
            return

        latest_version = latest_info.get("tag_name", "")
        if not latest_version or latest_version == current_version:
            logger.debug("[自动更新] 当前版本({})已是最新", current_version)
            return

        logger.info("[自动更新] 发现新版本: {} -> {}", current_version, latest_version)
        self._download_and_update(latest_info)

    def _get_current_version(self) -> str:
        """读取本地版本号文件"""
        try:
            if os.path.exists(self.version_file):
                with open(self.version_file, "r", encoding="utf-8") as f:
                    return f.read().strip()
        except (IOError, OSError) as e:
            logger.warning("[自动更新] 版本文件读取失败: {}", e)
        return "unknown"

    def _save_current_version(self, version: str):
        """保存版本号到本地文件"""
        try:
            with open(self.version_file, "w", encoding="utf-8") as f:
                f.write(version)
        except (IOError, OSError) as e:
            logger.error("[自动更新] 版本文件保存失败: {}", e)

    def _get_latest_release(self) -> dict:
        """从GitHub API获取最新Release信息"""
        try:
            response = requests.get(self.repo_url, timeout=30)
            if response.status_code == 200:
                return response.json()
            logger.debug("[自动更新] GitHub API 返回 HTTP {}", response.status_code)
        except requests.exceptions.Timeout:
            logger.debug("[自动更新] GitHub请求超时")
        except requests.exceptions.ConnectionError:
            logger.debug("[自动更新] 无法连接GitHub（网络不可达）")
        except Exception as e:
            logger.warning("[自动更新] 获取Release信息异常: {}", e)
        return {}

    def _download_and_update(self, release_info: dict):
        """下载新版本并执行热更新"""
        system = platform.system().lower()
        assets = release_info.get("assets", [])
        tag_name = release_info.get("tag_name", "")

        # 匹配当前平台的资产文件
        target_asset = self._find_platform_asset(assets, system)
        if not target_asset:
            logger.warning("[自动更新] 未找到平台({})对应的更新文件", system)
            return

        download_url = target_asset.get("browser_download_url", "")
        if not download_url:
            logger.error("[自动更新] 资产文件缺少下载地址")
            return

        exe_path = self._get_executable_path()
        temp_path = exe_path + ".update"

        try:
            # 流式下载，避免大文件占用过多内存
            logger.info("[自动更新] 下载中: {}", download_url)
            response = requests.get(download_url, timeout=300, stream=True)
            if response.status_code != 200:
                logger.error("[自动更新] 下载失败 HTTP {}", response.status_code)
                return

            with open(temp_path, "wb") as f:
                for chunk in response.iter_content(chunk_size=8192):
                    if chunk:
                        f.write(chunk)

            logger.info("[自动更新] 下载完成，执行替换...")

            # 按平台执行文件替换
            if system in ("linux", "darwin"):
                self._replace_unix(exe_path, temp_path)
            elif system == "windows":
                self._replace_windows(exe_path, temp_path)
            else:
                logger.warning("[自动更新] 不支持的平台: {}", system)
                return

            # 记录新版本号
            self._save_current_version(tag_name)
            logger.info("[自动更新] 已更新至: {}", tag_name)

            # 重启服务使新版本生效
            self._restart_service(system)

        except requests.exceptions.RequestException as e:
            logger.error("[自动更新] 下载过程网络异常: {}", e)
            self._cleanup_temp(temp_path)
        except (IOError, OSError) as e:
            logger.error("[自动更新] 文件操作异常: {}", e)
            self._cleanup_temp(temp_path)
        except Exception as e:
            logger.error("[自动更新] 更新过程未知异常: {}", e)
            self._cleanup_temp(temp_path)

    @staticmethod
    def _find_platform_asset(assets: list, system: str) -> dict | None:
        """根据当前平台关键词匹配对应的Release资产"""
        keywords_map = {
            "linux": ("linux", "serverinit"),
            "darwin": ("darwin", "macos", "osx"),
            "windows": ("windows", "cloudinit", "win"),
        }
        keywords = keywords_map.get(system, ())
        for asset in assets:
            name = asset.get("name", "").lower()
            if any(kw in name for kw in keywords):
                return asset
        # 兜底：仅有一个资产时直接使用
        return assets[0] if len(assets) == 1 else None

    @staticmethod
    def _replace_unix(exe_path: str, temp_path: str):
        """Unix系统（Linux/macOS）文件替换"""
        backup_path = exe_path + ".bak"
        if os.path.exists(backup_path):
            os.remove(backup_path)
        if os.path.exists(exe_path):
            os.rename(exe_path, backup_path)
        os.rename(temp_path, exe_path)
        os.chmod(exe_path, 0o755)

    @staticmethod
    def _replace_windows(exe_path: str, temp_path: str):
        """Windows系统：生成批处理脚本延迟替换"""
        batch_content = (
            '@echo off\n'
            'timeout /t 2 /nobreak >nul\n'
            f'del "{exe_path}"\n'
            f'move "{temp_path}" "{exe_path}"\n'
            f'start "" "{exe_path}"\n'
            'del "%~f0"\n'
        )
        batch_path = os.path.join(os.path.dirname(exe_path), "_update.bat")
        with open(batch_path, "w", encoding="utf-8") as f:
            f.write(batch_content)

    @staticmethod
    def _restart_service(system: str):
        """重启服务使新版本生效"""
        try:
            if system in ("linux", "darwin"):
                logger.info("[自动更新] 通过systemctl重启服务...")
                subprocess.run(
                    ["systemctl", "restart", "ServerInit"],
                    capture_output=True, text=True
                )
            elif system == "windows":
                logger.info("[自动更新] 通过批处理重启...")
                exe_path = AutoUpdate._get_executable_path()
                batch_path = os.path.join(os.path.dirname(exe_path), "_update.bat")
                if os.path.exists(batch_path):
                    flags = getattr(subprocess, 'CREATE_NO_WINDOW', 0)
                    subprocess.Popen([batch_path], shell=True, creationflags=flags)
                    sys.exit(0)
        except Exception as e:
            logger.error("[自动更新] 重启服务失败: {}", e)

    @staticmethod
    def _cleanup_temp(temp_path: str):
        """清理下载的临时文件"""
        try:
            if os.path.exists(temp_path):
                os.remove(temp_path)
        except OSError:
            pass

    @staticmethod
    def _get_executable_path() -> str:
        """获取当前可执行文件的绝对路径"""
        if getattr(sys, 'frozen', False):
            # PyInstaller 打包环境
            return sys.executable
        return os.path.abspath(sys.argv[0])