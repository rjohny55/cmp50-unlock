#!/usr/bin/env bash
# Build and install the staged CMP 50HX 610.43.03 kernel modules on
# Ubuntu/Debian. The running driver is never unloaded.
set -Eeuo pipefail

readonly driver_version="610.43.03"
readonly repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly kernel_release="$(uname -r)"

stage="rt"
install_userland=0
rebuild_initramfs=0
enable_idle_governor=0

log() { printf '[cmp50-install] %s\n' "$*"; }
die() { printf '[cmp50-install] ERROR: %s\n' "$*" >&2; exit 1; }

usage() {
    cat <<'EOF'
usage: sudo ./install.sh [options]

  --stage stockflow|rt|rebar|gen2  incremental patch stage (default: rt)
  --install-userland               install official NVIDIA 610.43.03 userspace
  --initramfs                      rebuild initramfs after installing modules
  --idle-governor                  install and enable the optional idle governor

The 20 GB path is proven through the rt stage. ReBAR still requires a cold-boot
confirmation on each host. Gen2 is experimental and requires the documented
two-pass link procedure; selecting gen2 does not run that procedure.
EOF
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --stage)
            stage="${2:?--stage needs a value}"
            shift 2
            ;;
        --install-userland)
            install_userland=1
            shift
            ;;
        --initramfs)
            rebuild_initramfs=1
            shift
            ;;
        --idle-governor)
            enable_idle_governor=1
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            die "unknown option: $1"
            ;;
    esac
done

case "${stage}" in stockflow|rt|rebar|gen2) ;; *) die "bad stage: ${stage}" ;; esac
[[ ${EUID} -eq 0 ]] || die "run as root (sudo)"
[[ -f "${repo_dir}/build.sh" ]] || die "run from a complete repository clone"
[[ -d "/lib/modules/${kernel_release}/build" ]] || \
    die "matching kernel headers are missing: linux-headers-${kernel_release}"

