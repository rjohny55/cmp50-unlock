#!/usr/bin/env bash
set -Eeuo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
kernel_release="$(uname -r)"
mapfile -t gpu_indices < <(nvidia-smi --query-gpu=index,pci.device_id \
    --format=csv,noheader,nounits | awk -F, 'toupper($2) ~ /1E0910DE/ {gsub(/ /,"",$1); print $1}')
[[ ${#gpu_indices[@]} -gt 0 ]] || { echo "no live CMP 50HX GPU found" >&2; exit 1; }

nvidia-smi --query-gpu=index,name,memory.total,pci.bus_id,pcie.link.gen.current,pcie.link.width.current \
    --format=csv,noheader
artifact_probe="$(find -L "${repo_dir}/artifacts" -type f -name rm-issue-rate \
    -path "*${kernel_release}*" | sort | tail -n1)"
if [[ -z "${artifact_probe}" ]]; then
    artifact_probe="$(find -L "${repo_dir}/artifacts" -type f -name rm-issue-rate | \
        sort | tail -n1)"
fi
[[ -x "${artifact_probe}" ]] || { echo "rm-issue-rate artifact not found" >&2; exit 1; }

# The current verifier tool is reliable on GPU index 0. Multi-GPU visibility,
# VRAM and PCIe state are still printed for every card above.
probe_json="$(mktemp)"
trap 'rm -f "${probe_json}"' EXIT
"${artifact_probe}" "${gpu_indices[0]}" > "${probe_json}"
python3 "${repo_dir}/verify/verify.py" "${probe_json}"

mapfile -t memory_totals < <(nvidia-smi --query-gpu=memory.total \
    --format=csv,noheader,nounits)
[[ ${#memory_totals[@]} -eq ${#gpu_indices[@]} ]] || {
    echo "could not read VRAM totals for every GPU" >&2
    exit 1
}
for index in "${!memory_totals[@]}"; do
    memory="${memory_totals[${index}]}"
    [[ "${memory}" -ge 9500 ]] || { echo "GPU ${index}: unexpected VRAM ${memory} MiB" >&2; exit 1; }
done

if systemctl is-enabled cmp50-gen2-second-pass.service >/dev/null 2>&1; then
    systemctl is-active --quiet cmp50-gen2-second-pass.service || {
        echo "Gen2 second-pass service is not active" >&2
        exit 1
    }
    mapfile -t cmp_bdfs < <(lspci -Dn | awk '$3 == "10de:1e09" {print $1}')
    mapfile -t bar1_totals < <(nvidia-smi -q -d MEMORY | \
        awk '/BAR1 Memory Usage/{getline; if ($1 == "Total") print $3}')
    [[ ${#bar1_totals[@]} -eq ${#gpu_indices[@]} ]] || {
        echo "could not read BAR1 totals for every GPU" >&2
        exit 1
    }
    for total in "${bar1_totals[@]}"; do
        [[ "${total}" -ge 16000 ]] || {
            echo "unexpected BAR1 total: ${total} MiB" >&2
            exit 1
        }
    done
    # Without nvidia-persistenced, closing a verifier client can start another
    # GSP init/retrain cycle. Allow the driver's bounded two-second retry to
    # finish before evaluating the final physical link state.
    all_gen2=0
    for _ in {1..15}; do
        all_gen2=1
        for bdf in "${cmp_bdfs[@]}"; do
            read -r speed < "/sys/bus/pci/devices/${bdf}/current_link_speed"
            [[ "${speed}" == '5.0 GT/s PCIe' ]] || all_gen2=0
        done
        [[ ${all_gen2} -eq 1 ]] && break
        sleep 1
    done
    if [[ ${all_gen2} -ne 1 ]]; then
        for bdf in "${cmp_bdfs[@]}"; do
            read -r speed < "/sys/bus/pci/devices/${bdf}/current_link_speed"
            echo "${bdf}: expected Gen2, got ${speed}" >&2
        done
        exit 1
    fi
fi
echo "PASS_CMP50_LIVE: ${#gpu_indices[@]} card(s) visible"
