"""
ToDesk远程桌面管理模块
负责：
1. Windows启动时运行ToDesk.exe（不阻塞）
2. 定期执行ToDeskSunDump.exe -a 获取设备代码和临时密码
3. 将获取到的信息提供给上报模块
"""
from __future__ import annotations

import os
import re
import sys
import subprocess
import threading
import time
import platform
from typing import Optional

from loguru import logger


class ToDeskManager:
    """ToDesk远程桌面管理器"""

    def __init__(self):
        # ToDesk信息
        self.device_code: str = ""  # 设备代码
        self.temp_password: str = ""  # 临时密码
        self._running = False
        self._thread: Optional[threading.Thread] = None
        # 定期刷新间隔（秒）
        self._refresh_interval = 120  # 每2分钟刷新一次

        # 获取exe所在目录（打包后和开发环境统一处理）
        if getattr(sys, 'frozen', False):
            self._base_dir = os.path.dirname(sys.executable)
        else:
            self._base_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
        # ToDesk目录
        self._toDesk_dir = os.path.join(self._base_dir, "ToDesk_4.6.0.1")
        # ToDeskSunDump.exe路径（与exe同目录）
        self._dump_exe = os.path.join(self._base_dir, "ToDeskSunDump.exe")

    def start(self):
        """启动ToDesk管理（仅Windows平台）"""
        if platform.system() != "Windows":
            logger.info("[ToDesk] 非Windows平台，跳过ToDesk管理")
            return

        # 启动ToDesk.exe（不阻塞）
        self._start_toDesk()

        # 启动定期刷新线程
        self._running = True
        self._thread = threading.Thread(target=self._refresh_loop, daemon=True)
        self._thread.start()
        logger.info("[ToDesk] ToDesk管理已启动，刷新间隔: {}s", self._refresh_interval)

    def stop(self):
        """停止ToDesk管理"""
        self._running = False
        if self._thread:
            self._thread.join(timeout=5)

    def get_toDesk_info(self) -> dict:
        """获取ToDesk连接信息，用于上报"""
        if self.device_code and self.temp_password:
            return {
                "code": self.device_code,
                "password": self.temp_password,
            }
        return {}

    def _start_toDesk(self):
        """启动ToDesk.exe（不阻塞主进程）"""
        toDesk_exe = os.path.join(self._toDesk_dir, "ToDesk.exe")
        if not os.path.exists(toDesk_exe):
            logger.warning("[ToDesk] ToDesk.exe不存在: {}", toDesk_exe)
            return

        try:
            # 使用subprocess.Popen启动，不等待进程结束
            subprocess.Popen(
                [toDesk_exe],
                cwd=self._toDesk_dir,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                creationflags=subprocess.CREATE_NO_WINDOW
                if hasattr(subprocess, "CREATE_NO_WINDOW")
                else 0,
            )
            logger.info("[ToDesk] ToDesk.exe已启动: {}", toDesk_exe)
        except Exception as e:
            logger.error("[ToDesk] 启动ToDesk.exe失败: {}", e)

    def _refresh_loop(self):
        """定期刷新ToDesk设备代码和临时密码"""
        # 首次启动等待10秒让ToDesk完全启动
        time.sleep(10)

        while self._running:
            try:
                self._fetch_toDesk_info()
            except Exception as e:
                logger.error("[ToDesk] 获取ToDesk信息异常: {}", e)

            # 等待下一次刷新
            for _ in range(self._refresh_interval):
                if not self._running:
                    return
                time.sleep(1)

    def _fetch_toDesk_info(self):
        """执行ToDeskSunDump.exe -a 获取设备代码和临时密码"""
        if not os.path.exists(self._dump_exe):
            logger.warning("[ToDesk] ToDeskSunDump.exe不存在: {}", self._dump_exe)
            return

        try:
            result = subprocess.run(
                [self._dump_exe, "-a"],
                capture_output=True,
                text=True,
                timeout=30,
                cwd=os.path.dirname(self._dump_exe),
            )

            if result.returncode != 0:
                logger.warning("[ToDesk] ToDeskSunDump.exe执行失败，返回码: {}", result.returncode)
                return

            output = result.stdout
            if not output:
                logger.warning("[ToDesk] ToDeskSunDump.exe无输出")
                return

            # 解析输出，提取设备代码和临时密码
            # 格式示例:
            # 设备代码: 950962358
            # 临时密码: 4xgpgy04
            device_code = ""
            temp_password = ""

            for line in output.splitlines():
                line = line.strip()
                # 匹配设备代码
                match = re.match(r"设备代码[:：]\s*(.+)", line)
                if match:
                    device_code = match.group(1).strip()
                    continue
                # 匹配临时密码
                match = re.match(r"临时密码[:：]\s*(.+)", line)
                if match:
                    temp_password = match.group(1).strip()
                    continue

            if device_code and temp_password:
                self.device_code = device_code
                self.temp_password = temp_password
                logger.info("[ToDesk] 设备代码: {} | 临时密码: {}", device_code, temp_password)
            else:
                logger.warning("[ToDesk] 未能从输出中解析到设备代码或临时密码")
                logger.debug("[ToDesk] 原始输出:\n{}", output)

        except subprocess.TimeoutExpired:
            logger.error("[ToDesk] ToDeskSunDump.exe执行超时")
        except Exception as e:
            logger.error("[ToDesk] 执行ToDeskSunDump.exe异常: {}", e)
