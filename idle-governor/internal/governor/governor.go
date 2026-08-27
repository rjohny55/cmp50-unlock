package governor

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

const autoPState = 16

type GPU struct {
	Index       int
	BusID       string
	DeviceID    uint32
	SubsystemID uint32
	MemoryBytes uint64
}

type Hardware interface {
	GPUs() []GPU
	Utilization(index int) (uint32, error)
	Temperature(index int) (uint32, error)
	SetPState(index, pstate int) error
	ResetLockedClocks(index int) error
	SetLockedClocks(index int, minimum, maximum uint32) error
	SetCoreOffset(index int, offset int32) error
	SetFan(index int, percent uint32) error
	SetFanAuto(index int) error
	Close() error
}

type Options struct {
	ControlDirectory string
	Poll             time.Duration
	IdleAfter        int
	UtilThreshold    uint32
	FanAutoTemp      uint32
	LoadClock        string
	LoadCoreOffset   string
	Logger           *log.Logger
}

type cardState struct {
	idlePolls int
	lowPower  bool
	manualFan bool
}

type Governor struct {
	hardware      Hardware
	options       Options
	status        StatusWriter
	states        map[int]*cardState
	control       Control
	active        bool
	clockMin      uint32
	clockMax      uint32
	hasClock      bool
	coreOffset    int32
	hasCoreOffset bool
}

func New(hardware Hardware, options Options) (*Governor, error) {
	if options.Poll <= 0 || options.IdleAfter <= 0 {
		return nil, errors.New("poll duration and idle poll count must be positive")
	}
	if options.FanAutoTemp == 0 {
		options.FanAutoTemp = 55
	}
	if options.Logger == nil {
		options.Logger = log.New(os.Stderr, "cmp-idle-governor: ", log.LstdFlags)
	}
	minimum, maximum, hasClock, err := parseClock(options.LoadClock)
	if err != nil {
		return nil, err
	}
	coreOffset, hasCoreOffset, err := parseCoreOffset(options.LoadCoreOffset)
	if err != nil {
		return nil, err
	}
	states := make(map[int]*cardState, len(hardware.GPUs()))
	for _, gpu := range hardware.GPUs() {
		states[gpu.Index] = &cardState{}
	}
	return &Governor{
		hardware: hardware, options: options,
		status: StatusWriter{Directory: options.ControlDirectory}, states: states,
		clockMin: minimum, clockMax: maximum, hasClock: hasClock,
		coreOffset: coreOffset, hasCoreOffset: hasCoreOffset,
	}, nil
}

func parseCoreOffset(value string) (int32, bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil || parsed < -2000 || parsed > 2000 {
		return 0, false, errors.New("CMP_LOAD_CORE_OFFSET must be -2000..2000 MHz")
	}
	return int32(parsed), true, nil
}

func parseClock(value string) (uint32, uint32, bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, 0, false, nil
	}
	parts := strings.Split(value, ",")
	if len(parts) > 2 {
		return 0, 0, false, errors.New("CMP_LOAD_CLOCK must be MHz or MIN,MAX")
	}
	values := make([]uint64, len(parts))
	for index, part := range parts {
		parsed, err := strconv.ParseUint(strings.TrimSpace(part), 10, 32)
		if err != nil || parsed < 100 || parsed > 5000 {
			return 0, 0, false, errors.New("CMP_LOAD_CLOCK must contain 100..5000 MHz")
		}
		values[index] = parsed
	}
	if len(values) == 1 {
		return uint32(values[0]), uint32(values[0]), true, nil
	}
	if values[0] > values[1] {
		return 0, 0, false, errors.New("CMP_LOAD_CLOCK minimum exceeds maximum")
	}
	return uint32(values[0]), uint32(values[1]), true, nil
}

