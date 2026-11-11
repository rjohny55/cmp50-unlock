# Standalone CMP50 clock governor

The Linux/amd64 Go binary runs as `cmp-idle-governor.service` on the host.
It does not need the NeithOS panel, Docker, Xorg, Python, HTTP endpoints or
an inference engine to run. Install it directly from this GitHub repository.
No governor binary belongs in panel images.

## Current policy: P8 temporarily disabled

**Forced P8, P3 and the private fallback/16 transition are disabled.** On the
tested CMP50 system with NVIDIA 610.43.03 open modules, forced P3 and returning
to automatic performance after forced P8 caused GSP/PMU failures (Xid 62/154).
Explicit P0 alone restored memory frequency but left the core at 645 MHz,
even under load. The binary no longer loads NvAPI or issues private P-state RPCs.

The replacement policy uses documented NVML clock controls:

- Idle: limit the graphics clock to one third of each card's reported maximum
  (700 MHz request / 705 MHz observed on the tested cards).
- Work: remove the idle limit and allow NVIDIA's stock automatic boost. Explicit
  user load-clock limits and core offsets remain supported when configured.
- Keep working clocks for **30 seconds** after the last GPU or memory-controller
  activity, including utilization peaks retained in NVML history. Poll once per
  second. Allocated model VRAM is not treated as ongoing activity.
- Long idle stays in the same safe clock policy; **there is no P8 timer**.
- Report the observed P-state from NVML; never infer it from the policy name.
- Idle fan settings respect the temperature guard and return to automatic mode
  on work, service exit, errors or disabling the policy.

A new request can wait up to one poll for its idle clock ceiling to be removed.
This is not a pre-request queue or a dependency on the panel. Latency on full
LLM workloads remains separate from the GPU frequency qualification.

## Install independently

```sh
git clone https://github.com/rjohny55/cmp50-unlock.git
cd cmp50-unlock
sudo ./install-governor.sh --start-now
```

New standalone installations use `/var/lib/cmp-idle-governor/control.json` and
create an enabled profile after checking the GPU PCI/subsystem identity.
An existing `/etc/default/cmp-idle-governor` and control file are preserved,
including disabled state and fan configuration. No panel installation is needed.

For a custom independent directory:

```sh
sudo ./install-governor.sh --start-now --control-dir /var/lib/my-cmp-governor
```

The legacy `--ai-manager-data-dir PATH` option remains available for an existing
shared configuration. The installer does not unload the driver or reboot.
A legacy running governor is upgraded atomically; its unsafe deferred private
P-state cleanup is skipped, and ExecStopPost uses the new NVML-only release.

## Settings

Environment: `/etc/default/cmp-idle-governor`.

| Variable | Default | Meaning |
|---|---|---|
| `CMP50_POWER_CONTROL_DIR` | `/var/lib/cmp-idle-governor` | Standalone control/status directory |
| `CMP_POLL` | `1` | GPU activity poll interval, seconds |
| `CMP_IDLE_DELAY_SECONDS` | `30` | Quiet interval before lowering graphics clocks |
| `CMP_UTIL` | `0` | GPU activity threshold; memory-controller activity also prevents idle |
| `CMP_WARM_CLOCK` | reported maximum / 3 | Idle graphics MHz, or MIN,MAX |
| `CMP_LOAD_CLOCK` | unset | Optional explicit load clock range; otherwise stock automatic clocks |
| `CMP_LOAD_CORE_OFFSET` | unset | Optional active core offset; cleared when idle |
| `CMP_FAN_AUTO_TEMP` | `55` | Temperature restoring automatic fan control |

`CMP_IDLE_AFTER` is retained only as a compatibility input. The old poll-count
P8 policy and `CMP_DEEP_IDLE_SECONDS` do not enable P8. Existing custom `CMP_POLL`
values are preserved by the installer and change the effective reaction time.
No voltage, memory offset or power limit is changed.

```sh
systemctl status cmp-idle-governor
journalctl -u cmp-idle-governor -f
cat /var/lib/cmp-idle-governor/power-stages.json
```

## Build and verification

Go 1.27.1; Ubuntu/Debian glibc Linux/amd64:

```sh
cd idle-governor
go test -race ./...
go vet ./...
./build.sh
sha256sum -c checksums.sha256
```

See [hardware qualification](../docs/GOVERNOR_CLOCK_POLICY_20261111.md) for
measured frequencies, telemetry limits and rejected P-state transitions.
