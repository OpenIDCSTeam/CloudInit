"""
自动更新模块 - 定期从GitHub拉取最新tag版本并热更新

工作流程：
1. 后台守护线程每24h（可配置）检查一次GitHub Release
2. 发现新版本后下载对应平台的二进制文件
3. 替换当前可执行文件并重启服务
"""
from __future__ import annotations

import os
import sys
import time
import platform
import subprocess
import threading
from typing import Optional

import requests
from loguru import logger

from .CoreConfig import config

# GitHub下载代理列表，按优先级顺序尝试
GITHUB_PROXIES = [
    "https://github.com/",
    "https://github.524228.xyz/",
    "https://ghfast.top/https://github.com/",
    "https://ghproxy.net/https://github.com/",
    "https://gh-proxy.org/https://github.com/",
]

# GitHub API代理列表，按优先级顺序尝试
GITHUB_API_PROXIES = [
    "https://api.github.com/",
    "https://api.github.524228.xyz/",
    "https://ghfast.top/https://api.github.com/",
    "https://ghproxy.net/https://api.github.com/",
    "https://gh-proxy.org/https://api.github.com/",
]


class AutoUpdate:
    """
    自动更新服务

    通过后台守护线程定期检查GitHub最新Release，
    发现新版本时自动下载、替换、重启，全程不阻塞主服务。
    """

    def __init__(self):
        self._running = False
        self._thread: Optional[threading.Thread] = None

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
        """
        版本文件路径。
        PyInstaller打包后基于可执行文件所在目录，
        开发环境下基于项目根目录。
        """
        if getattr(sys, 'frozen', False):
            # PyInstaller 打包环境：使用可执行文件所在目录
            base_dir = os.path.dirname(sys.executable)
        else:
            # 开发环境：使用项目根目录
            base_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
        return os.path.join(base_dir, ".version")

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
        if not latest_version:
            return

        # 忽略 beta/prerelease 版本
        if latest_info.get("prerelease", False):
            logger.debug("[自动更新] 最新版本({})为预发布版本，跳过", latest_version)
            return

        # 版本相同则跳过
        if latest_version == current_version:
            logger.debug("[自动更新] 当前版本({})已是最新", current_version)
            return

        # 语义化版本比较，确保远程版本确实更新
        if not self._is_newer_version(latest_version, current_version):
            logger.debug("[自动更新] 远程版本({})不高于当前版本({})，跳过",
                         latest_version, current_version)
            return

        logger.info("[自动更新] 发现新版本: {} -> {}", current_version, latest_version)
        self._download_and_update(latest_info)

    @staticmethod
    def _is_newer_version(remote: str, local: str) -> bool:
        """
        语义化版本比较，判断远程版本是否高于本地版本。

        支持格式: v1.2.3, 1.2.3, v1.0, 1.0 等
        如果本地版本为 "unknown" 则认为远程版本更新。
        无法解析时回退为字符串不等判断。
        """
        if local == "unknown":
            return True

        def parse_version(ver: str) -> list:
            """将版本字符串解析为数字列表，如 'v1.2.3' -> [1, 2, 3]"""
            ver = ver.strip().lstrip("vV")
            parts = []
            for p in ver.split("."):
                try:
                    parts.append(int(p))
                except ValueError:
                    # 处理类似 "3-rc1" 的情况，取数字部分
                    digits = ""
                    for ch in p:
                        if ch.isdigit():
                            digits += ch
                        else:
                            break
                    parts.append(int(digits) if digits else 0)
            return parts

        try:
            remote_parts = parse_version(remote)
            local_parts = parse_version(local)

            # 补齐长度，短的补0
            max_len = max(len(remote_parts), len(local_parts))
            remote_parts.extend([0] * (max_len - len(remote_parts)))
            local_parts.extend([0] * (max_len - len(local_parts)))

            return remote_parts > local_parts
        except Exception:
            # 解析失败，回退为简单不等判断
            return remote != local

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
        """
        从GitHub API获取最新正式Release信息，按代理列表依次尝试。
        使用 /releases 接口获取列表，过滤掉 prerelease 和 beta tag，
        返回第一个正式版本。
        """
        # 将 /releases/latest 替换为 /releases 以获取完整列表用于过滤
        repo_url = self.repo_url.replace("/releases/latest", "/releases")

        for proxy in GITHUB_API_PROXIES:
            # 将原始URL中的 https://api.github.com/ 替换为代理地址
            if repo_url.startswith("https://api.github.com/"):
                url = repo_url.replace("https://api.github.com/", proxy, 1)
            else:
                url = repo_url

            try:
                logger.debug("[自动更新] 尝试API: {}", url)
                response = requests.get(url, timeout=30)
                if response.status_code == 200:
                    logger.debug("[自动更新] API请求成功 (via {})", proxy.rstrip("/"))
                    releases = response.json()
                    # 如果返回的是列表，过滤出第一个非prerelease且非beta的版本
                    if isinstance(releases, list):
                        for release in releases:
                            tag = release.get("tag_name", "").lower()
                            if release.get("prerelease", False):
                                continue
                            if release.get("draft", False):
                                continue
                            if "beta" in tag or "alpha" in tag or "rc" in tag:
                                continue
                            return release
                        return {}  # 没有找到正式版本
                    # 兼容直接返回单个release的情况
                    return releases
                logger.debug("[自动更新] HTTP {} - 切换下一个API代理", response.status_code)
            except requests.exceptions.Timeout:
                logger.debug("[自动更新] API超时 - 切换下一个代理")
            except requests.exceptions.ConnectionError:
                logger.debug("[自动更新] API连接失败 - 切换下一个代理")
            except Exception as e:
                logger.debug("[自动更新] API请求异常: {} - 切换下一个代理", e)

        logger.warning("[自动更新] 所有API代理均无法获取Release信息")
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
            # 通过代理列表尝试下载
            response = self._download_with_proxy(download_url, temp_path)
            if not response:
                logger.error("[自动更新] 所有下载渠道均失败")
                return

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

    def _download_with_proxy(self, original_url: str, save_path: str) -> bool:
        """
        按代理优先级顺序尝试下载文件

        GitHub的下载URL格式为: https://github.com/owner/repo/releases/download/tag/file
        代理替换规则: 将 https://github.com/ 替换为代理前缀

        Args:
            original_url: 原始GitHub下载地址
            save_path: 本地保存路径

        Returns:
            True 下载成功，False 所有代理均失败
        """
        for proxy in GITHUB_PROXIES:
            # 将原始URL中的 https://github.com/ 替换为代理地址
            if original_url.startswith("https://github.com/"):
                url = original_url.replace("https://github.com/", proxy, 1)
            else:
                url = original_url

            try:
                logger.info("[自动更新] 尝试下载: {}", url)
                response = requests.get(url, timeout=60, stream=True)
                if response.status_code != 200:
                    logger.debug("[自动更新] HTTP {} - 切换下一个代理", response.status_code)
                    continue

                # 流式写入文件
                with open(save_path, "wb") as f:
                    for chunk in response.iter_content(chunk_size=8192):
                        if chunk:
                            f.write(chunk)

                logger.info("[自动更新] 下载成功 (via {})", proxy.rstrip("/"))
                return True

            except requests.exceptions.Timeout:
                logger.debug("[自动更新] 超时 - 切换下一个代理")
            except requests.exceptions.ConnectionError:
                logger.debug("[自动更新] 连接失败 - 切换下一个代理")
            except Exception as e:
                logger.debug("[自动更新] 下载异常: {} - 切换下一个代理", e)

            # 清理可能写了一半的文件
            self._cleanup_temp(save_path)

        return False

    @staticmethod
    def _is_legacy_windows() -> bool:
        """判断当前Windows是否为旧版本（Win7/Win8/Win8.1）"""
        if platform.system().lower() != "windows":
            return False
        try:
            # Windows版本号: Win7=6.1, Win8=6.2, Win8.1=6.3, Win10+=10.0
            ver = platform.version()  # 如 "6.1.7601" 或 "10.0.19041"
            major = int(ver.split(".")[0])
            return major < 10
        except (ValueError, IndexError):
            return False

    @staticmethod
    def _get_arch_tag() -> str:
        """
        获取当前机器架构标识。
        返回 "x64"、"x86" 或 "a64"（ARM64）。
        """
        machine = platform.machine().lower()
        if machine in ("aarch64", "arm64"):
            return "a64"
        if machine in ("i386", "i686", "x86"):
            return "x86"
        return "x64"

    @staticmethod
    def _find_platform_asset(assets: list, system: str) -> Optional[dict]:
        """
        根据当前平台和架构匹配对应的Release资产。

        命名规则: CloudInit-<系统>-<架构>.exe
        匹配优先级:
          1. 精确匹配: os_tag + arch（如 CloudInit-linux-x64）
          2. 默认别名: os_tag 且不含任何架构后缀（如 CloudInit-linux，默认为x64）
          3. 宽松匹配: 只要包含 os_tag 的任意资产
        Windows额外区分Win7/8和Win10/11。
        """
        arch = AutoUpdate._get_arch_tag()
        # 所有已知架构标识，用于判断资产是否带架构后缀
        all_arch_tags = ("x64", "x86", "a64")

        if system == "windows":
            is_legacy = AutoUpdate._is_legacy_windows()
            os_tag = "win78" if is_legacy else "win10"

            # 优先匹配: CloudInit-win10-x64 或 CloudInit-win78-a64 等
            for asset in assets:
                name = asset.get("name", "").lower()
                if os_tag in name and arch in name:
                    return asset

            # 回退: CloudInit-win10 或 CloudInit-win78（不带任何架构后缀，默认x64）
            # 仅当当前架构为x64时才使用默认别名（因为默认就是x64）
            if arch == "x64":
                for asset in assets:
                    name = asset.get("name", "").lower()
                    if os_tag in name and not any(a in name for a in all_arch_tags):
                        return asset

            # 再回退: 只要包含对应os_tag的任意资产（优先选不带架构的）
            for asset in assets:
                name = asset.get("name", "").lower()
                if os_tag in name and not any(a in name for a in all_arch_tags):
                    return asset
            for asset in assets:
                name = asset.get("name", "").lower()
                if os_tag in name:
                    return asset

            # Win7/8找不到时尝试通用Windows包
            if is_legacy:
                logger.warning("[自动更新] 未找到Win7/8专用更新包(win78)，尝试使用通用Windows包")
                for asset in assets:
                    name = asset.get("name", "").lower()
                    if ("win10" in name or "windows" in name) and "win78" not in name:
                        return asset
        else:
            os_tag_map = {
                "linux": "linux",
                "darwin": "macos",
            }
            os_tag = os_tag_map.get(system, system)

            # 优先匹配: CloudInit-linux-x64 或 CloudInit-macos-a64 等
            for asset in assets:
                name = asset.get("name", "").lower()
                if os_tag in name and arch in name:
                    return asset

            # 回退: CloudInit-linux 或 CloudInit-macos（不带任何架构后缀，默认x64）
            # 仅当当前架构为x64时才使用默认别名（因为默认就是x64）
            if arch == "x64":
                for asset in assets:
                    name = asset.get("name", "").lower()
                    if os_tag in name and not any(a in name for a in all_arch_tags):
                        return asset

            # 再回退: 只要包含对应os_tag的任意资产（优先选不带架构的）
            for asset in assets:
                name = asset.get("name", "").lower()
                if os_tag in name and not any(a in name for a in all_arch_tags):
                    return asset
            for asset in assets:
                name = asset.get("name", "").lower()
                if os_tag in name:
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
        """Windows系统：生成批处理脚本，用PID精确终止当前进程后替换文件并重启"""
        current_pid = os.getpid()
        batch_content = (
            '@echo off\n'
            f'taskkill /F /PID {current_pid} >nul 2>&1\n'
            'timeout /t 3 /nobreak >nul\n'
            # 循环等待文件可删除（进程可能还未完全退出）
            ':retry\n'
            f'del /F "{exe_path}" >nul 2>&1\n'
            f'if exist "{exe_path}" (\n'
            '    timeout /t 2 /nobreak >nul\n'
            '    goto retry\n'
            ')\n'
            f'move /Y "{temp_path}" "{exe_path}"\n'
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
                # 使用Popen异步发起重启，然后立即退出当前进程
                # systemd会负责kill当前进程并启动新版本
                subprocess.Popen(
                    ["systemctl", "restart", "ServerInit"],
                    stdout=subprocess.DEVNULL,
                    stderr=subprocess.DEVNULL,
                )
                sys.exit(0)
            elif system == "windows":
                logger.info("[自动更新] 通过批处理重启...")
                exe_path = AutoUpdate._get_executable_path()
                batch_path = os.path.join(os.path.dirname(exe_path), "_update.bat")
                if os.path.exists(batch_path):
                    # 使用CREATE_NEW_PROCESS_GROUP确保批处理独立于当前进程
                    flags = subprocess.CREATE_NO_WINDOW | subprocess.CREATE_NEW_PROCESS_GROUP
                    subprocess.Popen(
                        ["cmd.exe", "/c", batch_path],
                        creationflags=flags,
                        close_fds=True,
                    )
                    # 强制退出当前进程，让批处理能删除exe
                    os._exit(0)
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