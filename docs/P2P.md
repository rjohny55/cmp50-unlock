# Experimental CMP50 20 GB PCIe P2P

[Русская инструкция](#русская-инструкция)

## Scope and evidence

Validated on 26 October 2026: Ubuntu, kernel **6.8.0-147-generic**, NVIDIA open
kernel modules and matching userspace **610.43.03**, two modified **20 GB CMP
50HX / TU102 / SM75** cards, PCIe Gen2 x16, same CPU/PHB, no NVLink. Other
motherboards, kernels, mixed GPU configurations and passthrough are not validated.

After a normal reboot into the installed patch set:

- Both GPUs retained 20480 MiB VRAM and 32768 MiB BAR1; network, graphical
  desktop, panel and idle governor remained available.
- CUDA peer access in both directions, remote-read and remote-write kernels,
  and `cudaMemcpyPeerAsync` passed full 64 MiB data comparisons.
- 32 copies of 64 MiB measured approximately **5.28 GB/s per direction**.
- NCCL all-reduce passed full-value comparisons at 4 B, 4 KiB, 4 MiB and
  64 MiB. Logs confirmed **`via P2P/CUMEM`**, not merely capability reporting.

This is not a full-VRAM/endurance test or a fresh LLM throughput benchmark.

## Separate, reproducible build

The default `build.sh`, `install.sh` and `install-all.sh` behavior is unchanged.
`build-p2p.sh` produces artifacts only: it does not install modules, edit boot
or network configuration, unload a driver or reset GPUs. Use it on an already
working 20 GB unlock installation with the matching kernel headers, build tools
and `modinfo` available:

```bash
curl -fL https://github.com/NVIDIA/open-gpu-kernel-modules/archive/refs/tags/610.43.03.tar.gz \
  -o NVIDIA-610.43.03.tar.gz
KERNEL_RELEASE=6.8.0-147-generic JOBS=12 \
  bash ./build-p2p.sh --source-tarball NVIDIA-610.43.03.tar.gz
```

The archive SHA256 is pinned to
`9df87d753cd9c05aa0eedc462af9b35debb549a657136e863282f94c96ee2640`.
Patches must apply with zero fuzz. The build verifies module version and kernel
vermagic, then exports four required modules, optional matching PEERMEM, and
checksums to `artifacts/610.43.03-6.8.0-147-generic-p2p/`. Existing output is
not overwritten. An explicit `CMP50_P2P_ARTIFACT_DIR` can select another output.

Patch order:

1. Existing 20 GB dynamic-WPR stockflow and RT patches (`01`, `02`).
2. `03-cmp50-rebar-p2p.patch`: extends BAR1 selection to 32 GiB while retaining
   the validated ReBAR re-arm after FLR. Selector 8 remains the default 16 GiB;
   **selector 9** selects 32 GiB, covering the entire 20 GB VRAM.
3. Existing Gen2 patch (`04`).
4. `06-aikitoria-p2p.patch` and `07-turing-p2p.patch`: upstream P2P changes
   from [xrip/cmp50hx-unlock](https://github.com/xrip/cmp50hx-unlock), pinned to
   commit `df2fe11b370bb52cc314576d0f2f5ae47ce908f8`. These include broad HAL and
   mapping changes, not just a CMP-specific capability flag; do not use this
   experimental build on an untested mixed-GPU host.
5. `08-cmp50-dma32-bootstrap.patch`: keeps only the CMP bootstrap signature,
   replacement signature and fresh WPR metadata addressable below 4 GiB.
   Without this, GSP failed to start when these buffers received high physical
   addresses with IOMMU disabled. Address checks fail closed; normal model/GPU
   allocations are not restricted to DMA32. Firmware-state validation remains.

The boot second-pass helper now validates the actual configured BAR1 size for
both selector 8 and 9; it retains busy-device, kernel and udev-restoration guards.

## Installation is an explicit maintenance operation

There is deliberately no unattended P2P installer. Before installing artifacts,
stop GPU workloads, arrange local recovery access, back up the current modules,
initramfs, boot and modprobe configuration, and retain a known-good boot entry.
Secure Boot may require module signing according to the host's existing policy.

On the validated Intel bare-metal host, successful real P2P required a boot
with **`intel_iommu=off`** and `cmp50_rebar_size=9`. This disables DMA isolation
globally and is inappropriate for untrusted devices or workloads that require
IOMMU isolation/passthrough. Do not automatically copy it to other systems.
Enabling P2P while native IOMMU translation is active risks corruption; a
successful NVML capability report is not sufficient evidence of safe transfers.
See [NVIDIA's NCCL GPU troubleshooting](https://docs.nvidia.com/deeplearning/nccl/user-guide/docs/troubleshooting/gpu_troubleshooting.html).

The validated host retained kernel 137 and its original modules/initramfs as a
separate recovery entry, without the IOMMU-off option and with BAR1 selector 8.
After installing matching modules, update `depmod`, initramfs and the boot menu
using the host's own paths/policy, then reboot and repeat all data tests. Never
unbind a network device or reset a shared upstream bridge to enable P2P.

## Verify real transfers and NCCL routing

Run only after checking that native IOMMU translation is disabled for the
tested bare-metal setup. With exactly two visible GPUs and a CUDA toolkit:

```bash
nvidia-smi topo -p2p r
nvidia-smi topo -p2p w
nvcc -O2 -arch=sm_75 verify/p2p_correctness.cu -o /tmp/cmp50-p2p-correctness
/tmp/cmp50-p2p-correctness
# Requires CUDA-enabled PyTorch; use the same environment as the inference engine.
OMP_NUM_THREADS=2 NCCL_P2P_DISABLE=0 NCCL_P2P_LEVEL=PHB NCCL_IB_DISABLE=1 \
  NCCL_DEBUG=INFO python3 verify/nccl-p2p.py
```

Require `P2P_BIDIRECTIONAL_DATA_INTEGRITY_PASS`,
`NCCL_P2P_ALLREDUCE_INTEGRITY_PASS` and actual **`via P2P/CUMEM`** NCCL log
entries. A correctness pass alone does not prove which NCCL transport was used.
`PHB` is the measured topology, not a recommendation to force distant pairs on
every machine. Existing engine presets with `NCCL_P2P_DISABLE=1` still disable
P2P until explicitly changed. The test uses localhost port 29679; ensure it is
free. Do not run two copies simultaneously.

## Русская инструкция

Это отдельная экспериментальная сборка, а не замена обычному установщику.
Проверены две CMP50 по 20 ГБ, NVIDIA 610.43.03 и точное ядро 6.8.0-147-generic:
реальные чтение/запись/копирование между картами и NCCL P2P/CUMEM работают,
в том числе после перезагрузки. Замер — около 5,28 ГБ/с в каждую сторону на
Gen2 x16; скорость генерации LLM после этого изменения ещё не измерена.

Команда сборки приведена выше. Она только создаёт модули и проверяет их версии,
не меняет загрузку, сеть и работающий драйвер. Стадия использует существующий
анлок 20 ГБ, P2P-патчи upstream и исправление DMA32 для небольших служебных
буферов запуска GSP. Память моделей этим ограничением не затрагивается.

Для проверенного сервера нужны BAR1 32 ГиБ (`cmp50_rebar_size=9`) и
`intel_iommu=off`. Последнее отключает DMA-изоляцию всего сервера: это осознанный
компромисс безопасности, не универсальная настройка. Обычные установщики её
не включают. Перед ручной установкой сохраните модули, initramfs и настройки
загрузчика; оставьте рабочий вариант отката с прежним ядром, драйвером,
селектором 8 и прежним режимом IOMMU. Нужен локальный доступ для восстановления.

После установки и перезагрузки выполните CUDA- и NCCL-тесты выше. Одного `OK`
в `nvidia-smi` недостаточно. Должны пройти проверки содержимого данных, а в
логе NCCL должен быть `via P2P/CUMEM`. Существующие профили с
`NCCL_P2P_DISABLE=1` сами не изменяются. Другие ядра, платы, смешанные GPU и
длительная эксплуатация этой сборкой не подтверждены. Сетевые устройства и
общие PCIe-мосты для включения P2P отключать/сбрасывать нельзя.
