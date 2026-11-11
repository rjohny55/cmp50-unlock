package governor

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

func supportedClockGPU(gpu GPU) bool {
	vendor, device := gpu.SubsystemID&0xffff, gpu.SubsystemID>>16
	return gpu.DeviceID == 0x1e09 && ((vendor == 0x10de && device == 0x1554) || (vendor == 0x1462 && device == 0x371f)) && gpu.MemoryBytes >= 20*1024*1024*1024-512*1024*1024 && gpu.MemoryBytes <= 20*1024*1024*1024+512*1024*1024
}

func (g *Governor) warmBounds(index int) (uint32, uint32, error) {
	var maximum uint32
	for _, device := range g.hardware.GPUs() {
		if device.Index == index {
			maximum = device.MaxGraphicsClock
		}
	}
	if maximum < 100 {
		return 0, 0, fmt.Errorf("GPU %d did not report its supported maximum graphics clock", index)
	}
	if g.hasClock && g.clockMax < maximum {
		maximum = g.clockMax
	}
	minimum := maximum / 3
	maximum = minimum
	if g.options.WarmClock != "" {
		lo, hi, _, err := parseClock(g.options.WarmClock)
		if err != nil {
			return 0, 0, err
		}
		minimum = lo
		maximum = hi
	}
	var hardwareMax uint32
	for _, device := range g.hardware.GPUs() {
		if device.Index == index {
			hardwareMax = device.MaxGraphicsClock
		}
	}
	if minimum > maximum || maximum > hardwareMax {
		return 0, 0, fmt.Errorf("GPU %d warm clock range exceeds supported/profile clocks", index)
	}
	return minimum, maximum, nil
}

func (g *Governor) pollClockPolicy() error {
	now := g.options.Now()
	changed := false
	for index := 0; index < Profiles[g.control.Profile]; index++ {
		state := g.states[index]
		var utilization, memoryActivity uint32
		var err error
		busyAt := now
		if activity, ok := g.hardware.(interface {
			ActivityTimestamp(int) (uint32, uint32, time.Time, error)
		}); ok {
			utilization, memoryActivity, busyAt, err = activity.ActivityTimestamp(index)
		} else if activity, ok := g.hardware.(interface {
			Activity(int) (uint32, uint32, error)
		}); ok {
			utilization, memoryActivity, err = activity.Activity(index)
		} else {
			utilization, err = g.hardware.Utilization(index)
		}
		if err != nil {
			return fmt.Errorf("GPU %d utilization: %w", index, err)
		}
		if utilization > g.options.UtilThreshold || memoryActivity > 0 {
			if !busyAt.IsZero() && busyAt.After(state.lastBusy) {
				state.lastActivity, state.lastBusy = busyAt, busyAt
			}
		}
		stage := "warm"
		// Keep stock clocks between short token-generation bursts. Allocated
		// VRAM alone is never activity, and polling does not extend the grace.
		if !state.lastBusy.IsZero() && now.Sub(state.lastBusy) < g.options.IdleDelay {
			stage = "active"
		}

		if stage != state.stage {
			changed = true
		}
		if stage == "warm" && state.coreOffsetManaged {
			if err := g.hardware.SetCoreOffset(index, 0); err != nil {
				return err
			}
			state.coreOffsetManaged = false
		}
		if stage == "active" && g.hasCoreOffset && !state.coreOffsetManaged {
			if err := g.restoreCoreOffset(index); err != nil {
				return err
			}
			state.coreOffsetManaged = true
		}
		if stage == "active" && !g.hasClock {
			// Work runs with the stock automatic clock policy. The idle
			// frequency ceiling must never cap an active workload.
			if state.clockManaged {
				if err := g.hardware.ResetLockedClocks(index); err != nil {
					return fmt.Errorf("GPU %d restore automatic load clocks: %w", index, err)
				}
				state.clockManaged = false
			}
		} else {
			minimum, maximum, err := g.warmBounds(index)
			if err != nil {
				return err
			}
			if stage == "active" && g.hasClock {
				minimum, maximum = g.clockMin, g.clockMax
			}
			if !state.clockManaged || state.warmMin != minimum || state.warmMax != maximum {
				if err := g.hardware.SetLockedClocks(index, minimum, maximum); err != nil {
					return fmt.Errorf("GPU %d set %s clock range: %w", index, stage, err)
				}
				state.clockManaged = true
				state.warmMin, state.warmMax = minimum, maximum
			}
		}
		if stage == "active" {
			if state.manualFan {
				if err := g.hardware.SetFanAuto(index); err != nil {
					return err
				}
				state.manualFan = false
			}
		} else if g.control.IdleFanPercent > 0 {
			temperature, err := g.hardware.Temperature(index)
			if err != nil {
				return err
			}
			if temperature >= g.options.FanAutoTemp {
				if state.manualFan {
					if err := g.hardware.SetFanAuto(index); err != nil {
						return err
					}
					state.manualFan = false
				}
			} else if !state.manualFan {
				if err := g.hardware.SetFan(index, uint32(g.control.IdleFanPercent)); err != nil {
					return err
				}
				state.manualFan = true
			}
		}
		if state.stage != stage {
			g.options.Logger.Printf("GPU %d power stage %s (warm floor %d MHz, max %d MHz; driver-controlled P-state)", index, stage, state.warmMin, state.warmMax)
		}
		state.stage = stage
	}
	if changed || g.lastClockStatus.IsZero() || now.Sub(g.lastClockStatus) >= 10*time.Second {
		if err := g.publishClockPolicy(now); err != nil {
			return err
		}
		g.lastClockStatus = now
	}
	return nil
}

