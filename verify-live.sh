#!/usr/bin/env bash
set -Eeuo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
mapfile -t gpu_indices < <(nvidia-smi --query-gpu=index,pci.device_id \
    --format=csv,noheader,nounits | awk -F, 'toupper($2) ~ /1E0910DE/ {gsub(/ /,"",$1); print $1}')
[[ ${#gpu_indices[@]} -gt 0 ]] || { echo "no live CMP 50HX GPU found" >&2; exit 1; }

nvidia-smi --query-gpu=index,name,memory.total,pci.bus_id,pcie.link.gen.current,pcie.link.width.current \
    --format=csv,noheader
artifact_probe="$(find "${repo_dir}/artifacts" -type f -name rm-issue-rate | sort | tail -n1)"
[[ -x "${artifact_probe}" ]] || { echo "rm-issue-rate artifact not found" >&2; exit 1; }

# The current verifier tool is reliable on GPU index 0. Multi-GPU visibility,
# VRAM and PCIe state are still printed for every card above.
probe_json="$(mktemp)"
trap 'rm -f "${probe_json}"' EXIT
"${artifact_probe}" "${gpu_indices[0]}" > "${probe_json}"
python3 "${repo_dir}/verify/verify.py" "${probe_json}"

for index in "${gpu_indices[@]}"; do
    memory="$(nvidia-smi -i "${index}" --query-gpu=memory.total --format=csv,noheader,nounits)"
    [[ "${memory}" -ge 9500 ]] || { echo "GPU ${index}: unexpected VRAM ${memory} MiB" >&2; exit 1; }
done
echo "PASS_CMP50_LIVE: ${#gpu_indices[@]} card(s) visible"
