#!/usr/bin/env bash
# Install only the governor; never changes/unloads the kernel driver or Gen2.
set -Eeuo pipefail
repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
data_dir=/opt/stacks/ai-server-manager/data
start_now=0
die() { printf '[cmp50-governor-install] ERROR: %s\n' "$*" >&2; exit 1; }
while [[ $# -gt 0 ]]; do
    case "$1" in
        --ai-manager-data-dir) data_dir="${2:?missing data directory}"; shift 2 ;;
        --start-now) start_now=1; shift ;;
        -h|--help)
            printf 'usage: sudo ./install-governor.sh [--start-now] [--ai-manager-data-dir PATH]\nInstalls/enables for next boot; preserves existing control and environment settings.\n'
            exit 0 ;;
        *) die "unknown option: $1" ;;
    esac
done
[[ ${EUID} -eq 0 ]] || die 'run as root (sudo)'
[[ $(uname -m) == x86_64 ]] || die 'bundled binary requires linux/amd64'
[[ ${data_dir} == /* && ${data_dir} != *[!A-Za-z0-9_./-]* ]] || die 'invalid absolute data directory'
for required in lspci systemctl sha256sum install flock; do
    command -v "${required}" >/dev/null || die "missing command: ${required}"
done
exec 9>/run/lock/cmp50-governor-install.lock
flock -n 9 || die 'another governor installer is running'
source "${repo_dir}/scripts/governor-profile.sh"
profile="$(cmp50_governor_profile)" || die 'unsupported GPU configuration'
binary="${repo_dir}/idle-governor/bin/cmp-idle-governor-linux-amd64"
[[ -x ${binary} ]] || die 'bundled governor binary is missing'
(cd "${repo_dir}/idle-governor" && sha256sum -c checksums.sha256)
# A checksum alone cannot detect a binary linked to an absent musl loader.
"${binary}" --version >/dev/null || die 'governor cannot execute on this host; rebuild with idle-governor/build.sh'
if [[ ${start_now} -eq 1 ]]; then
    nvidia-smi --query-gpu=driver_version --format=csv,noheader | \
        awk 'NF { if ($0 != "610.43.03") exit 1; n++ } END { if (!n) exit 1 }' || \
        die 'start-now requires a working NVIDIA 610.43.03 driver'
fi
control_dir="${data_dir}/cmp50-power"
[[ ! -L ${control_dir} && ! -L ${control_dir}/control.json && ! -L /etc/default/cmp-idle-governor ]] || \
    die 'refusing symlinked configuration targets'
install -d -m 0755 "${control_dir}"
# Do not silently replace the panel's request, including an explicit disabled state.
if [[ ! -e ${control_dir}/control.json ]]; then
    temp_control="$(mktemp "${control_dir}/.control.XXXXXX")"
    trap 'rm -f -- "${temp_control:-}"' EXIT
    printf '{"enabled":true,"profile":"%s","idle_fan_percent":0}\n' "${profile}" > "${temp_control}"
    chmod 0644 "${temp_control}"
    # Preserve the panel data directory owner so an unprivileged panel can edit it.
    chown --reference="${data_dir}" "${control_dir}" "${temp_control}"
    ln "${temp_control}" "${control_dir}/control.json"
    rm -f -- "${temp_control}"
fi
if [[ ! -e /etc/default/cmp-idle-governor ]]; then
    printf 'CMP50_POWER_CONTROL_DIR=%s\n' "${control_dir}" > /etc/default/cmp-idle-governor
    chmod 0644 /etc/default/cmp-idle-governor
else
    # Never point a restarted service at a different panel's control file.
    grep -Fxq "CMP50_POWER_CONTROL_DIR=${control_dir}" /etc/default/cmp-idle-governor || \
        die 'existing environment uses a different control path; reconcile it first'
fi
if systemctl is-active --quiet cmp-idle-governor.service; then
    start_now=1
    systemctl stop cmp-idle-governor.service
fi
install -m 0755 "${binary}" /usr/local/sbin/cmp-idle-governor
install -m 0644 "${repo_dir}/idle-governor/cmp-idle-governor.service" /etc/systemd/system/cmp-idle-governor.service
if systemctl cat cmp-idle-governor-control.service >/dev/null 2>&1; then
    systemctl disable --now cmp-idle-governor-control.service
    rm -f /etc/systemd/system/cmp-idle-governor-control.service /usr/local/libexec/cmp-idle-governor-control
fi
systemctl daemon-reload
systemctl enable cmp-idle-governor.service
if [[ ${start_now} -eq 1 ]]; then
    systemctl start cmp-idle-governor.service
    sleep 2
    systemctl is-active --quiet cmp-idle-governor.service || die 'governor failed to start; inspect journalctl -u cmp-idle-governor'
    printf 'PASS_CMP50_GOVERNOR_STARTED (check active-profile/last-error and idle/load behavior)\n'
else
    printf 'PASS_CMP50_GOVERNOR_INSTALLED (enabled for next boot)\n'
fi