command -v lspci >/dev/null || die "pciutils/lspci is required"
mapfile -t cmp_devices < <(lspci -Dn | awk '$3 == "10de:1e09" {print $1}')
[[ ${#cmp_devices[@]} -gt 0 ]] || die "no CMP 50HX (10de:1e09) found"
for bdf in "${cmp_devices[@]}"; do
    sysfs_dev="/sys/bus/pci/devices/${bdf}"
    [[ -r "${sysfs_dev}/subsystem_vendor" && -r "${sysfs_dev}/subsystem_device" ]] || \
        die "cannot read subsystem for ${bdf}"
    read -r subsystem_vendor < "${sysfs_dev}/subsystem_vendor"
    read -r subsystem_device < "${sysfs_dev}/subsystem_device"
    subsystem="${subsystem_vendor#0x}:${subsystem_device#0x}"
    case "${subsystem}" in
        10de:1554|1462:371f) ;;
        *) die "unsupported subsystem ${subsystem} at ${bdf}" ;;
    esac
done
log "found ${#cmp_devices[@]} supported CMP 50HX card(s): ${cmp_devices[*]}"

if command -v mokutil >/dev/null 2>&1 \
        && mokutil --sb-state 2>/dev/null | grep -q 'SecureBoot enabled\|Secure Boot enabled'; then
    die "Secure Boot is enabled; sign the modules with an enrolled key first"
fi

if [[ ${install_userland} -eq 1 ]]; then
    readonly run_url="https://us.download.nvidia.com/XFree86/Linux-x86_64/${driver_version}/NVIDIA-Linux-x86_64-${driver_version}.run"
    readonly run_sha256="45e2d4c134a23c35e50f253a4aa63e7e5e8d17e3d185d4a07c8a58e9612ed392"
    run_file="${repo_dir}/cache/NVIDIA-Linux-x86_64-${driver_version}.run"
    mkdir -p "${repo_dir}/cache"
    if [[ ! -f "${run_file}" ]]; then
        log "downloading official NVIDIA ${driver_version} userspace"
        curl -fL --retry 3 --show-error -o "${run_file}.part" "${run_url}"
        mv "${run_file}.part" "${run_file}"
    fi
    printf '%s  %s\n' "${run_sha256}" "${run_file}" | sha256sum -c -
    sh "${run_file}" --silent --no-kernel-modules --no-dkms --no-backup \
        --no-rebuild-initramfs --no-x-check --no-nouveau-check \
        --skip-module-unload
fi

installed_userland="$(nvidia-smi --version 2>/dev/null | \
    awk -F: '/NVIDIA-SMI version/ {gsub(/[[:space:]]/, "", $2); print $2; exit}' || true)"
if [[ "${installed_userland}" != "${driver_version}" ]]; then
    die "NVIDIA ${driver_version} userspace is not installed; rerun with --install-userland"
fi

artifact_dir="${repo_dir}/artifacts/${driver_version}-${kernel_release}-${stage}"
if [[ ! -f "${artifact_dir}/checksums.sha256" ]]; then
    log "building stage ${stage} for ${kernel_release}"
    CMP50_PATCH_STAGE="${stage}" \
    CMP50_ALL_ARTIFACT_DIR="${artifact_dir}" \
    KERNEL_RELEASE="${kernel_release}" \
        bash "${repo_dir}/build.sh"
fi
(cd "${artifact_dir}" && sha256sum -c checksums.sha256)

timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
backup_dir="/var/lib/cmp50-unlock/backups/${timestamp}"
install_dir="/lib/modules/${kernel_release}/updates"
install -d -m 0700 "${backup_dir}/modules"
for module in nvidia nvidia_uvm nvidia_modeset nvidia_drm nvidia_peermem; do
    old_path="$(modinfo -n "${module}" 2>/dev/null || true)"
    if [[ -n "${old_path}" && -f "${old_path}" ]]; then
        cp -a --parents "${old_path}" "${backup_dir}/modules"
    fi
done
if [[ -e "/boot/initrd.img-${kernel_release}" ]]; then
    cp -a "/boot/initrd.img-${kernel_release}" "${backup_dir}/"
fi
if [[ -f /etc/modprobe.d/cmp50-unlock.conf ]]; then
    cp -a /etc/modprobe.d/cmp50-unlock.conf "${backup_dir}/"
fi
uname -a > "${backup_dir}/uname.txt"
lspci -Dnn > "${backup_dir}/lspci.txt"

install -d -m 0755 "${install_dir}"
for module in nvidia nvidia-uvm nvidia-modeset nvidia-drm nvidia-peermem; do
    [[ -f "${artifact_dir}/${module}.ko" ]] || continue
    install -m 0644 "${artifact_dir}/${module}.ko" "${install_dir}/${module}.ko"
done
if [[ "${stage}" == rebar || "${stage}" == gen2 ]]; then
    printf 'options nvidia cmp50_rebar_size=8\n' > /etc/modprobe.d/cmp50-unlock.conf
else
    rm -f /etc/modprobe.d/cmp50-unlock.conf
fi
depmod -a "${kernel_release}"
[[ "$(modinfo -F version "${install_dir}/nvidia.ko")" == "${driver_version}" ]] || \
    die "installed module version check failed"
[[ "$(modinfo -n nvidia)" == "${install_dir}/nvidia.ko" ]] || \
    die "depmod selected another nvidia.ko; restore backup ${backup_dir}"

if [[ ${enable_idle_governor} -eq 1 ]]; then
    install -d -m 0755 /opt/cmp50-unlock/idle-governor
    install -m 0755 "${repo_dir}/idle-governor/cmp-idle-governor.sh" \
        "${repo_dir}/idle-governor/cmp-pstate.py" /opt/cmp50-unlock/idle-governor/
    install -m 0644 "${repo_dir}/idle-governor/cmp-idle-governor.service" \
        /etc/systemd/system/cmp-idle-governor.service
    systemctl daemon-reload
    systemctl enable cmp-idle-governor.service
    log "idle governor installed; it will start after reboot"
fi

if [[ ${rebuild_initramfs} -eq 1 ]]; then
    command -v update-initramfs >/dev/null || die "update-initramfs is unavailable"
    update-initramfs -u -k "${kernel_release}"
fi

cat <<EOF

PASS_CMP50_INSTALL
stage:  ${stage}
modules: ${install_dir}
backup:  ${backup_dir}

The running driver was not unloaded. Reboot (cold power cycle for ReBAR), then
run: sudo ./verify-live.sh
Rollback: sudo ./rollback.sh '${backup_dir}'
EOF
