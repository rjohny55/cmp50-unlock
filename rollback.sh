#!/usr/bin/env bash
set -Eeuo pipefail

[[ ${EUID} -eq 0 ]] || { echo "run as root" >&2; exit 1; }
backup_dir="${1:-}"
[[ -n "${backup_dir}" && -d "${backup_dir}/modules" ]] || {
    echo "usage: sudo ./rollback.sh /var/lib/cmp50-unlock/backups/TIMESTAMP" >&2
    exit 2
}
case "${backup_dir}" in
    /var/lib/cmp50-unlock/backups/*) ;;
    *) echo "refusing backup outside /var/lib/cmp50-unlock/backups" >&2; exit 2 ;;
esac

kernel_release="$(sed -n 's/.* \([0-9][^ ]*-generic\) .*/\1/p' "${backup_dir}/uname.txt" | head -n1)"
[[ -n "${kernel_release}" ]] || kernel_release="$(uname -r)"
install_dir="/lib/modules/${kernel_release}/updates"
disabled_dir="/var/lib/cmp50-unlock/disabled-$(date -u +%Y%m%dT%H%M%SZ)"
install -d -m 0700 "${disabled_dir}"
for module in nvidia nvidia-uvm nvidia-modeset nvidia-drm nvidia-peermem; do
    if [[ -f "${install_dir}/${module}.ko" ]]; then
        mv "${install_dir}/${module}.ko" "${disabled_dir}/"
    fi
done
cp -a "${backup_dir}/modules/." /
systemctl disable cmp50-gen2-second-pass.service >/dev/null 2>&1 || true
rm -f /usr/local/sbin/cmp50-gen2-second-pass \
    /etc/systemd/system/cmp50-gen2-second-pass.service
for path in usr/local/sbin/cmp50-gen2-second-pass \
            etc/systemd/system/cmp50-gen2-second-pass.service; do
    if [[ -f "${backup_dir}/${path}" ]]; then
        install -D -m "$(stat -c '%a' "${backup_dir}/${path}")" \
            "${backup_dir}/${path}" "/${path}"
    fi
done
systemctl daemon-reload
if [[ -f "${backup_dir}/cmp50-gen2-second-pass.enabled" ]]; then
    systemctl enable cmp50-gen2-second-pass.service >/dev/null
fi
rm -f /etc/modprobe.d/cmp50-unlock.conf
if [[ -f "${backup_dir}/cmp50-unlock.conf" ]]; then
    cp -a "${backup_dir}/cmp50-unlock.conf" /etc/modprobe.d/cmp50-unlock.conf
fi
if [[ -f "${backup_dir}/initrd.img-${kernel_release}" ]]; then
    cp -a "${backup_dir}/initrd.img-${kernel_release}" "/boot/initrd.img-${kernel_release}"
fi
depmod -a "${kernel_release}"
echo "PASS_CMP50_ROLLBACK: reboot into ${kernel_release}"
