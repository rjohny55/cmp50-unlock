# CMP50 governor for AI Server Manager

`cmp-idle-governor` is a single Linux/amd64 Go binary and systemd service. It
replaces the former Bash supervisor and Python NVAPI helper. No Python runtime,
Xorg, Coolbits, or privileged AI Server Manager container is required.

The host daemon reads the panel's validated request from:

```text
/opt/stacks/ai-server-manager/data/cmp50-power/control.json
```

It accepts only fixed profiles for 1, 2, 4, 6, or 8 contiguous CMP 50HX 20 GB
cards. It verifies PCI device `10de:1e09`, the supported subsystem and VRAM
before applying a request. The daemon writes `active-profile`,
`active-fan-percent`, or `last-error` beside the control file so the panel shows
the real host state.

## Behavior

- After six idle polls (30 seconds by default), reset the GPU clock lock and
  request P8 through the private NVAPI entry point.
- On load, request P16 and restore either the configured clock lock or automatic
  clocks within one poll.
- Optionally set the panel-selected fan percentage only while the card is idle
  and cooler than 55 C. Restore NVIDIA automatic fan control on load, overheat,
  invalid configuration, service stop, or error.
- Manage every card independently in one event loop.
- On service start and in `ExecStopPost`, forcibly clear stale P8/manual-fan
  state left by a killed older process.

The measured reference result remains P8 at 645/405 MHz and about 1.8 W per
idle card, with P0/full clocks restored under load. The four-card 20 GB system
was validated with Gen2 and 16 GiB BAR1 active.

## Install with the driver patch

```sh
# Full, explicitly acknowledged Gen2 installation (20 GB / supported X99/kernel):
sudo ./install-all.sh --install-deps --allow-experimental-gen2
# Or add the governor without touching an already working driver/Gen2:
sudo ./install-governor.sh --start-now
```

For a non-default AI Server Manager data directory:

```sh
sudo ./install-governor.sh --start-now \
  --ai-manager-data-dir /opt/stacks/ai-server-manager/data
```

The installer verifies `checksums.sha256`, installs the ready binary as
`/usr/local/sbin/cmp-idle-governor`, installs the systemd unit, and enables it
for the next boot (`--start-now` starts it immediately on a working 610.43.03
driver). A missing control.json is created with the detected 1/2/4/6/8-card
profile, enabled=true, and automatic fans. Existing control.json and environment
settings are preserved; an existing disabled control is not silently enabled.
It also removes the obsolete two-service panel controller
when present. The NVIDIA driver is not unloaded by the installer.

The legacy `install.sh --idle-governor` delegates to the same installer. Always
pass the intended driver stage explicitly: the default `rt` does not preserve
an installed Gen2 stage. The standalone governor installer never changes it.

The ready 1.0.1 binary targets the glibc loader at
`/lib64/ld-linux-x86-64.so.2`, not Alpine's musl loader. CGO_ENABLED=0 by itself
does not prevent the purego dynamic-loader path from selecting the build host's
loader. The build script pins the Ubuntu/Debian interpreter; Alpine hosts are
not runtime targets. The installer runs `--version` before writing the service.

```sh
systemctl status cmp-idle-governor
journalctl -u cmp-idle-governor -f
```

## Settings

`/etc/default/cmp-idle-governor` contains `CMP50_POWER_CONTROL_DIR`. Optional
systemd environment overrides are:

| Variable | Default | Meaning |
|---|---:|---|
| `CMP_POLL` | `5` | Poll interval and maximum load-release delay, seconds |
| `CMP_IDLE_AFTER` | `6` | Consecutive idle polls before P8 |
| `CMP_UTIL` | `5` | Utilization still treated as idle, percent |
| `CMP_FAN_AUTO_TEMP` | `55` | Temperature that restores automatic fan control |
| `CMP_LOAD_CLOCK` | unset | Clock to restore (`MHz` or `MIN,MAX`) |
| `CMP_LOAD_CORE_OFFSET` | unset | Core VF offset restored on load; temporarily zeroed for P8 |

Power limits and memory VF offsets are never changed. A configured core VF
offset is temporarily set to zero for P8 and restored on load. The old external
`cmp-tune` subprocess hook was removed so the daemon never executes
configuration-derived commands.

## Build and verification

The module is pinned to Go 1.26.6, matching AI Server Manager:

```sh
go test ./...
go vet ./...
go test -race ./...
bash ./build.sh
sha256sum -c checksums.sha256
```

On the Ubuntu target, `bin/cmp-idle-governor-linux-amd64 --version` must run
without loading the NVIDIA APIs. A short optional wake-up/correctness check is
`python3 verify/cuda-smoke.py --load-seconds 10` from the repository root; it
runs on each supported card sequentially and is not a throughput benchmark.

The NVIDIA ABI boundary is isolated in `internal/nvidia`; the policy and file
handling use ordinary safe Go. Dynamic libraries are opened once and closed on
exit, tickers and signal contexts are stopped, and the daemon creates no
per-poll goroutines. Hardware-independent tests use a synchronized fake NVML
backend and are run under the Go race detector in CI.