func (g *Governor) Run(ctx context.Context) error {
	if err := g.Cleanup(); err != nil {
		g.options.Logger.Printf("startup cleanup: %v", err)
	}
	defer func() {
		if err := g.Cleanup(); err != nil {
			g.options.Logger.Printf("shutdown cleanup: %v", err)
		}
	}()
	if err := g.syncControl(); err != nil && !errors.Is(err, os.ErrNotExist) {
		g.fail(err)
	}
	controlTicker := time.NewTicker(time.Second)
	defer controlTicker.Stop()
	gpuTicker := time.NewTicker(g.options.Poll)
	defer gpuTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-controlTicker.C:
			if err := g.syncControl(); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					g.disable()
					continue
				}
				g.fail(err)
				continue
			}
		case <-gpuTicker.C:
			if g.active {
				if err := g.poll(); err != nil {
					g.fail(err)
				}
			}
		}
	}
}

func (g *Governor) syncControl() error {
	control, err := LoadControl(g.options.ControlDirectory + "/control.json")
	if err != nil {
		return err
	}
	if !control.Enabled {
		g.disable()
		return nil
	}
	if g.active && control == g.control {
		return nil
	}
	if err := g.validate(control); err != nil {
		return err
	}
	g.releaseAll()
	g.control, g.active = control, true
	if err := g.status.Active(control); err != nil {
		g.active = false
		return fmt.Errorf("publish active status: %w", err)
	}
	g.options.Logger.Printf("enabled profile %s, idle fan %d%%", control.Profile, control.IdleFanPercent)
	return nil
}

func (g *Governor) validate(control Control) error {
	count := Profiles[control.Profile]
	gpus := g.hardware.GPUs()
	if len(gpus) < count {
		return fmt.Errorf("profile requires %d GPUs, only %d found", count, len(gpus))
	}
	for index := 0; index < count; index++ {
		gpu := gpus[index]
		if gpu.Index != index {
			return fmt.Errorf("profile requires contiguous NVIDIA GPU indices starting at 0")
		}
		if gpu.DeviceID != 0x1e09 {
			return fmt.Errorf("GPU %d is not CMP 50HX (device %04x)", index, gpu.DeviceID)
		}
		vendor, device := gpu.SubsystemID&0xffff, gpu.SubsystemID>>16
		if !((vendor == 0x10de && device == 0x1554) || (vendor == 0x1462 && device == 0x371f)) {
			return fmt.Errorf("GPU %d has unsupported subsystem %04x:%04x", index, vendor, device)
		}
		const gib20 = 20 * 1024 * 1024 * 1024
		if gpu.MemoryBytes < gib20-512*1024*1024 || gpu.MemoryBytes > gib20+512*1024*1024 {
			return fmt.Errorf("GPU %d is not the 20 GB variant", index)
		}
	}
	return nil
}

func (g *Governor) poll() error {
	count := Profiles[g.control.Profile]
	for index := 0; index < count; index++ {
		state := g.states[index]
		utilization, err := g.hardware.Utilization(index)
		if err != nil {
			return fmt.Errorf("GPU %d utilization: %w", index, err)
		}
		if utilization > g.options.UtilThreshold {
			state.idlePolls = 0
			if state.lowPower || state.manualFan {
				if err := g.release(index, state); err != nil {
					return err
				}
				g.options.Logger.Printf("GPU %d busy (%d%%), released", index, utilization)
			}
			continue
		}
		state.idlePolls++
		if !state.lowPower && state.idlePolls >= g.options.IdleAfter {
			if g.hasCoreOffset {
				if err := g.hardware.SetCoreOffset(index, 0); err != nil {
					return fmt.Errorf("GPU %d clear core offset: %w", index, err)
				}
			}
			if err := g.hardware.ResetLockedClocks(index); err != nil {
				_ = g.restoreCoreOffset(index)
				return fmt.Errorf("GPU %d reset clocks: %w", index, err)
			}
			if err := g.hardware.SetPState(index, 8); err != nil {
				_ = g.restoreClocks(index)
				_ = g.restoreCoreOffset(index)
				return fmt.Errorf("GPU %d force P8: %w", index, err)
			}
			state.lowPower = true
			g.options.Logger.Printf("GPU %d idle, forced P8", index)
		}
		if state.lowPower && g.control.IdleFanPercent > 0 {
			temperature, err := g.hardware.Temperature(index)
			if err != nil {
				return fmt.Errorf("GPU %d temperature: %w", index, err)
			}
			if temperature >= g.options.FanAutoTemp {
				if state.manualFan {
					if err := g.hardware.SetFanAuto(index); err != nil {
						return fmt.Errorf("GPU %d automatic fan: %w", index, err)
					}
					state.manualFan = false
				}
			} else if !state.manualFan {
				if err := g.hardware.SetFan(index, uint32(g.control.IdleFanPercent)); err != nil {
					return fmt.Errorf("GPU %d idle fan: %w", index, err)
				}
				state.manualFan = true
			}
		}
	}
	return nil
}

