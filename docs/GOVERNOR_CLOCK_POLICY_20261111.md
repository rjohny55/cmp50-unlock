# CMP50 clock governor qualification — 11 November 2026

P8 is **temporarily disabled because of wake-up failures**. The executable uses
NVML only; it does not load NvAPI or force any P-state.

Hardware: four modified CMP50HX 20 GiB cards, NVIDIA 610.43.03 open kernel module.
All four cards were exercised with an FP16 GEMM workload. These are frequency
and power-policy checks, not an LLM tokens/s or latency benchmark.

| Condition | Observed graphics clock | Observed state | Power telemetry |
|---|---|---|---|
| Idle graphics limit 700 MHz | 705 MHz | P0 | approximately 40–60 W per card after settling |
| Active workload, limit released | approximately 1710–1905 MHz | P0 | depends on workload and card |
| First 30 seconds after work | stock automatic clocks retained | P0 | higher than limited idle |
| After 30 seconds without sampled activity | 705 MHz again | P0 | returns toward idle readings |

The tested idle readings before the final workload were 60.29 / 40.19 / 50.73 /
47.58 W for GPU 0–3. These are NVIDIA-reported values, not calibrated wall-meter
measurements; modified cards can report different offsets.

The final test retained full clocks while idle between 12:20:43 and 12:21:11 UTC.
GPU 0/3 returned to the idle ceiling at 12:21:12, GPU 1/2 at 12:21:13. There were
no GPU reset errors, NVML history errors, governor reactivations or abnormal exit
in this final cycle. Service shutdown released its own NVML limits successfully.

## Activity history

NVML GPU and memory-controller history remains enabled. An overlapping
three-second read window avoids normal `NVML_ERROR_NOT_FOUND` sample-cadence
races from querying only after the last returned timestamp. The timestamp of
the actual last nonzero sample is retained, so reading an old burst twice does
not reset the 30-second timer. Allocated VRAM does not count as ongoing work.

## Rejected paths

- NVML lists P0/P3/P8 as supported. A private forced P3 transition nevertheless
  produced GSP/PMU halt, Xid 62/154, and `GPU requires reset` on one test card.
- P8 → forced P0 restored memory to 7000 MHz but left graphics at 645 MHz,
  including during a workload. Public clock/application-clock resets did not
  recover the normal automatic boost.
- The staged P8 → P0 → private automatic/fallback transition also produced
  GSP/PMU halt and required recovery. It is not used by this release.

The policy therefore remains in the working driver-managed state with NVML
clock limits only. There is no delayed private P8 transition, including after
ten minutes or on service shutdown. No voltage or memory VF offset is changed.

## Software verification

- `go test -race ./...` and `go vet ./...`.
- Installer fixtures cover independent installation without a panel, preserved
  configuration, rejected hardware and atomic legacy-service replacement.
- Fake-clock regression covers the default 30-second idle boundary, short core
  and memory bursts, long idle, cleanup markers and absence of forced P-states.
- Hardware validation covers idle → load → 30-second grace → idle on all cards.
