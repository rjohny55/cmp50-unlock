#!/usr/bin/env bash
# Idle P-state governor for unlocked CMP cards.
#
# Why this exists: CMP 50HX does not lower its own P-state request. Measured
# on 2026-08-24, it sits at P0/1920 MHz/62-64 W at 0 % utilisation. The
# private NVAPI P-state control can force P8/645 MHz/405 MHz/1.8 W, while P16
# returns control to the normal high-performance path.
#
# So this supervisor forces P8 after a debounce of idle polls, and releases
# to P16 the moment work appears.
#
# All state is runtime-only. Stopping the service releases every forced state.
#
# Tunables (environment, or systemd drop-in):
#   CMP_POLL         seconds between samples (default 5). This is also the
#                    worst-case delay before a new job gets full clocks.
#   CMP_IDLE_AFTER   consecutive idle polls before clamping (default 6).
#   CMP_UTIL         utilisation percent still counted as idle (default 5).
#   CMP_LOAD_CORE_OFFSET
#                    core VF offset to restore when work appears. While idle
#                    the offset is set to 0, because a non-zero core offset
#                    pins the card in P0. It is dropped before forcing P8 and
#                    restored when P16 is requested.
#                    Written automatically by "cmp-tune apply".
#   CMP_LOAD_CLOCK   what to restore when work appears, as "MIN,MAX" or a
#                    single MHz value. Unset (default) means reset the lock
#                    entirely. Set this to the clock range your tuning profile
#                    uses, so the governor hands the card back in that state
#                    instead of clearing it. The lock is cleared while P8 is
#                    forced, then restored on load.
#
# Usage: cmp-idle-governor.sh            run the governor
#        cmp-idle-governor.sh --release  release every GPU and exit
set -u

POLL="${CMP_POLL:-5}"
IDLE_AFTER="${CMP_IDLE_AFTER:-6}"
UTIL_THRESHOLD="${CMP_UTIL:-5}"
LOAD_CLOCK="${CMP_LOAD_CLOCK:-}"
LOAD_CORE_OFFSET="${CMP_LOAD_CORE_OFFSET:-}"
CMP_TUNE="${CMP_TUNE:-/usr/local/bin/cmp-tune}"
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
PSTATE_HELPER="${CMP_PSTATE_HELPER:-${SCRIPT_DIR}/cmp-pstate.py}"

log() { printf '%s cmp-idle-governor: %s\n' "$(date -Is)" "$*"; }

command -v nvidia-smi >/dev/null || { log "nvidia-smi not found"; exit 1; }
command -v python3 >/dev/null || { log "python3 not found"; exit 1; }

# Only CMP cards are managed. A box can hold a display card or another
# accelerator, and clamping those is not wanted. Override with CMP_GPUS as a
# comma-separated index list if you need to.
# nvidia-smi reports pci.device_id as 0xDDDDVVVV (device, then vendor).
SUPPORTED_PCI_IDS="0x1E0910DE 0x220D10DE"   # CMP 50HX, CMP 90HX

select_gpus() {
    if [[ -n "${CMP_GPUS:-}" ]]; then
        tr ',' ' ' <<< "${CMP_GPUS}"
        return
    fi
    while IFS=', ' read -r idx pciid; do
        [[ -n "${idx:-}" && -n "${pciid:-}" ]] || continue
        for want in ${SUPPORTED_PCI_IDS}; do
            if [[ "${pciid^^}" == "${want^^}" ]]; then
                printf '%s
' "$idx"
                break
            fi
        done
    done < <(nvidia-smi --query-gpu=index,pci.device_id                         --format=csv,noheader,nounits)
}

