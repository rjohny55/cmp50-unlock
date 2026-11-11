#!/usr/bin/env bash
# Install only the governor; never changes/unloads the kernel driver or Gen2.
set -Eeuo pipefail
repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
data_dir=
control_dir=
explicit_control=0
start_now=0
die() { printf '[cmp50-governor-install] ERROR: %s\n' "$*" >&2; exit 1; }
while [[ $# -gt 0 ]]; do
    case "$1" in
        --ai-manager-data-dir) data_dir="${2:?missing data directory}"; control_dir="${data_dir}/cmp50-power"; explicit_control=1; shift 2 ;;
        --control-dir) control_dir="${2:?missing control directory}"; explicit_control=1; shift 2 ;;
        --start-now) start_now=1; shift ;;
        -h|--help)
            printf 'usage: sudo ./install-governor.sh [--start-now] [--control-dir PATH] [--ai-manager-data-dir PATH]\nInstalls/enables for next boot; preserves existing control and environment settings.\n'
            exit 0 ;;
        *) die "unknown option: $1" ;;
    esac
done
[[ ${EUID} -eq 0 ]] || die 'run as root (sudo)'
[[ $(uname -m) == x86_64 ]] || die 'bundled binary requires linux/amd64'
if [[ -z ${control_dir} && -f /etc/default/cmp-idle-governor ]]; then
    control_dir="$(sed -n 's/^CMP50_POWER_CONTROL_DIR=//p' /etc/default/cmp-idle-governor)"
fi
control_dir=${control_dir:-/var/lib/cmp-idle-governor}
[[ ${control_dir} == /* && ${control_dir} != *[!A-Za-z0-9_./-]* ]] || die 'invalid absolute control directory'
[[ -z ${data_dir} || (${data_dir} == /* && ${data_dir} != *[!A-Za-z0-9_./-]*) ]] || die 'invalid absolute data directory'
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
[[ ! -L ${control_dir} && ! -L ${control_dir}/control.json && ! -L /etc/default/cmp-idle-governor ]] || \
    die 'refusing symlinked configuration targets'
install -d -m 0755 "${control_dir}"
if [[ -n ${data_dir} ]]; then install -d -m 0755 "${data_dir}"; fi
# Do not silently replace the panel's request, including an explicit disabled state.
if [[ ! -e ${control_dir}/control.json ]]; then
    temp_control="$(mktemp "${control_dir}/.control.XXXXXX")"
    trap 'rm -f -- "${temp_control:-}"' EXIT
    printf '{"enabled":true,"profile":"%s","idle_fan_percent":0}\n' "${profile}" > "${temp_control}"
    chmod 0644 "${temp_control}"
    # Preserve the panel data directory owner so an unprivileged panel can edit it.
    if [[ -n ${data_dir} ]]; then chown --reference="${data_dir}" "${control_dir}" "${temp_control}"; fi
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
was_active=0
legacy_active=0
if systemctl is-active --quiet cmp-idle-governor.service; then
    was_active=1
    start_now=1
    if ! /usr/local/sbin/cmp-idle-governor --capabilities 2>/dev/null | grep -q '"forced_pstates":false'; then legacy_active=1; fi
fi
"${binary}" --capabilities | grep -q '"forced_pstates":false' || die 'new binary does not advertise safe NVML-only controls'
# Atomic replacement avoids ETXTBSY and makes ExecStopPost use the safe release.
temp_binary="$(mktemp /usr/local/sbin/.cmp-idle-governor.XXXXXX)"
trap 'rm -f -- "${temp_control:-}" "${temp_binary:-}"' EXIT
install -m 0755 "${binary}" "${temp_binary}"
mv -f -- "${temp_binary}" /usr/local/sbin/cmp-idle-governor
if [[ ${was_active} -eq 1 ]]; then
    if [[ ${legacy_active} -eq 1 ]]; then
        # Skip only the old governor's unsafe private P-state deferred cleanup.
        systemctl kill --kill-whom=main --signal=SIGKILL cmp-idle-governor.service
    fi
    systemctl stop cmp-idle-governor.service
fi
reload=0
if ! cmp -s "${repo_dir}/idle-governor/cmp-idle-governor.service" /etc/systemd/system/cmp-idle-governor.service; then
    install -m 0644 "${repo_dir}/idle-governor/cmp-idle-governor.service" /etc/systemd/system/cmp-idle-governor.service
    reload=1
fi
# This repository-owned legacy drop-in forced 30-second polling; leave user
# drop-ins intact and remove only the retired history-helper settings.
legacy_dropin=/etc/systemd/system/cmp-idle-governor.service.d/90-nvml-activity.conf
if [[ -f ${legacy_dropin} ]]; then
    rm -f -- "${legacy_dropin}"
    reload=1
fi
if systemctl cat cmp-idle-governor-control.service >/dev/null 2>&1; then
    systemctl kill --kill-whom=all --signal=SIGKILL cmp-idle-governor-control.service || true
    systemctl disable --now cmp-idle-governor-control.service
    rm -f /etc/systemd/system/cmp-idle-governor-control.service /usr/local/libexec/cmp-idle-governor-control
    reload=1
fi
if [[ ${reload} -eq 1 ]]; then systemctl daemon-reload; fi
systemctl enable cmp-idle-governor.service
if [[ ${start_now} -eq 1 ]]; then
    systemctl start cmp-idle-governor.service
    sleep 2
    systemctl is-active --quiet cmp-idle-governor.service || die 'governor failed to start; inspect journalctl -u cmp-idle-governor'
    printf 'PASS_CMP50_GOVERNOR_STARTED (check active-profile/last-error and idle/load behavior)\n'
else
    printf 'PASS_CMP50_GOVERNOR_INSTALLED (enabled for next boot)\n'
fi
