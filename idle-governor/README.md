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

- Read driver-maintained NVML GPU/memory-controller history once every 30 seconds.
  Any nonzero activity in the interval resets the idle countdown, even if the
  instantaneous utilization at the poll is zero. Allocated VRAM is not activity.
- After two fully quiet intervals (at least 60 seconds), reset the GPU clock lock and
  request P8 through the private NVAPI entry point.
- On load, request P16 and restore either the configured clock lock or automatic
  clocks within one poll. With the 30-second poll a newly started job can wait
  up to 30 seconds for release from P8; this is not an instantaneous wake policy.
- Optionally set the panel-selected fan percentage only while the card is idle
  and cooler than 55 C. Restore NVIDIA automatic fan control on load, overheat,
  invalid configuration, service stop, or error.
- Manage every card independently in one event loop.
- On service start and in `ExecStopPost`, forcibly clear stale P8/manual-fan
  state left by a killed older process.

The measured reference result remains P8 at 645/405 MHz with host-dependent idle power, with P0/full clocks restored under load. The four-card 20 GB system
was validated with Gen2 and 32 GiB BAR1 active.

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

The ready 1.0.2-nvml-activity binary targets the glibc loader at
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
| `CMP_POLL` | `30` | NVML-history poll interval and maximum load-release delay, seconds |
| `CMP_IDLE_AFTER` | `2` | Consecutive fully quiet intervals before P8 |
| `CMP_UTIL` | `0` | GPU activity threshold; keep zero to count even 1% as busy |
| `CMP_FAN_AUTO_TEMP` | `55` | Temperature that restores automatic fan control |
| `CMP_LOAD_CLOCK` | unset | Clock to restore (`MHz` or `MIN,MAX`) |
| `CMP_LOAD_CORE_OFFSET` | unset | Core VF offset restored on load; temporarily zeroed for P8 |

Power limits and memory VF offsets are never changed. A configured core VF
offset is temporarily set to zero for P8 and restored on load. The old external
`cmp-tune` subprocess hook was removed so the daemon never executes
configuration-derived commands.

No model ports, HTTP metrics, panel API, per-request lease files or heartbeat
writes are used. History is read directly from NVML. The tested 610.43.03/CMP50
host keeps about 70 seconds of samples, sufficient for the 30-second interval.
Missing, stale, unsupported or truncated history is treated as unknown: never
force P8 based on it. No code infers an active request from allocated VRAM.

A request waiting entirely on CPU with no sampled GPU/memory activity for the
whole idle window remains indistinguishable from idle hardware. This policy
is not a claim that P8 is prohibited throughout every HTTP request.

`legacy/` contains the same policy for an existing Bash/NVAPI installation,
without migrating its fan, tuning or panel-control settings. Install the ready
binary as `/usr/local/libexec/cmp-governor-nvml-sample`, the legacy script at its
existing path beside `cmp-pstate.py`, and the provided systemd drop-in. The
`--sample-activity` helper opens NVML read-only, prints interval peaks, and exits;
it does not load NVAPI or change power/clocks. The service invokes it only once
per 30-second poll. Back up the original script/unit before replacement.

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
