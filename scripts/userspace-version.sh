#!/usr/bin/env bash
# Unlike nvidia-smi --version, this check works before the first module load.
# Validate the actual 64-bit library selected by the dynamic loader, not merely
# a downloaded installer or a different driver left elsewhere on the disk.
cmp50_userspace_matches() {
    local expected=$1 library candidate resolved found
    [[ "$expected" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || return 1
    command -v nvidia-smi >/dev/null || return 1
    command -v nvidia-modprobe >/dev/null || return 1
    for library in libnvidia-ml libcuda; do
        found=0
        while IFS= read -r candidate; do
            [[ -f "$candidate" ]] || continue
            resolved=$(readlink -f -- "$candidate") || return 1
            # The first existing x86-64 candidate is the loader's selection.
            [[ "$(basename -- "$resolved")" == "$library.so.$expected" ]] || return 1
            found=1
            break
        done < <(ldconfig -p | awk -v name="$library.so.1" \
            '$1 == name && /x86-64/ {print $NF}')
        [[ "$found" -eq 1 ]] || return 1
    done
}
