#!/usr/bin/env bash
# Experimental, separately selected CMP50 20GB / kernel-149 P2P build.
# Artifacts only: never installs drivers, edits boot/network settings, or resets GPUs.
set -Eeuo pipefail
readonly repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly kernel="${KERNEL_RELEASE:-$(uname -r)}"
readonly archive_sha=9df87d753cd9c05aa0eedc462af9b35debb549a657136e863282f94c96ee2640
[[ $kernel == 6.8.0-147-generic || $kernel == 6.8.0-149-generic ]] || { echo 'Only kernels 6.8.0-147-generic and 6.8.0-149-generic have been validated for this P2P stage.' >&2; exit 2; }
[[ $# == 2 && $1 == --source-tarball && -f $2 ]] || { echo 'Usage: build-p2p.sh --source-tarball NVIDIA-610.43.03.tar.gz' >&2; exit 2; }
readonly archive=$2
readonly artifact="${CMP50_P2P_ARTIFACT_DIR:-$repo_dir/artifacts/610.43.03-$kernel-p2p}"
[[ ! -e $artifact ]]
printf '%s  %s\n' "$archive_sha" "$archive" | sha256sum -c -
mkdir -p "$repo_dir/work"
readonly work="$(mktemp -d "$repo_dir/work/p2p.XXXXXX")"
tar -xzf "$archive" --strip-components=1 -C "$work"
patches=(01-cmp50-stockflow.patch 02-cmp50-rt-core-count.patch 03-cmp50-rebar-p2p.patch
         04-cmp50-pcie-gen2.patch 06-aikitoria-p2p.patch 07-turing-p2p.patch 08-cmp50-dma32-bootstrap.patch)
for file in "${patches[@]}"; do
  patch --batch --fuzz=0 --dry-run -d "$work" -p1 < "$repo_dir/$file"
  patch --batch --fuzz=0 -d "$work" -p1 < "$repo_dir/$file"
done
make -C "$work" modules -j"${JOBS:-12}" KERNEL_UNAME="$kernel"
mkdir -p "$artifact"
for module in nvidia nvidia-modeset nvidia-drm nvidia-uvm; do
  file="$work/kernel-open/$module.ko"
  [[ $(modinfo -F version "$file") == 610.43.03 ]]
  [[ $(modinfo -F vermagic "$file" | awk '{print $1}') == "$kernel" ]]
  install -m0644 "$file" "$artifact/"
done
# Optional RDMA PEERMEM is not needed for CUDA/NCCL P2P; never copy stale objects.
if [[ -f $work/kernel-open/nvidia-peermem.ko &&
      $(modinfo -F vermagic "$work/kernel-open/nvidia-peermem.ko" | awk '{print $1}') == "$kernel" ]]; then
  install -m0644 "$work/kernel-open/nvidia-peermem.ko" "$artifact/"
fi
(cd "$repo_dir" && sha256sum "${patches[@]}") > "$artifact/source-patches.sha256"
(cd "$artifact" && sha256sum ./*.ko > checksums.sha256)
printf 'P2P_BUILD_PASS\nArtifacts: %s\nSource: %s\n' "$artifact" "$work"
