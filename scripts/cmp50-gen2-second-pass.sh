#!/usr/bin/env bash
set -Eeuo pipefail

udev_stopped=0

log() { printf '[cmp50-gen2-second-pass] %s\n' "$*"; }
die() { log "ERROR: $*" >&2; exit 1; }

restore_udev() {
    if [[ ${udev_stopped} -eq 1 ]]; then
        systemctl unmask --runtime systemd-udevd.service \
            systemd-udevd-control.socket systemd-udevd-kernel.socket \
            >/dev/null 2>&1 || true
        systemctl daemon-reload >/dev/null 2>&1 || true
        systemctl start systemd-udevd-control.socket \
            systemd-udevd-kernel.socket systemd-udevd.service \
            >/dev/null 2>&1 || true
    fi
}
trap restore_udev EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

[[ ${EUID} -eq 0 ]] || die 'must run as root'
case "$(uname -r)" in
    6.8.0-137-generic|6.8.0-147-generic) ;;
    *) die 'this second-pass procedure is restricted to kernels 137 and 147' ;;
esac
command -v lspci >/dev/null || die 'lspci is required'
command -v setpci >/dev/null || die 'setpci is required'
command -v nvidia-modprobe >/dev/null || die 'nvidia-modprobe is required'
command -v fuser >/dev/null || die 'fuser (psmisc) is required'
command -v flock >/dev/null || die 'flock is required'
exec 9>/run/lock/cmp50-gen2.lock
flock -n 9 || die 'another Gen2 initialization is running'

mapfile -t devices < <(lspci -Dn | awk '$3 == "10de:1e09" {print $1}')
readonly expected_count=${#devices[@]}
[[ ${expected_count} -gt 0 ]] || die 'no CMP 50HX devices found'

for bdf in "${devices[@]}"; do
    dev="/sys/bus/pci/devices/${bdf}"
    [[ -w "${dev}/reset" ]] || die "FLR is unavailable for ${bdf}"
    read -r subvendor < "${dev}/subsystem_vendor"
    read -r subdevice < "${dev}/subsystem_device"
    case "${subvendor#0x}:${subdevice#0x}" in
        10de:1554|1462:371f) ;;
        *) die "unsupported subsystem at ${bdf}" ;;
    esac
done

# Complete the normal first pass before preserving its Gen2 policy through FLR.
modprobe nvidia
nvidia-modprobe -u -c=0
for _ in {1..60}; do
    if [[ "$(nvidia-smi --query-gpu=pci.bus_id --format=csv,noheader 2>/dev/null | wc -l)" -eq ${expected_count} ]]; then
        break
    fi
    sleep 1
done
[[ "$(nvidia-smi --query-gpu=pci.bus_id --format=csv,noheader 2>/dev/null | wc -l)" -eq ${expected_count} ]] || \
    die 'first pass did not initialize all GPUs'

all_gen2=1
for bdf in "${devices[@]}"; do
    read -r speed < "/sys/bus/pci/devices/${bdf}/current_link_speed"
    [[ "${speed}" == '5.0 GT/s PCIe' ]] || all_gen2=0
done
if [[ ${all_gen2} -eq 1 ]]; then
    mapfile -t bar1_totals < <(nvidia-smi -q -d MEMORY | \
        awk '/BAR1 Memory Usage/{getline; if ($1 == "Total") print $3}')
    [[ ${#bar1_totals[@]} -eq ${expected_count} ]] || \
        die 'could not read BAR1 totals for every GPU'
    for total in "${bar1_totals[@]}"; do
        [[ ${total} -ge 16000 ]] || die "unexpected BAR1 total: ${total} MiB"
    done
    log "PASS: ${expected_count} GPUs reached PCIe Gen2 on the first pass"
    exit 0
fi

# Unloading NVIDIA is a module-wide operation. Do not disturb another GPU type
# or a running application, even when all requested CMP boards are supported.
for driver_device in /sys/bus/pci/drivers/nvidia/????:??:??.?; do
    [[ -L "${driver_device}" ]] || continue
    read -r vendor < "${driver_device}/vendor"
    read -r device < "${driver_device}/device"
    [[ "${vendor}:${device}" == 0x10de:0x1e09 ]] || \
        die 'a non-CMP50 GPU uses nvidia; refusing the module-wide second pass'
done
shopt -s nullglob
gpu_nodes=(/dev/nvidia[0-9]* /dev/nvidiactl /dev/nvidia-uvm /dev/nvidia-uvm-tools /dev/nvidia-modeset)
if fuser "${gpu_nodes[@]}" >/dev/null 2>&1; then
    die 'NVIDIA device files are in use; stop GPU applications before the second pass'
fi
for unit in systemd-udevd.service systemd-udevd-control.socket systemd-udevd-kernel.socket; do
    unit_state=$(systemctl is-enabled "${unit}" 2>/dev/null || true)
    [[ "${unit_state}" != masked && "${unit_state}" != masked-runtime ]] || \
        die "${unit} was already masked; refusing to alter its policy"
done

# Mark before the first operation: a partial mask/stop failure must also run
# restoration. Previously an error in systemctl stop left udev masked.
udev_stopped=1
systemctl mask --runtime systemd-udevd.service \
    systemd-udevd-control.socket systemd-udevd-kernel.socket >/dev/null
systemctl stop systemd-udevd-control.socket systemd-udevd-kernel.socket \
    systemd-udevd.service

for bdf in "${devices[@]}"; do
    dev="/sys/bus/pci/devices/${bdf}"
    if [[ -L "${dev}/driver" ]]; then
        printf '%s\n' "${bdf}" > "${dev}/driver/unbind"
    fi
done

modprobe -r nvidia_drm nvidia_modeset nvidia_uvm nvidia_peermem nvidia \
    2>/dev/null || true
grep -q '^nvidia ' /proc/modules && die 'nvidia core module is still in use'

for bdf in "${devices[@]}"; do
    printf '1\n' > "/sys/bus/pci/devices/${bdf}/reset"
done

# modprobe supplies the installed module's dependencies and the options from
# /etc/modprobe.d/cmp50-unlock.conf. No bridge Link Disable is used.
modprobe nvidia
nvidia-modprobe -u -c=0

for _ in {1..60}; do
    if [[ "$(nvidia-smi --query-gpu=pci.bus_id --format=csv,noheader 2>/dev/null | wc -l)" -eq ${expected_count} ]]; then
        break
    fi
    sleep 1
done
[[ "$(nvidia-smi --query-gpu=pci.bus_id --format=csv,noheader 2>/dev/null | wc -l)" -eq ${expected_count} ]] || \
    die 'second pass did not initialize all GPUs'

for bdf in "${devices[@]}"; do
    read -r speed < "/sys/bus/pci/devices/${bdf}/current_link_speed"
    [[ "${speed}" == '5.0 GT/s PCIe' ]] || die "${bdf} stayed at ${speed}"
    rebar_ctrl="$(setpci -s "${bdf}" ECAP_REBAR+10.l)"
    rebar_size=$(( (0x${rebar_ctrl} >> 8) & 0x1f ))
    [[ ${rebar_size} -eq 14 ]] || \
        die "${bdf} BAR1 ReBAR control stayed at encoding ${rebar_size}"
done

log "PASS: ${expected_count} GPUs are at PCIe Gen2 with 16 GiB BAR1"
