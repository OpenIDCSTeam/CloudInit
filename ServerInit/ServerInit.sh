#!/bin/bash
# OpenIDC ServerInit 安装脚本

# 停止已有服务（首次安装时忽略错误）
systemctl stop ServerInit 2>/dev/null
# 等待进程完全退出，避免 Text file busy
while pgrep -x ServerInit >/dev/null 2>&1; do
    sleep 1
done

chmod +x ./ServerInit
chmod +x ./ServerInit.service
mkdir                -p /opt/ServerInit/
rm -f /opt/ServerInit/ServerInit
cp ./ServerInit         /opt/ServerInit/
cp ./ServerInit.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now ServerInit

# 安装磁盘扩容依赖工具（growpart）
if [ -f /etc/os-release ]; then
    . /etc/os-release
    OS_ID=$ID
else
    OS_ID=$(uname -s)
fi

echo "[环境准备] 检测到操作系统: $OS_ID"

case "$OS_ID" in
    ubuntu|debian)
        apt-get install -y cloud-guest-utils e2fsprogs xfsprogs 2>/dev/null
        ;;
    centos|rhel|fedora|almalinux|rocky|ol)
        yum install -y cloud-utils-growpart e2fsprogs xfsprogs 2>/dev/null || \
        dnf install -y cloud-utils-growpart e2fsprogs xfsprogs 2>/dev/null
        ;;
    arch)
        pacman -S --noconfirm cloud-guest-utils e2fsprogs xfsprogs 2>/dev/null
        ;;
    opensuse*)
        zypper install -y growpart e2fsprogs xfsprogs 2>/dev/null
        ;;
esac

echo "[环境准备] 磁盘扩容工具安装完成"

# 根据操作系统类型配置网络
echo "[网络配置] 检测到操作系统: $OS_ID"

case "$OS_ID" in
    ubuntu|debian|arch|opensuse*)
        echo "[网络配置] 使用 systemd-networkd 配置网络"
        systemctl enable --now systemd-networkd

        mkdir -p /etc/systemd/network
        cat > /etc/systemd/network/99-dhcp-any.network << 'EOF'
[Match]
Name=*

[Network]
DHCP=yes
EOF

        echo "[网络配置] 网络配置文件已创建: /etc/systemd/network/99-dhcp-any.network"
        systemctl restart systemd-networkd
        echo "[网络配置] systemd-networkd 已重启"
        ;;
    
    centos|rhel|fedora|almalinux|rocky|ol)
        echo "[网络配置] 使用 NetworkManager 配置网络"
        
        systemctl enable --now NetworkManager
        
        for interface in $(nmcli -t -f DEVICE device status | grep -v "^lo"); do
            echo "[网络配置] 配置接口 $interface 为 DHCP"
            nmcli con modify "$interface" ipv4.method auto 2>/dev/null || \
            nmcli device connect "$interface" 2>/dev/null
        done
        
        systemctl restart NetworkManager
        echo "[网络配置] NetworkManager 已重启"
        ;;
    
    *)
        echo "[网络配置] 未知操作系统 $OS_ID，跳过网络配置"
        echo "[网络配置] 请手动配置网络"
        ;;
esac

# 清理系统痕迹
history -c && history -w
find /home /root -maxdepth 1 -type f -name '.bash_history' -exec truncate -s 0 {} \;
find /tmp /var/tmp -type f -delete 2>/dev/null
find /tmp /var/tmp -mindepth 1 -maxdepth 1 -type d -exec rm -rf {} + 2>/dev/null

command -v apt >/dev/null && apt clean
command -v yum  >/dev/null && yum clean all
command -v dnf  >/dev/null && dnf clean all
command -v zypper >/dev/null && zypper clean --all
command -v pacman >/dev/null && pacman -Scc --noconfirm

command -v snap >/dev/null && snap list --all | awk '/disabled/{print $1,$2}' | while read snapname revision; do snap remove "$snapname" --revision="$revision"; done 2>/dev/null

journalctl --vacuum-time=1d

find /home /root -type d -name '.local' -exec find {}/Share/Trash -mindepth 1 -delete \; 2>/dev/null
