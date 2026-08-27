# CMP50 Unlock

[Русская версия](README_RU.md) · [20 GB guide](docs/20GB.md) · [10 GB guide](docs/10GB.md) · [Agent installation runbook (RU)](AGENT_INSTALL_RU.md) · [Original technical guide](docs/UPSTREAM_README.md) · [Original project](https://github.com/xrip/cmp50hx-unlock)

## Card variants

| [CMP 50HX 10 GB](docs/10GB.md) | [CMP 50HX 20 GB](docs/20GB.md) |
| --- | --- |
| Original, most thoroughly validated configuration | Dynamic WPR support; four 20480 MiB cards validated on kernels 6.8.0-137 and 6.8.0-138 |

The 20 GB update replaces fixed WPR geometry with fail-closed, per-BDF capture
of the stock FWSEC range. Compute passes on four cards and a 16 GiB BAR1 is
boot-validated. On the reference X99 host, kernel 6.8.0-137 now completes a
fail-closed second driver pass and trains all four physical links at Gen2
(x4 + 3 x16), without Link Disable. The same boot also preserves 20480 MiB VRAM
and 16384 MiB BAR1 on every card. Kernel 6.8.0-138 remains validated only
through the ReBAR stage; the Gen2 installer deliberately refuses it.

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
`rebar` is the broadly boot-validated next stage. `gen2` is host-specific,
validated on the reference X99/kernel-137 system, and requires an explicit
installer acknowledgement. See `sudo ./install.sh --help`.

The detailed reasoning, register descriptions, evidence levels, measurements, and limitations are preserved in [docs/UPSTREAM_README.md](docs/UPSTREAM_README.md). Additional research notes are available in [docs/CMP50HX.md](docs/CMP50HX.md).

## Installation and verification

The installer builds modules for the running kernel, creates a rollback backup,
and stages the new driver for the next boot. It never unloads the active driver.
Start with `rt`, then validate each additional stage separately:

```bash
git clone https://github.com/rjohny55/cmp50-unlock.git
cd cmp50-unlock
sudo ./install.sh --stage rt --install-userland --initramfs
sudo systemctl poweroff
```

After the cold boot:

```bash
sudo ./verify-live.sh
```

ReBAR is a separate stage and must be followed by another cold boot:

```bash
sudo ./install.sh --stage rebar --initramfs
sudo systemctl poweroff
```

On the validated X99 host with exactly kernel `6.8.0-137-generic`, the complete
20 GB Gen2 stage is:

```bash
sudo ./install.sh --stage gen2 --allow-experimental-gen2 --initramfs
sudo systemctl reboot
systemctl status cmp50-gen2-second-pass.service
sudo ./verify-live.sh
```

The Gen2 installer intentionally refuses kernel 138 and every other kernel.
On a different motherboard, stop at ReBAR until the host-specific Gen2 path is
independently validated. Roll back with the backup path printed by the installer:

```bash
sudo ./rollback.sh /var/lib/cmp50-unlock/backups/TIMESTAMP
```

### Validated 20 GB result

| Check | Result on the reference X99 / kernel 137 host |
| --- | --- |
| GPUs | 4 × CMP 50HX, 20480 MiB each |
| PCIe | Gen2 x4 + 3 × Gen2 x16 |
| BAR1 | 16384 MiB on every GPU |
| Compute verifier | `PASS_CMP50HX_ISSUE_RATE_AND_COUNTS` |
| Boot helper | `cmp50-gen2-second-pass.service`: `active (exited)` |
| Link procedure | Endpoint-first Retrain + root Retrain; no Link Disable |

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
config/cmp50-gen2.conf
scripts/cmp50-gen2-second-pass.sh
systemd/cmp50-gen2-second-pass.service
idle-governor/
decompil/gsp_tu10x_610.43.03.elf.i64
docs/10GB.md and docs/10GB_RU.md
docs/20GB.md and docs/20GB_RU.md
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
