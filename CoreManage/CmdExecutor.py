"""
CmdExecutor - 远程命令执行模块
接收并执行来自 HostAgent 的命令，记录日志，回传执行结果

工作流程：
1. 主循环上报状态时，HostAgent 在响应中携带 vm_cmd 字段
2. CmdExecutor 解析命令，在后台线程中非阻塞执行
3. 执行完成后，将结果通过 HTTP POST 回传给 HostAgent
4. HostAgent 存储结果并生成系统事件
"""

import subprocess
import threading
import time
import platform

import requests
from loguru import logger


class CmdExecutor:
    """远程命令执行器"""

    def __init__(self):
        self._lock = threading.Lock()

    def execute_command(self, cmd_data: dict, report_url: str):
        """
        非阻塞执行来自 HostAgent 的命令

        Args:
            cmd_data: 命令数据字典，包含:
                - cmd_id: 命令唯一标识
                - command: 要执行的命令字符串
                - timeout: 超时时间（秒），默认60
                - shell: 是否使用shell执行，默认True
            report_url: 结果回传的基础URL（HostAgent地址）
        """
        if not cmd_data or not cmd_data.get("command"):
            logger.warning("[命令执行] 收到空命令，跳过")
            return

        cmd_id = cmd_data.get("cmd_id", "")
        command = cmd_data.get("command", "")
        timeout = cmd_data.get("timeout", 60)

        logger.info("[命令执行] 收到命令 [{}]: {}", cmd_id, command)

        # 非阻塞执行
        threading.Thread(
            target=self._do_execute,
            args=(cmd_id, command, timeout, report_url),
            daemon=True,
            name=f"CmdExec-{cmd_id[:8]}"
        ).start()

    def _do_execute(self, cmd_id: str, command: str, timeout: int, report_url: str):
        """
        实际执行命令（后台线程）

        Args:
            cmd_id: 命令唯一标识
            command: 要执行的命令
            timeout: 超时时间（秒）
            report_url: 结果回传URL
        """
        start_time = time.time()
        result = {
            "cmd_id": cmd_id,
            "command": command,
            "exit_code": -1,
            "stdout": "",
            "stderr": "",
            "success": False,
            "duration": 0,
            "platform": platform.system(),
        }

        try:
            logger.info("[命令执行] 开始执行 [{}]: {}", cmd_id, command)

            # 根据平台选择shell
            system = platform.system().lower()
            if system == "windows":
                shell_exec = True
            else:
                shell_exec = True

            # 执行命令
            proc = subprocess.run(
                command,
                shell=shell_exec,
                capture_output=True,
                text=True,
                timeout=timeout,
            )

            result["exit_code"] = proc.returncode
            result["stdout"] = proc.stdout[-4096:] if len(proc.stdout) > 4096 else proc.stdout
            result["stderr"] = proc.stderr[-2048:] if len(proc.stderr) > 2048 else proc.stderr
            result["success"] = proc.returncode == 0

            if proc.returncode == 0:
                logger.info("[命令执行] 命令 [{}] 执行成功, 退出码: 0", cmd_id)
            else:
                logger.warning("[命令执行] 命令 [{}] 执行完成, 退出码: {}", cmd_id, proc.returncode)

            if proc.stdout.strip():
                logger.debug("[命令执行] stdout: {}", proc.stdout[:500])
            if proc.stderr.strip():
                logger.debug("[命令执行] stderr: {}", proc.stderr[:500])

        except subprocess.TimeoutExpired:
            result["stderr"] = f"命令执行超时（{timeout}秒）"
            result["exit_code"] = -2
            logger.error("[命令执行] 命令 [{}] 执行超时（{}秒）", cmd_id, timeout)

        except Exception as e:
            result["stderr"] = str(e)
            result["exit_code"] = -3
            logger.error("[命令执行] 命令 [{}] 执行异常: {}", cmd_id, e)

        finally:
            result["duration"] = round(time.time() - start_time, 2)

        # 回传结果给 HostAgent
        self._report_result(result, report_url)

    def _report_result(self, result: dict, report_url: str):
        """
        将命令执行结果回传给 HostAgent

        Args:
            result: 执行结果字典
            report_url: HostAgent 的基础URL
        """
        if not report_url:
            logger.warning("[命令执行] 无回传地址，结果仅记录本地日志")
            return

        # 构建回传URL: /api/client/cmd_result
        # report_url 格式为 http://x.x.x.x:1880
        callback_url = f"{report_url}/api/client/cmd_result"

        try:
            logger.info("[命令执行] 回传结果到: {}", callback_url)
            resp = requests.post(
                url=callback_url,
                json=result,
                timeout=10,
            )
            if resp.status_code == 200:
                logger.info("[命令执行] 结果回传成功 [{}]", result.get("cmd_id", ""))
            else:
                logger.warning("[命令执行] 结果回传失败, HTTP {}", resp.status_code)

        except requests.exceptions.ConnectionError:
            logger.error("[命令执行] 结果回传连接失败: {}", callback_url)
        except requests.exceptions.Timeout:
            logger.error("[命令执行] 结果回传超时: {}", callback_url)
        except Exception as e:
            logger.error("[命令执行] 结果回传异常: {}", e)
