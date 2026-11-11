# CMP50 Unlock

[Русский](README_RU.md) · [P2P details](docs/P2P.md) · [Power governor](idle-governor/README.md)

Open NVIDIA driver patches for modified **CMP 50HX 20 GB / TU102 (SM75)** boards.
Current validated configuration: **Ubuntu 24.04, kernel 6.8.0-149-generic,
NVIDIA open kernel modules and userspace 610.43.03**.

## Current result

Checked on the four-card host on 9 November 2026:

| Item | Result |
| --- | --- |
| GPU memory | 4 × 20480 MiB |
| BAR1 aperture | **32768 MiB per GPU** (32 GiB; this is an aperture, not additional VRAM) |
| PCIe | All four links at Gen2 x16 |
| CUDA peer capability | All 12 directed GPU pairs report access |
| Gen2 boot service | Completed successfully on kernel 6.8.0-149 |
| Idle governor | Standalone NVML clock control; 30 s idle delay, stock automatic boost on load. P8 temporarily disabled after CMP50 GSP wake failures. |

The current P2P patch path uses a 32 GiB BAR1 aperture and DMA32-safe bootstrap
allocations. Firmware must reserve enough PCI MMIO space. A successful peer
capability query alone is not a bandwidth or NCCL correctness benchmark.
Historical measurements and hardware-specific limitations remain in Git history.

## Install

```bash
git clone https://github.com/rjohny55/cmp50-unlock.git
cd cmp50-unlock
sudo ./install-all.sh --install-deps --allow-experimental-gen2 --p2p
```

This selects the complete P2P/Gen2 patch set, stages matching kernel modules,
installs the official matching userspace when needed, configures BAR1 selector 9,
updates initramfs and installs the governor. It creates rollback backups and
**does not unload the active driver or reboot**. Finish workloads and reboot
manually when ready, then run:

```bash
sudo ./verify-live.sh
python3 ./verify/cuda-smoke.py
systemctl status cmp50-gen2-second-pass cmp-idle-governor
```

To add only the power governor to an already working driver:

```bash
sudo ./install-governor.sh --start-now
```

The governor runs independently from the panel. New standalone installations use
`/var/lib/cmp-idle-governor`; an existing configured control directory is preserved. It preserves
existing settings, tracks driver activity history and restores automatic fan
control on load or shutdown. Its shipped binary targets Ubuntu/Debian glibc.
Use `--ai-manager-data-dir /absolute/path` for a different panel data mount.

## Build and recovery

`CMP50_PATCH_STAGE=p2p bash ./build.sh` builds artifacts for the running kernel
without installing them. The build downloads the checksum-pinned NVIDIA
610.43.03 source and applies the ordered patches. Matching kernel headers,
compiler and firmware support are required. Unknown Gen2 kernels, unsupported
PCI/subsystem identities and unsigned Secure Boot installations are refused.
This is a validated kernel target, not a promise that every newer kernel works.

Restore the printed backup with `sudo ./rollback.sh /var/lib/cmp50-unlock/backups/TIMESTAMP`.
Keep recovery access when changing the kernel driver or PCIe configuration.
The patched paths are board-gated; do not remove PCI identity checks. The RT-core
reporting patch does not enable physical RT execution.

## Attribution

Based on [xrip/cmp50hx-unlock](https://github.com/xrip/cmp50hx-unlock), NVIDIA's
open GPU kernel modules and credited P2P work. Original research and notices
are retained. See [original technical material](docs/UPSTREAM_README.md) and
[patch provenance](docs/P2P.md). Older measurements in those documents are
historical, not the current installation instructions.
