# CMP50 Unlock

[Русская версия](README_RU.md) · [Agent installation runbook (RU)](AGENT_INSTALL_RU.md) · [Original technical guide](docs/UPSTREAM_README.md) · [Original project](https://github.com/xrip/cmp50hx-unlock)

## Card variants

| [CMP 50HX 10 GB (RU)](docs/10GB_RU.md) | [CMP 50HX 20 GB (RU)](docs/20GB_RU.md) |
| --- | --- |
| Original, most thoroughly validated configuration | Dynamic WPR support; four 20480 MiB cards validated on kernels 6.8.0-137 and 6.8.0-138 |

The 20 GB update replaces fixed WPR geometry with fail-closed, per-BDF capture
of the stock FWSEC range. Compute and the host RT-count stage pass on four
cards. A 16 GiB BAR1 is boot-validated. Kernel 6.8.0-138 works with the ReBAR
stage, while a direct boot of the experimental Gen2 stage caused GSP Booter
`0x8d`. On kernel 137 a controlled two-module sequence proved that all four
physical links can train at Gen2 (x4 + 3 x16), but the subsequent bridge cycle
made the XVE BAR0 page unreadable and RM could not rebind, even after FLR. A
reboot restored all GPUs at Gen1. The Gen2 stage must not be deployed on a
production system.

An independent, clean-history mirror of research and patches for the NVIDIA CMP 50HX. The original research, patches, measurements, and technical explanations were created by **[xrip](https://github.com/xrip)** in **[xrip/cmp50hx-unlock](https://github.com/xrip/cmp50hx-unlock)**.

This repository preserves attribution and links to the original author. It is not presented as original work by the mirror owner.

## Scope

The patch set targets NVIDIA's open GPU kernel modules version `610.43.03` and the tested CMP 50HX PCI identities:

| Field | Value |
| --- | --- |
| GPU | NVIDIA CMP 50HX / TU102 |
| PCI vendor/device | `10de:1e09` |
| NVIDIA subsystem | `10de:1554` |
| MSI subsystem | `1462:371f` |
| Driver source | NVIDIA open-gpu-kernel-modules `610.43.03` |

The project contains four incremental patches:

1. `01-cmp50-stockflow.patch` — board-gated GSP/RM stockflow and SM issue-rate path.
2. `02-cmp50-rt-core-count.patch` — host-side RT core count reporting.
3. `03-cmp50-rebar.patch` — CMP50-specific ReBAR setup.
4. `04-cmp50-pcie-gen2.patch` — endpoint and upstream-bridge PCIe Gen2 retraining.

Select a build stage with
`CMP50_PATCH_STAGE=stockflow|rt|rebar|gen2`; the safe default is `rt`, and
`rebar` is the next boot-validated stage. `gen2` is research-only and requires
an explicit installer acknowledgement. See `sudo ./install.sh --help`.

The detailed reasoning, register descriptions, evidence levels, measurements, and limitations are preserved in [docs/UPSTREAM_README.md](docs/UPSTREAM_README.md). Additional research notes are available in [docs/CMP50HX.md](docs/CMP50HX.md).

## Important limitations

- This is experimental low-level GPU and kernel-driver research.
- The patches are restricted to exact PCI identities. Do not remove those checks.
- The reported 56 RT cores expose a host API value; they do **not** enable physical RT instruction execution.
- ReBAR and PCIe changes depend on motherboard firmware, bridge resources, kernel version, and the exact board.
- A bad kernel module or firmware-related change can make the system unbootable, destabilize the GPU, or require recovery from another kernel.
- Keep a known-good kernel, initramfs, driver package, and remote/recovery access before testing.

## Repository contents

```text
01-cmp50-stockflow.patch
02-cmp50-rt-core-count.patch
03-cmp50-rebar.patch
04-cmp50-pcie-gen2.patch
build.sh
install.sh
rollback.sh
verify-live.sh
verify/rm_issue_rate.c
verify/verify.py
tools/cmp50-bar0-link-rate.c
tools/cmp50-bar0-gen2-prepare.c
idle-governor/
decompil/gsp_tu10x_610.43.03.elf.i64
docs/10GB_RU.md
docs/20GB_RU.md
docs/CMP50HX.md
docs/UPSTREAM_README.md
```

## Build prerequisites

The supplied build script expects Linux, matching kernel headers, a C compiler, GNU make, curl, patch, tar, `modinfo`, and related base utilities. Its source target is pinned to:

```text
https://github.com/NVIDIA/open-gpu-kernel-modules/archive/refs/tags/610.43.03.tar.gz
SHA-256: 9df87d753cd9c05aa0eedc462af9b35debb549a657136e863282f94c96ee2640
```

Before executing anything, read `build.sh` and review every patch. The imported upstream snapshot should be treated as research material: verify that all paths and helper sources referenced by the script are present in your checkout before relying on an automated build.

The script's intended interface is:

```bash
bash ./build.sh
bash ./build.sh --source-dir /path/to/open-gpu-kernel-modules
bash ./build.sh --source-tarball /path/to/610.43.03.tar.gz
```

It is designed to build artifacts only. It does not install, load, unload, or reset the GPU.

## Safe review workflow

1. Confirm the exact PCI device and subsystem IDs with `lspci -nn`.
2. Confirm the running kernel and install exactly matching kernel headers.
3. Review the four patches in order.
4. Run patch dry-runs against a clean, hash-verified NVIDIA source tree.
5. Build artifacts without installing them.
6. Inspect module version, vermagic, strings, and checksums.
7. Prepare rollback and recovery access.
8. Only then perform hardware testing on a non-critical host.

## Attribution

Source project and original author:

- Author: [xrip](https://github.com/xrip)
- Source: [github.com/xrip/cmp50hx-unlock](https://github.com/xrip/cmp50hx-unlock)
- Imported source commit: `6ddaaf034782bd3f61ce26a211c0168fabbd7684`

No upstream license file was present in the imported snapshot. This mirror keeps explicit attribution and the original technical guide. Users are responsible for verifying their rights and all applicable NVIDIA, driver, firmware, and local legal terms before copying, modifying, distributing, or using the material.