func (g *Governor) releaseClockPolicy(index int, state *cardState) error {
	var result error
	// Do not issue control RPCs to a GPU which cannot report its own health.
	if _, err := g.hardware.Utilization(index); err != nil {
		return err
	}
	if state.manualFan {
		result = errors.Join(result, g.hardware.SetFanAuto(index))
	}
	if state.coreOffsetManaged {
		result = errors.Join(result, g.hardware.SetCoreOffset(index, 0))
	}
	if state.clockManaged {
		result = errors.Join(result, g.hardware.ResetLockedClocks(index))
	}
	if result == nil {
		state.manualFan, state.clockManaged, state.coreOffsetManaged = false, false, false
		state.stage = ""
		state.lastActivity = time.Time{}
	}
	return result
}

func (g *Governor) cleanupClockPolicy() error {
	var result error
	for _, device := range g.hardware.GPUs() {
		if !supportedClockGPU(device) {
			continue
		}
		if _, err := g.hardware.Utilization(device.Index); err != nil {
			result = errors.Join(result, fmt.Errorf("GPU %d health unavailable; controls skipped: %w", device.Index, err))
			continue
		}
		if state := g.states[device.Index]; state != nil && state.manualFan {
			result = errors.Join(result, g.hardware.SetFanAuto(device.Index))
			state.manualFan = false
		}
		if state := g.states[device.Index]; state != nil && state.coreOffsetManaged {
			if err := g.hardware.SetCoreOffset(device.Index, 0); err != nil {
				result = errors.Join(result, err)
				continue
			}
			state.coreOffsetManaged = false
		}
		// Reset only documented NVML controls; never force P0/P3/P8 or
		// invoke the legacy private auto/16 cleanup path.
		if err := g.hardware.ResetLockedClocks(device.Index); err != nil {
			result = errors.Join(result, err)
			continue
		}
		if state := g.states[device.Index]; state != nil {
			state.clockManaged = false
			state.stage = ""
			state.lastActivity = time.Time{}
		}
	}
	return result
}

func (g *Governor) publishClockPolicy(now time.Time) error {
	type entry struct {
		GPU         int    `json:"gpu"`
		Stage       string `json:"stage"`
		PState      string `json:"observed_pstate"`
		IdleSeconds int64  `json:"idle_seconds"`
		Minimum     uint32 `json:"minimum_clock_mhz"`
		Maximum     uint32 `json:"maximum_clock_mhz"`
	}
	entries := []entry{}
	for index := 0; index < Profiles[g.control.Profile]; index++ {
		state := g.states[index]
		idle := int64(0)
		if !state.lastActivity.IsZero() {
			idle = max(0, int64(now.Sub(state.lastActivity).Seconds()))
		}
		pstate := "unavailable"
		if reader, ok := g.hardware.(interface{ PerformanceState(int) (uint32, error) }); ok {
			if value, err := reader.PerformanceState(index); err == nil && value < 16 {
				pstate = fmt.Sprintf("P%d", value)
			}
		}
		minimum, maximum := uint32(0), uint32(0)
		if state.clockManaged {
			minimum, maximum = state.warmMin, state.warmMax
		}
		entries = append(entries, entry{index, state.stage, pstate, idle, minimum, maximum})
	}
	raw, err := json.Marshal(map[string]any{"policy": "nvml-clock-stages", "p8_enabled": false, "idle_delay_seconds": int64(g.options.IdleDelay.Seconds()), "p8_disabled_reason": "CMP50_GSP_WAKE_FAILURE", "updated_at": now.UTC(), "gpus": entries})
	if err != nil {
		return err
	}
	return g.status.writeAtomic("power-stages.json", string(raw)+"\n")
}
