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
disable_nouveau=0
allow_experimental_gen2=0
ai_manager_data_dir="/opt/stacks/ai-server-manager/data"

log() { printf '[cmp50-install] %s\n' "$*"; }
die() { printf '[cmp50-install] ERROR: %s\n' "$*" >&2; exit 1; }

usage() {
    cat <<'EOF'
usage: sudo ./install.sh [options]

  --stage stockflow|rt|rebar|gen2  incremental patch stage (default: rt)
  --install-userland               install official NVIDIA 610.43.03 userspace
  --initramfs                      rebuild initramfs after installing modules
  --disable-nouveau                stage a nouveau blacklist for the next boot;
                                   requires --initramfs, never unloads it live
  --idle-governor                  install and enable the optional idle governor
  --ai-manager-data-dir PATH       host data path used by AI Server Manager
                                   (default: /opt/stacks/ai-server-manager/data)
  --allow-experimental-gen2        acknowledge the host-specific Gen2 path

The 20 GB rt and ReBAR stages are boot-validated on the reference host with
kernels 6.8.0-137 and 6.8.0-138. Validate ReBAR on every new host. Gen2 uses a
fail-closed second-pass FLR helper on hosts that remain at Gen1 after the first
driver load. The supported kernels are 6.8.0-137 and 6.8.0-147, on the
documented X99 configurations. Other kernels, including 138, remain blocked.
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
        --disable-nouveau)
            disable_nouveau=1
            shift
            ;;
        --ai-manager-data-dir)
            ai_manager_data_dir="${2:?--ai-manager-data-dir needs a value}"
            shift 2
            ;;
        --allow-experimental-gen2)
            allow_experimental_gen2=1
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
if [[ ${disable_nouveau} -eq 1 && ${rebuild_initramfs} -ne 1 ]]; then
    die "--disable-nouveau requires --initramfs so the next boot uses the new policy"
fi
if [[ "${stage}" == gen2 && ${allow_experimental_gen2} -ne 1 ]]; then
    die "gen2 is host-specific; rerun with --allow-experimental-gen2 only with local recovery access"
fi
if [[ "${stage}" == gen2 ]]; then
    case "${kernel_release}" in
        6.8.0-137-generic|6.8.0-147-generic) ;;
        *) die "the automatic Gen2 second pass supports only kernels 6.8.0-137-generic and 6.8.0-147-generic" ;;
    esac
    for required in flock fuser setpci nvidia-modprobe; do
        command -v "${required}" >/dev/null || die "Gen2 requires ${required}"
    done