func (g *Governor) restoreClocks(index int) error {
	if g.hasClock {
		return g.hardware.SetLockedClocks(index, g.clockMin, g.clockMax)
	}
	return g.hardware.ResetLockedClocks(index)
}

func (g *Governor) restoreCoreOffset(index int) error {
	if !g.hasCoreOffset {
		return nil
	}
	return g.hardware.SetCoreOffset(index, g.coreOffset)
}

func (g *Governor) release(index int, state *cardState) error {
	var result error
	if state.manualFan {
		result = errors.Join(result, g.hardware.SetFanAuto(index))
	}
	result = errors.Join(result, g.hardware.SetPState(index, autoPState))
	result = errors.Join(result, g.restoreCoreOffset(index))
	result = errors.Join(result, g.restoreClocks(index))
	state.lowPower, state.manualFan, state.idlePolls = false, false, 0
	if result != nil {
		return fmt.Errorf("GPU %d release: %w", index, result)
	}
	return nil
}

func (g *Governor) releaseAll() {
	for index, state := range g.states {
		if state.lowPower || state.manualFan {
			if err := g.release(index, state); err != nil {
				g.options.Logger.Print(err)
			}
		}
	}
}

// Cleanup releases every supported CMP 50HX 20 GB card. It intentionally does
// not trust in-memory state: ExecStopPost and a restarted service must also be
// able to recover a manual fan or P8 left by a killed previous process.
func (g *Governor) Cleanup() error {
	var result error
	for _, gpu := range g.hardware.GPUs() {
		vendor, device := gpu.SubsystemID&0xffff, gpu.SubsystemID>>16
		supportedSubsystem := (vendor == 0x10de && device == 0x1554) ||
			(vendor == 0x1462 && device == 0x371f)
		const gib20 = 20 * 1024 * 1024 * 1024
		is20GB := gpu.MemoryBytes >= gib20-512*1024*1024 && gpu.MemoryBytes <= gib20+512*1024*1024
		if gpu.DeviceID != 0x1e09 || !supportedSubsystem || !is20GB {
			continue
		}
		result = errors.Join(result, g.hardware.SetFanAuto(gpu.Index))
		result = errors.Join(result, g.hardware.SetPState(gpu.Index, autoPState))
		result = errors.Join(result, g.restoreCoreOffset(gpu.Index))
		result = errors.Join(result, g.restoreClocks(gpu.Index))
		if state := g.states[gpu.Index]; state != nil {
			state.lowPower, state.manualFan, state.idlePolls = false, false, 0
		}
	}
	return result
}

func (g *Governor) disable() {
	g.releaseAll()
	g.active = false
	_ = g.status.Inactive()
}

func (g *Governor) fail(err error) {
	if cleanupErr := g.Cleanup(); cleanupErr != nil {
		err = errors.Join(err, fmt.Errorf("safety cleanup: %w", cleanupErr))
	}
	g.active = false
	g.status.Error(err)
	g.options.Logger.Print(err)
}
