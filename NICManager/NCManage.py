from __future__ import annotations

import netifaces as ni
from loguru import logger
from .NCConfig import NCConfig


class NCManage:
    """网络接口管理器"""

    def __init__(self):
        self.nic_list: dict[str, NCConfig] = {}
        self.get_nic()

    def get_nic(self):
        """扫描并获取所有有效网络接口"""
        self.nic_list = {}

        # 获取默认网关信息
        gateways_info = ni.gateways()
        default_ipv4_gateway = gateways_info.get('default', {}).get(ni.AF_INET, (None, None))[0]
        default_ipv6_gateway = gateways_info.get('default', {}).get(ni.AF_INET6, (None, None))[0]

        # 获取每个接口的网关信息
        ipv4_gateways = gateways_info.get(ni.AF_INET, [])
        ipv6_gateways = gateways_info.get(ni.AF_INET6, [])

        # 创建网关映射字典 {接口名: 网关IP}
        ipv4_gateway_map = {gateway[1]: gateway[0] for gateway in ipv4_gateways}
        ipv6_gateway_map = {gateway[1]: gateway[0] for gateway in ipv6_gateways}

        processed_interfaces = set()

        for nic_data in ni.interfaces():
            nic_key = nic_data.lower()

            if nic_key in processed_interfaces:
                continue

            try:
                # 获取MAC地址
                mac = ni.ifaddresses(nic_data)[ni.AF_LINK][0]['addr']
                if mac == '00:00:00:00:00:00':
                    processed_interfaces.add(nic_key)
                    continue

                if mac == '':
                    mac = '00:00:00:00:00:00'

                # 获取IPv4地址信息
                ip4_info = ni.ifaddresses(nic_data).get(ni.AF_INET, [{}])
                ip4_addr = ip4_info[0].get('addr', '') if ip4_info and ip4_info[0] else ''

                # 获取IPv6地址信息
                ip6_info = ni.ifaddresses(nic_data).get(ni.AF_INET6, [{}])
                ip6_addr = ip6_info[0].get('addr', '') if ip6_info and ip6_info[0] else ''

                # 获取IPv4网关
                ip4_gate = ipv4_gateway_map.get(nic_data, '')
                if not ip4_gate and default_ipv4_gateway:
                    ip4_gate = default_ipv4_gateway

                # 获取IPv6网关
                ip6_gate = ipv6_gateway_map.get(nic_data, '')
                if not ip6_gate and default_ipv6_gateway:
                    ip6_gate = default_ipv6_gateway

                # 创建NCConfig对象
                nic_config = NCConfig(
                    mac_addr=mac,
                    nic_type=nic_data,
                    ip4_addr=ip4_addr,
                    ip6_addr=ip6_addr,
                    ip4_gate=ip4_gate,
                    ip6_gate=ip6_gate
                )

                self.nic_list[nic_key] = nic_config
                processed_interfaces.add(nic_key)
                logger.debug("[网卡] {} MAC={} IPv4={} GW={}",
                             nic_data, mac, ip4_addr, ip4_gate)

            except (KeyError, IndexError) as e:
                logger.warning("[网卡] 获取接口 {} 信息失败: {}", nic_data, e)
                processed_interfaces.add(nic_key)
                continue

        logger.info("[网卡] 扫描完成，发现 {} 个有效接口", len(self.nic_list))

    def get_nic_info(self) -> dict:
        """返回格式化的网卡信息字典"""
        return {name: config.to_dict() for name, config in self.nic_list.items()}
