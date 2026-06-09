import json
import psutil
import GPUtil
from loguru import logger
from .HWStatus import HWStatus
from .VMPowers import VMPowers


class VMStatus:
    """虚拟机硬件状态采集"""

    def __init__(self):
        self.vm_status = HWStatus()

    def to_dict(self) -> dict:
        """转换为字典"""
        return self.vm_status.to_dict()

    def __str__(self) -> str:
        return json.dumps(self.to_dict())

    def status(self) -> HWStatus:
        """采集当前硬件状态"""
        self.vm_status.ac_status = VMPowers.STARTED
        self._collect_cpu()
        self._collect_memory()
        self._collect_disk()
        self._collect_gpu()
        self._collect_network()
        return self.vm_status

    def _collect_cpu(self):
        """采集CPU信息"""
        try:
            self.vm_status.cpu_total = psutil.cpu_count(logical=True)
            self.vm_status.cpu_usage = int(psutil.cpu_percent(interval=1))
        except Exception as e:
            logger.error("[状态采集] CPU信息获取失败: {}", e)

    def _collect_memory(self):
        """采集内存信息"""
        try:
            mem = psutil.virtual_memory()
            self.vm_status.mem_total = int(mem.total / (1024 * 1024))
            self.vm_status.mem_usage = int(mem.used / (1024 * 1024))
        except Exception as e:
            logger.error("[状态采集] 内存信息获取失败: {}", e)

    def _collect_disk(self):
        """采集磁盘信息"""
        try:
            import platform
            disk_path = 'C:\\' if platform.system() == 'Windows' else '/'
            disk_usage = psutil.disk_usage(disk_path)
            self.vm_status.hdd_total = int(disk_usage.total / (1024 * 1024))
            self.vm_status.hdd_usage = int(disk_usage.used / (1024 * 1024))
        except Exception as e:
            logger.error("[状态采集] 磁盘信息获取失败: {}", e)

    def _collect_gpu(self):
        """采集GPU信息"""
        try:
            gpus = GPUtil.getGPUs()
            self.vm_status.gpu_total = len(gpus)
            for gpu in gpus:
                self.vm_status.gpu_usage[gpu.id] = int(gpu.load * 100)
        except Exception as e:
            logger.debug("[状态采集] GPU信息获取失败（可能无GPU）: {}", e)

    def _collect_network(self):
        """采集网络带宽信息"""
        try:
            nic_list = psutil.net_io_counters(True)
            max_name = ""
            total_tx = total_rx = 0

            for nic_name, nic_data in nic_list.items():
                if nic_data.bytes_sent / (1024 * 1024) > total_tx:
                    total_tx = nic_data.bytes_sent / (1024 * 1024)
                    total_rx = nic_data.bytes_recv / (1024 * 1024)
                    max_name = nic_name

            self.vm_status.flu_usage = int(total_tx + total_rx)
            self.vm_status.network_u = int(total_tx / 60 * 8)
            self.vm_status.network_d = int(total_rx / 60 * 8)

            logger.debug("[状态采集] 网络流量: {}MB | 上行: {}Mbps | 下行: {}Mbps",
                         self.vm_status.flu_usage,
                         self.vm_status.network_u,
                         self.vm_status.network_d)

            psutil.net_io_counters.cache_clear()

            # 获取物理网卡速率
            nic_stats = psutil.net_if_stats()
            if max_name in nic_stats:
                self.vm_status.network_a = nic_stats[max_name].speed
        except Exception as e:
            logger.error("[状态采集] 网络信息获取失败: {}", e)