fi
[[ ${EUID} -eq 0 ]] || die "run as root (sudo)"
case "${ai_manager_data_dir}" in
    /*) ;;
    *) die "--ai-manager-data-dir must be an absolute path" ;;
esac
case "${ai_manager_data_dir}" in
    *[!A-Za-z0-9_./-]*) die "--ai-manager-data-dir contains unsupported characters" ;;
esac
[[ -f "${repo_dir}/build.sh" ]] || die "run from a complete repository clone"
[[ -d "/lib/modules/${kernel_release}/build" ]] || \
    die "matching kernel headers are missing: linux-headers-${kernel_release}"
if [[ ${rebuild_initramfs} -eq 1 ]]; then
    command -v update-initramfs >/dev/null || die "update-initramfs is unavailable"
fi
if [[ ${disable_nouveau} -eq 1 ]]; then
    [[ -f "${repo_dir}/config/cmp50-disable-nouveau.conf" ]] || \
        die "nouveau policy is missing from the repository"
fi
[[ -f "${repo_dir}/scripts/userspace-version.sh" ]] || \
    die "userspace version helper is missing from the repository"
if [[ ${enable_idle_governor} -eq 1 ]]; then
    [[ -f "${repo_dir}/install-governor.sh" ]] || die 'governor installer is missing'
    [[ "$(uname -m)" == x86_64 ]] || die 'bundled governor requires linux/amd64'
    (cd "${repo_dir}/idle-governor" && sha256sum -c checksums.sha256)
    "${repo_dir}/idle-governor/bin/cmp-idle-governor-linux-amd64" --version >/dev/null || \
        die 'bundled governor cannot execute on this host'
    source "${repo_dir}/scripts/governor-profile.sh"
    cmp50_governor_profile >/dev/null || die 'unsupported governor GPU configuration'
fi

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
    if [[ -L "${sysfs_dev}/driver" &&
          "$(basename "$(readlink -f "${sysfs_dev}/driver")")" == nouveau &&
          ${disable_nouveau} -ne 1 ]]; then
        die "nouveau owns ${bdf}; use --disable-nouveau --initramfs for a staged first install"
    fi
done
log "found ${#cmp_devices[@]} supported CMP 50HX card(s): ${cmp_devices[*]}"

if command -v mokutil >/dev/null 2>&1 \
        && mokutil --sb-state 2>/dev/null | grep -q 'SecureBoot enabled\|Secure Boot enabled'; then
    die "Secure Boot is enabled; sign the modules with an enrolled key first"
fi

# Preserve the previous boot state before changing userspace or modules, also
# on a clean server where nouveau is the only installed GPU driver.
timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
backup_dir="/var/lib/cmp50-unlock/backups/${timestamp}-$$"
install_dir="/lib/modules/${kernel_release}/updates"
install -d -m 0700 "${backup_dir}" "${backup_dir}/modules"
for module in nvidia nvidia_uvm nvidia_modeset nvidia_drm nvidia_peermem; do
    old_path="$(modinfo -n "${module}" 2>/dev/null || true)"
    if [[ -n "${old_path}" && -f "${old_path}" ]]; then
        cp -a --parents "${old_path}" "${backup_dir}/modules"
    fi
done
if [[ -e "/boot/initrd.img-${kernel_release}" ]]; then
    cp -a "/boot/initrd.img-${kernel_release}" "${backup_dir}/"
fi
for config_file in cmp50-unlock.conf cmp50-disable-nouveau.conf; do
    if [[ -f "/etc/modprobe.d/${config_file}" ]]; then
        cp -a "/etc/modprobe.d/${config_file}" "${backup_dir}/"
    fi
done
for path in /usr/local/sbin/cmp50-gen2-second-pass \
            /etc/systemd/system/cmp50-gen2-second-pass.service; do
    if [[ -f "${path}" ]]; then
        cp -a --parents "${path}" "${backup_dir}/"
    fi
done
if systemctl is-enabled cmp50-gen2-second-pass.service >/dev/null 2>&1; then
    : > "${backup_dir}/cmp50-gen2-second-pass.enabled"
fi
uname -a > "${backup_dir}/uname.txt"
lspci -Dnn > "${backup_dir}/lspci.txt"
log "rollback backup: ${backup_dir}"
if [[ ${disable_nouveau} -eq 1 ]]; then
    : > "${backup_dir}/nouveau-policy-managed"
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

source "${repo_dir}/scripts/userspace-version.sh"
if ! cmp50_userspace_matches "${driver_version}"; then
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

install -d -m 0755 "${install_dir}"
for module in nvidia nvidia-uvm nvidia-modeset nvidia-drm nvidia-peermem; do
    [[ -f "${artifact_dir}/${module}.ko" ]] || continue
    install -m 0644 "${artifact_dir}/${module}.ko" "${install_dir}/${module}.ko"
done
if [[ "${stage}" == gen2 ]]; then
    install -m 0644 "${repo_dir}/config/cmp50-gen2.conf" \
        /etc/modprobe.d/cmp50-unlock.conf
    install -m 0755 "${repo_dir}/scripts/cmp50-gen2-second-pass.sh" \
        /usr/local/sbin/cmp50-gen2-second-pass
    install -m 0644 "${repo_dir}/systemd/cmp50-gen2-second-pass.service" \
        /etc/systemd/system/cmp50-gen2-second-pass.service
    systemctl daemon-reload
    systemctl enable cmp50-gen2-second-pass.service
elif [[ "${stage}" == rebar ]]; then
    printf 'options nvidia cmp50_rebar_size=8\n' > /etc/modprobe.d/cmp50-unlock.conf
else
    rm -f /etc/modprobe.d/cmp50-unlock.conf
fi
if [[ "${stage}" != gen2 ]] && systemctl cat cmp50-gen2-second-pass.service >/dev/null 2>&1; then
    systemctl disable cmp50-gen2-second-pass.service >/dev/null 2>&1 || true
    rm -f /usr/local/sbin/cmp50-gen2-second-pass \
        /etc/systemd/system/cmp50-gen2-second-pass.service
    systemctl daemon-reload
fi
depmod -a "${kernel_release}"
[[ "$(modinfo -F version "${install_dir}/nvidia.ko")" == "${driver_version}" ]] || \
    die "installed module version check failed"
[[ "$(modinfo -n nvidia)" == "${install_dir}/nvidia.ko" ]] || \
    die "depmod selected another nvidia.ko; restore backup ${backup_dir}"

if [[ ${disable_nouveau} -eq 1 ]]; then
    install -m 0644 "${repo_dir}/config/cmp50-disable-nouveau.conf" \
        /etc/modprobe.d/cmp50-disable-nouveau.conf
fi

if [[ ${enable_idle_governor} -eq 1 ]]; then
    bash "${repo_dir}/install-governor.sh" --ai-manager-data-dir "${ai_manager_data_dir}"
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
