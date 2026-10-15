#!/usr/bin/env bash
# Explicit opt-in installer: patched driver + Gen2 boot service + idle governor.
set -Eeuo pipefail
repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
data_dir=/opt/stacks/ai-server-manager/data
allow_gen2=0
install_deps=0
die() { printf '[cmp50-all] ERROR: %s\n' "$*" >&2; exit 1; }
while [[ $# -gt 0 ]]; do
    case "$1" in
        --allow-experimental-gen2) allow_gen2=1; shift ;;
        --install-deps) install_deps=1; shift ;;
        --ai-manager-data-dir) data_dir="${2:?missing data directory}"; shift 2 ;;
        -h|--help)
            printf 'usage: sudo ./install-all.sh --allow-experimental-gen2 [--install-deps] [--ai-manager-data-dir PATH]\nStages NVIDIA 610.43.03 + Gen2 + enabled idle governor. Never reboots or unloads the driver.\nRequires supported X99 hardware and local recovery access.\n'
            exit 0 ;;
        *) die "unknown option: $1" ;;
    esac
done
[[ ${allow_gen2} -eq 1 ]] || die 'Gen2 requires --allow-experimental-gen2 and local recovery access'
[[ ${EUID} -eq 0 ]] || die 'run as root (sudo)'
case "$(uname -r)" in
    6.8.0-137-generic|6.8.0-147-generic) ;;
    *) die 'Gen2 supports only kernels 6.8.0-137-generic and 6.8.0-147-generic' ;;
esac
[[ ${data_dir} == /* && ${data_dir} != *[!A-Za-z0-9_./-]* ]] || die 'invalid absolute data directory'
[[ $(uname -m) == x86_64 ]] || die 'bundled governor requires linux/amd64'
if [[ ${install_deps} -eq 1 ]]; then
    apt-get update
    apt-get install -y build-essential "linux-headers-$(uname -r)" pciutils psmisc util-linux curl ca-certificates nvidia-modprobe initramfs-tools
fi
command -v flock >/dev/null || die 'missing flock; use --install-deps on Ubuntu/Debian'
exec 8>/run/lock/cmp50-all-install.lock
flock -n 8 || die 'another all-in-one installer is running'
source "${repo_dir}/scripts/governor-profile.sh"
cmp50_governor_profile >/dev/null || die 'unsupported governor GPU configuration'
(cd "${repo_dir}/idle-governor" && sha256sum -c checksums.sha256)
"${repo_dir}/idle-governor/bin/cmp-idle-governor-linux-amd64" --version >/dev/null || die 'bundled governor cannot execute on this host'
args=(--stage gen2 --allow-experimental-gen2 --initramfs --disable-nouveau)
source "${repo_dir}/scripts/userspace-version.sh"
if ! cmp50_userspace_matches 610.43.03; then
    args+=(--install-userland)
fi
bash "${repo_dir}/install.sh" "${args[@]}"
bash "${repo_dir}/install-governor.sh" --ai-manager-data-dir "${data_dir}"
printf '\nPASS_CMP50_ALL_STAGED\nReboot manually when safe, then check:\n  sudo ./verify-live.sh\n  python3 ./verify/cuda-smoke.py\n  systemctl status cmp50-gen2-second-pass cmp-idle-governor\n  journalctl -u cmp-idle-governor -b\nExisting governor control.json is preserved; new installations enable automatic fans and idle P8.\n'
