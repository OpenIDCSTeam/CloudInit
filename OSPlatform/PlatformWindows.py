import os
import sys
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

    # ==================== VMware Tools管理 ====================

    def uninstall_vmtools_if_not_vmware(self):
        """
        检测虚拟化环境，如果不是VMware/ESXi环境则静默卸载VMware Tools
        避免非VMware环境下VMTools残留导致的兼容性问题
        """
        import threading

        def _do_check_and_uninstall():
            try:
                # 检测虚拟化环境（多种方式综合判断）
                is_vmware = False

                # 方法1：通过wmic查询硬件信息（Win7/Win10兼容）
                combined_info = ""
                wmic_cmds = [
                    "wmic bios get serialnumber,manufacturer /format:list",
                    "wmic computersystem get manufacturer,model /format:list",
                    "wmic baseboard get manufacturer,product /format:list",
                ]
                for cmd in wmic_cmds:
                    result = self._run_cmd(cmd, shell=True)
                    if result.returncode == 0:
                        combined_info += result.stdout.lower() + "\n"

                # 方法2：wmic不可用时（Win11），用PowerShell查询
                if not combined_info.strip():
                    ps_cmd = (
                        "powershell -NoProfile -Command \""
                        "(Get-CimInstance Win32_BIOS | Select-Object Manufacturer,SerialNumber | Format-List);"
                        "(Get-CimInstance Win32_ComputerSystem | Select-Object Manufacturer,Model | Format-List);"
                        "(Get-CimInstance Win32_BaseBoard | Select-Object Manufacturer,Product | Format-List)"
                        "\""
                    )
                    result = self._run_cmd(ps_cmd, shell=True)
                    if result.returncode == 0:
                        combined_info = result.stdout.lower()

                vmware_keywords = ["vmware", "esxi"]
                if any(kw in combined_info for kw in vmware_keywords):
                    is_vmware = True

                # 方法3：通过systeminfo检测（兜底）
                if not is_vmware:
                    sysinfo_result = self._run_cmd(
                        ["systeminfo"],
                        shell=True
                    )
                    if sysinfo_result.returncode == 0:
                        sysinfo_lower = sysinfo_result.stdout.lower()
                        if "vmware" in sysinfo_lower or "esxi" in sysinfo_lower:
                            is_vmware = True

                if is_vmware:
                    logger.info("[VMTools管理] 检测到VMware/ESXi虚拟化环境，保留VMware Tools")
                    return

                logger.info("[VMTools管理] 非VMware/ESXi环境，检查是否安装了VMware Tools")

                # 检查VMware Tools是否已安装（通过注册表查询卸载信息）
                result_check = self._run_cmd(
                    ["reg", "query",
                     r"HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall",
                     "/s", "/f", "VMware Tools"],
                    shell=True
                )

                if result_check.returncode != 0 or "VMware Tools" not in result_check.stdout:
                    logger.info("[VMTools管理] 未检测到VMware Tools安装，无需卸载")
                    return

                logger.info("[VMTools管理] 检测到VMware Tools已安装，开始静默卸载")

                # 卸载前先强制结束VM相关进程
                vm_processes = [
                    "vmtoolsd.exe", "vmwaretray.exe", "vmacthlp.exe",
                    "VGAuthService.exe", "vm3dservice.exe", "vmwareuser.exe"
                ]
                for proc in vm_processes:
                    self._run_cmd(f'taskkill /F /IM {proc}', shell=True)

                # 停止VM相关服务
                vm_services = ["VMTools", "VGAuthService", "vm3dservice", "VMwarePhysicalDiskHelper", "VMUSBArbService"]
                for svc in vm_services:
                    self._run_cmd(f'net stop {svc}', shell=True)

                logger.info("[VMTools管理] 已结束VM相关进程和服务")

                # 尝试通过MsiExec静默卸载（VMware Tools通常通过MSI安装）
                # 先尝试wmic获取GUID（Win7兼容），失败再用PowerShell（Win11）
                product_guid = ""
                guid_result = self._run_cmd(
                    'wmic product where "name=\'VMware Tools\'" get IdentifyingNumber /format:list',
                    shell=True
                )
                if guid_result.returncode == 0 and "IdentifyingNumber=" in guid_result.stdout:
                    for line in guid_result.stdout.splitlines():
                        if line.startswith("IdentifyingNumber="):
                            product_guid = line.split("=", 1)[1].strip()
                            break

                if not product_guid:
                    # wmic不可用（Win11），尝试PowerShell
                    guid_result = self._run_cmd(
                        'powershell -NoProfile -Command "(Get-CimInstance Win32_Product | Where-Object {$_.Name -eq \'VMware Tools\'}).IdentifyingNumber"',
                        shell=True
                    )
                    if guid_result.returncode == 0 and guid_result.stdout.strip():
                        product_guid = guid_result.stdout.strip()

                if product_guid:
                    # 使用MsiExec静默卸载
                    logger.info("[VMTools管理] 找到VMware Tools GUID: {}，执行静默卸载", product_guid)
                    uninstall_cmd = f'msiexec /x {product_guid} /qn /norestart'
                    result_uninstall = subprocess.run(
                        uninstall_cmd,
                        capture_output=True,
                        text=True,
                        shell=True,
                        timeout=300
                    )
                else:
                    # 备用方案：直接调用VMware Tools卸载程序
                    logger.info("[VMTools管理] 未找到GUID，尝试直接调用卸载程序")
                    vmtools_path = r"C:\Program Files\VMware\VMware Tools"
                    uninstaller = os.path.join(vmtools_path, "VMwareToolsUninstaller.exe")

                    if os.path.exists(uninstaller):
                        result_uninstall = subprocess.run(
                            [uninstaller, "/S"],
                            capture_output=True,
                            text=True,
                            timeout=300
                        )
                    else:
                        # 最终备用：通过wmic卸载（Win7）或PowerShell卸载（Win11）
                        logger.info("[VMTools管理] 卸载程序不存在，尝试wmic/PowerShell卸载")
                        result_uninstall = self._run_cmd(
                            'wmic product where "name=\'VMware Tools\'" call uninstall /nointeractive',
                            shell=True
                        )
                        if result_uninstall.returncode != 0:
                            result_uninstall = self._run_cmd(
                                'powershell -NoProfile -Command "Get-CimInstance Win32_Product | Where-Object {$_.Name -eq \'VMware Tools\'} | Invoke-CimMethod -MethodName Uninstall"',
                                shell=True
                            )

                if result_uninstall.returncode == 0:
                    logger.info("[VMTools管理] VMware Tools 静默卸载完成")
                else:
                    logger.warning("[VMTools管理] VMware Tools 卸载返回码: {}", result_uninstall.returncode)
                    if hasattr(result_uninstall, 'stderr') and result_uninstall.stderr:
                        logger.debug("[VMTools管理] 卸载错误输出: {}", result_uninstall.stderr[:500])

            except subprocess.TimeoutExpired:
                logger.warning("[VMTools管理] 卸载操作超时")
            except Exception as e:
                logger.error("[VMTools管理] 检测/卸载过程异常: {}", e)

        # 异步执行检测和卸载
        thread = threading.Thread(target=_do_check_and_uninstall, daemon=True, name="VMToolsCheckThread")
        thread.start()
        logger.info("[VMTools管理] 已启动异步虚拟化环境检测线程")

    # ==================== Windows激活 ====================

    def activate_windows(self):
        """
        异步激活Windows系统
        Win10/11使用HWID方式，Win7/8等旧版本使用TSforge方式
        """
        import platform
        import threading

        def _do_activate():
            try:
                # 获取MASAIO.cmd脚本路径（与CloudInit.exe同目录）
                if getattr(sys, 'frozen', False):
                    script_dir = os.path.dirname(sys.executable)
                else:
                    script_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
                masaio_path = os.path.join(script_dir, "MASAIO.cmd")

                if not os.path.exists(masaio_path):
                    logger.warning("[Windows激活] 激活脚本不存在: {}", masaio_path)
                    return

                # 检查是否已激活
                result = self._run_cmd(
                    ["cscript", "//nologo", r"C:\Windows\System32\slmgr.vbs", "/xpr"],
                    shell=True
                )
                if result.returncode == 0 and "永久激活" in result.stdout:
                    logger.info("[Windows激活] 系统已永久激活，跳过")
                    return
                if result.returncode == 0 and "permanently activated" in result.stdout.lower():
                    logger.info("[Windows激活] 系统已永久激活，跳过")
                    return

                # 根据Windows版本选择激活方式
                win_ver = platform.version()  # 如 "10.0.19041"
                major_ver = int(win_ver.split(".")[0]) if win_ver else 0
                build_number = int(win_ver.split(".")[2]) if len(win_ver.split(".")) >= 3 else 0

                if major_ver >= 10:
                    # Win10/11 使用 HWID 方式
                    activate_param = "/HWID /S"
                    method_name = "HWID"
                else:
                    # Win7/8/8.1 使用 TSforge 方式
                    activate_param = "/Z-Windows /S"
                    method_name = "TSforge"

                logger.info("[Windows激活] 使用 {} 方式激活 (版本: {})", method_name, win_ver)

                # 执行激活脚本
                cmd = f'cmd.exe /c "{masaio_path}" {activate_param}'
                result = subprocess.run(
                    cmd,
                    capture_output=True,
                    text=True,
                    shell=True,
                    timeout=300  # 5分钟超时
                )

                if result.returncode == 0:
                    logger.info("[Windows激活] {} 激活执行完成", method_name)
                else:
                    logger.warning("[Windows激活] {} 激活返回码: {}", method_name, result.returncode)
                    if result.stderr:
                        logger.debug("[Windows激活] 错误输出: {}", result.stderr[:500])

            except subprocess.TimeoutExpired:
                logger.warning("[Windows激活] 激活脚本执行超时")
            except Exception as e:
                logger.error("[Windows激活] 激活过程异常: {}", e)

        # 异步执行激活
        thread = threading.Thread(target=_do_activate, daemon=True, name="WinActivateThread")
        thread.start()
        logger.info("[Windows激活] 已启动异步激活线程")