mapfile -t GPUS < <(select_gpus)
if [[ ${#GPUS[@]} -eq 0 ]]; then
    log "no supported CMP card found (CMP 50HX / CMP 90HX); nothing to manage"
    exit 0
fi

gpu_bus_id() {
    local idx="$1"
    nvidia-smi -i "$idx" --query-gpu=pci.bus_id \
        --format=csv,noheader,nounits 2>/dev/null | tr -d '[:space:]'
}

# A non-zero core VF offset pins the card in P0. Dropping the offset while idle
# lets the card reach P8. Only meaningful when a tuning profile set an offset
# in the first place.
set_core_offset() {
    local idx="$1" value="$2"
    [[ -n "${LOAD_CORE_OFFSET}" ]] || return 0
    [[ -x "${CMP_TUNE}" ]] || return 0
    "${CMP_TUNE}" -i "$idx" core-offset "$value" --quiet >/dev/null 2>&1
}

# The private NVAPI entry point is also used by nvidia-pstated. Match the
# physical GPU by PCI bus because NVAPI handle order is not the Linux GPU index.
set_pstate() {
    local idx="$1" pstate="$2" bus="${gpu_bus[$idx]:-}"
    [[ -r "${PSTATE_HELPER}" && -n "${bus}" ]] || return 1
    python3 "${PSTATE_HELPER}" --bus-id "${bus}" --pstate "${pstate}" \
        >/dev/null 2>&1
}

# Hand the card back to the normal high-performance path. An explicit tuning
# range is restored after P16; otherwise the GPU clock lock is cleared.
release_gpu() {
    local idx="$1"
    set_core_offset "$idx" "${LOAD_CORE_OFFSET:-0}" || return 1
    if [[ -n "${LOAD_CLOCK}" ]]; then
        nvidia-smi -i "$idx" -lgc "${LOAD_CLOCK}" >/dev/null 2>&1 || return 1
    else
        nvidia-smi -i "$idx" -rgc >/dev/null 2>&1 || return 1
    fi
    set_pstate "$idx" 16
}

declare -A gpu_bus
for g in "${GPUS[@]}"; do
    gpu_bus["$g"]="$(gpu_bus_id "$g")"
    [[ -n "${gpu_bus[$g]}" ]] || log "GPU $g: cannot read PCI bus ID"
done

# --release: used by the unit's ExecStopPost as a safety net, so a killed
# governor never leaves the card stuck in P8.
if [[ "${1:-}" == "--release" ]]; then
    for g in "${GPUS[@]}"; do
        release_gpu "$g" && log "GPU $g: released (--release)"
    done
    exit 0
fi

declare -A idle_count state
for g in "${GPUS[@]}"; do
    idle_count["$g"]=0
    state["$g"]=free
done
log "managing CMP GPU indices: ${GPUS[*]} (force P8 after $((POLL * IDLE_AFTER))s idle, P16 on load${LOAD_CLOCK:+, restoring ${LOAD_CLOCK} MHz lock}${LOAD_CORE_OFFSET:+, core offset ${LOAD_CORE_OFFSET}})"

release_all() {
    for g in "${GPUS[@]}"; do
        [[ "${state[$g]:-free}" == low ]] || continue
        release_gpu "$g" && log "GPU $g: released on exit"
    done
    exit 0
}
trap release_all TERM INT EXIT

while true; do
    while IFS=', ' read -r idx util; do
        [[ -n "${idx:-}" && -n "${util:-}" ]] || continue
        [[ -n "${state[$idx]+x}" ]] || continue
        if [[ "$util" -le "$UTIL_THRESHOLD" ]]; then
            idle_count["$idx"]=$(( idle_count[$idx] + 1 ))
            if [[ "${state[$idx]}" == free
                  && "${idle_count[$idx]}" -ge "$IDLE_AFTER" ]]; then
                # Remove a profile's clock lock before forcing P8. It is
                # restored by release_gpu when work appears.
                if set_core_offset "$idx" 0 \
                        && nvidia-smi -i "$idx" -rgc >/dev/null 2>&1 \
                        && set_pstate "$idx" 8; then
                    state["$idx"]=low
                    log "GPU $idx: idle, forced P8 (645 MHz core / 405 MHz memory)${LOAD_CORE_OFFSET:+ (core offset dropped)}"
                else
                    set_core_offset "$idx" "${LOAD_CORE_OFFSET:-0}" >/dev/null 2>&1 || true
                    log "GPU $idx: P8 request failed"
                fi
            fi
        else
            idle_count["$idx"]=0
            if [[ "${state[$idx]}" == low ]]; then
                if release_gpu "$idx"; then
                    state["$idx"]=free
                    log "GPU $idx: busy (${util}%), released to full clocks"
                else
                    log "GPU $idx: release failed"
                fi
            fi
        fi
    done < <(nvidia-smi --query-gpu=index,utilization.gpu \
                        --format=csv,noheader,nounits)
    sleep "$POLL"
done
