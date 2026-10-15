#!/usr/bin/env bash
# Source this helper. Only complete, homogeneous CMP50 configurations qualify.
cmp50_governor_profile() {
    local sysfs_root="${1:-/sys/bus/pci/devices}" bdf class device rest vendor subsystem count=0
    local inventory
    inventory="$(lspci -Dn)" || return 1
    while read -r bdf class device rest; do
        [[ ${class} == 03* && ${device} == 10de:* ]] || continue
        [[ ${device} == 10de:1e09 ]] || {
            printf 'Governor refuses a mixed NVIDIA GPU configuration\n' >&2; return 1;
        }
        read -r vendor < "${sysfs_root}/${bdf}/subsystem_vendor" || return 1
        read -r subsystem < "${sysfs_root}/${bdf}/subsystem_device" || return 1
        case "${vendor}:${subsystem}" in
            0x10de:0x1554|0x1462:0x371f) ;;
            *) printf 'Unsupported CMP50 subsystem at %s\n' "${bdf}" >&2; return 1 ;;
        esac
        count=$((count + 1))
    done <<< "${inventory}"
    case ${count} in
        1|2|4|6|8) printf 'cmp50-%sx20gb\n' "${count}" ;;
        *) printf 'Governor requires 1, 2, 4, 6 or 8 supported CMP50 GPUs; found %s\n' "${count}" >&2; return 1 ;;
    esac
}
