#!/bin/sh
# Полное удаление RandDesk с этого ПК (Linux).
# Удаляет службу, программу, настройки и данные, правила брандмауэра и, по желанию, ZeroTier One.

if [ "$(id -u)" -ne 0 ]; then
    echo "Нужны права root — запускаю через sudo…"
    exec sudo sh "$0" "$@"
fi

echo ""
echo "  Удаление RandDesk с этого ПК"
echo "  Будут удалены служба RandDesk, программа, все настройки (логин и пароль, список ПК,"
echo "  разрешения, журнал) и правила брандмауэра RandDesk."
echo ""
printf "  Продолжить? [Д/н] "
read ans
case "$ans" in н|Н|n|N|нет|no) echo "  Отменено."; exit 0 ;; esac
printf "  Удалить также ZeroTier One (RandDesk ставил его для связи через интернет)? [Д/н] "
read zt
case "$zt" in н|Н|n|N|нет|no) removezt=0 ;; *) removezt=1 ;; esac

# 1. Служба
for unit in RandDesk randdesk; do
    if systemctl list-unit-files 2>/dev/null | grep -q "^$unit.service"; then
        systemctl stop "$unit" 2>/dev/null
        systemctl disable "$unit" 2>/dev/null
        echo "  служба $unit остановлена"
    fi
    rm -f "/etc/systemd/system/$unit.service" "/lib/systemd/system/$unit.service"
done
systemctl daemon-reload 2>/dev/null
pkill -x randdesk 2>/dev/null

# 2. Брандмауэр
if command -v ufw >/dev/null 2>&1; then
    ufw delete allow 8420/tcp >/dev/null 2>&1
    ufw delete allow 8421/udp >/dev/null 2>&1
fi
if command -v firewall-cmd >/dev/null 2>&1 && firewall-cmd --state >/dev/null 2>&1; then
    firewall-cmd --permanent --remove-port=8420/tcp --remove-port=8421/udp >/dev/null 2>&1
    firewall-cmd --reload >/dev/null 2>&1
fi
echo "  правила брандмауэра RandDesk удалены"

# 3. ZeroTier One
hasZT=0
{ command -v zerotier-one >/dev/null 2>&1 || [ -d /var/lib/zerotier-one ]; } && hasZT=1
if [ "$removezt" = 1 ] && [ "$hasZT" = 1 ]; then
    echo "  удаление ZeroTier One…"
    systemctl stop zerotier-one 2>/dev/null
    systemctl disable zerotier-one 2>/dev/null
    if command -v apt-get >/dev/null 2>&1; then apt-get remove -y --purge zerotier-one >/dev/null 2>&1
    elif command -v dnf >/dev/null 2>&1; then dnf remove -y zerotier-one >/dev/null 2>&1
    elif command -v yum >/dev/null 2>&1; then yum remove -y zerotier-one >/dev/null 2>&1
    elif command -v zypper >/dev/null 2>&1; then zypper -n remove zerotier-one >/dev/null 2>&1
    elif command -v pacman >/dev/null 2>&1; then pacman -Rns --noconfirm zerotier-one >/dev/null 2>&1
    fi
    rm -rf /var/lib/zerotier-one
    echo "  ZeroTier One удалён"
fi

# 4. Программа и данные
rm -rf /var/lib/randdesk
rm -f /usr/local/bin/randdesk
echo "  удалены /usr/local/bin/randdesk и /var/lib/randdesk"
rm -f /usr/local/bin/randdesk-uninstall

echo ""
echo "  RandDesk удалён с этого ПК."
