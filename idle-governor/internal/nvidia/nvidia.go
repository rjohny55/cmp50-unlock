// Package nvidia is the only package that crosses the Go/C ABI boundary.
// The public governor remains ordinary safe Go and can be race-tested with a
// fake Hardware implementation without loading an NVIDIA driver.
package nvidia

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/ebitengine/purego"
	"github.com/rjohny55/cmp50-unlock/idle-governor/internal/governor"
)

const (
	nvmlSuccess        = 0
	nvmlTemperatureGPU = 0
	maxPhysicalGPUs    = 64
)

type pciInfo struct {
	BusIDLegacy                                [16]byte
	Domain, Bus, Device, DeviceID, SubsystemID uint32
	BusID                                      [32]byte
	Reserved                                   [4]uint32
}

type memoryInfo struct{ Total, Free, Used uint64 }
type utilization struct{ GPU, Memory uint32 }

type api struct {
	ml              uintptr
	mlShutdown      func() int32
	mlCount         func(*uint32) int32
	mlHandle        func(uint32, *uintptr) int32
	mlPCI           func(uintptr, *pciInfo) int32
	mlMemory        func(uintptr, *memoryInfo) int32
	mlUtil          func(uintptr, *utilization) int32
	mlSamples       samplesFn
	mlTemperature   func(uintptr, uint32, *uint32) int32
	mlMaxClock      func(uintptr, uint32, *uint32) int32
	mlPstate        func(uintptr, *uint32) int32
	mlFans          func(uintptr, *uint32) int32
	mlSetFan        func(uintptr, uint32, uint32) int32
	mlFanAuto       func(uintptr, uint32) int32
	mlResetClocks   func(uintptr) int32
	mlSetClocks     func(uintptr, uint32, uint32) int32
	mlSetCoreOffset func(uintptr, int32) int32
	devices         []device
}

type device struct {
	info   governor.GPU
	handle uintptr
	fans   uint32
}

// Open initializes documented NVML functions only. Forced P8 and the private
// fallback/16 RPC are temporarily disabled following CMP50 GSP wake failures.
func Open() (_ governor.Hardware, result error) {
	a := &api{}
	defer func() {
		if result != nil {
			_ = a.Close()
		}
	}()
	var err error
	a.ml, err = purego.Dlopen("libnvidia-ml.so.1", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return nil, fmt.Errorf("load NVML: %w", err)
	}
	var mlInit func() int32
	for name, target := range map[string]any{
		"nvmlInit_v2": &mlInit, "nvmlShutdown": &a.mlShutdown,
		"nvmlDeviceGetCount_v2": &a.mlCount, "nvmlDeviceGetHandleByIndex_v2": &a.mlHandle,
		"nvmlDeviceGetPciInfo_v3": &a.mlPCI, "nvmlDeviceGetMemoryInfo": &a.mlMemory,
		"nvmlDeviceGetUtilizationRates": &a.mlUtil, "nvmlDeviceGetSamples": &a.mlSamples, "nvmlDeviceGetTemperature": &a.mlTemperature,
		"nvmlDeviceGetNumFans":      &a.mlFans,
		"nvmlDeviceGetMaxClockInfo": &a.mlMaxClock, "nvmlDeviceGetPerformanceState": &a.mlPstate, "nvmlDeviceSetFanSpeed_v2": &a.mlSetFan,
		"nvmlDeviceSetDefaultFanSpeed_v2": &a.mlFanAuto,
		"nvmlDeviceResetGpuLockedClocks":  &a.mlResetClocks,
		"nvmlDeviceSetGpuLockedClocks":    &a.mlSetClocks,
		"nvmlDeviceSetGpcClkVfOffset":     &a.mlSetCoreOffset,
	} {
		if err := register(a.ml, name, target); err != nil {
			return nil, err
		}
	}
	if status := mlInit(); status != nvmlSuccess {
		return nil, statusError("nvmlInit_v2", status)
	}

	if err := a.enumerate(); err != nil {
		return nil, err
	}
	return a, nil
}

func register(library uintptr, name string, target any) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("resolve %s: %v", name, recovered)
		}
	}()
	purego.RegisterLibFunc(target, library, name)
	return nil
}

func (a *api) enumerate() error {
	var count uint32
	if status := a.mlCount(&count); status != nvmlSuccess {
		return statusError("nvmlDeviceGetCount_v2", status)
	}
	for index := uint32(0); index < count; index++ {
		var handle uintptr
		if status := a.mlHandle(index, &handle); status != nvmlSuccess {
			return statusError("nvmlDeviceGetHandleByIndex_v2", status)
		}
		var pci pciInfo
		if status := a.mlPCI(handle, &pci); status != nvmlSuccess {
			return statusError("nvmlDeviceGetPciInfo_v3", status)
		}
		var memory memoryInfo
		if status := a.mlMemory(handle, &memory); status != nvmlSuccess {
			return statusError("nvmlDeviceGetMemoryInfo", status)
		}
		var fans uint32
		if status := a.mlFans(handle, &fans); status != nvmlSuccess {
			return statusError("nvmlDeviceGetNumFans", status)
		}
		var maximum uint32
		if status := a.mlMaxClock(handle, 0, &maximum); status != nvmlSuccess {
			maximum = 0
		}
		busID := cString(pci.BusID[:])
		a.devices = append(a.devices, device{
			info: governor.GPU{Index: int(index), BusID: busID, DeviceID: pci.DeviceID >> 16,
				SubsystemID: pci.SubsystemID, MemoryBytes: memory.Total, MaxGraphicsClock: maximum},
			handle: handle, fans: fans,
		})
	}
	return nil
}

func cString(value []byte) string {
	if index := strings.IndexByte(string(value), 0); index >= 0 {
		value = value[:index]
	}
	return string(value)
}

func (a *api) GPUs() []governor.GPU {
	result := make([]governor.GPU, len(a.devices))
	for index := range a.devices {
		result[index] = a.devices[index].info
	}
	return result
}

func (a *api) device(index int) (*device, error) {
	if index < 0 || index >= len(a.devices) {
		return nil, fmt.Errorf("GPU index %d is out of range", index)
	}
	return &a.devices[index], nil
}

func (a *api) Utilization(index int) (uint32, error) {
	device, err := a.device(index)
	if err != nil {
		return 0, err
	}
	var value utilization
	status := a.mlUtil(device.handle, &value)
	runtime.KeepAlive(&value)
	if status != nvmlSuccess {
		return 0, statusError("nvmlDeviceGetUtilizationRates", status)
	}
	return value.GPU, nil
}

func (a *api) Temperature(index int) (uint32, error) {
	device, err := a.device(index)
	if err != nil {
		return 0, err
	}
	var value uint32
	status := a.mlTemperature(device.handle, nvmlTemperatureGPU, &value)
	runtime.KeepAlive(&value)
	if status != nvmlSuccess {
		return 0, statusError("nvmlDeviceGetTemperature", status)
	}
	return value, nil
}

func (a *api) ResetLockedClocks(index int) error {
	device, err := a.device(index)
	if err != nil {
		return err
	}
	if status := a.mlResetClocks(device.handle); status != nvmlSuccess {
		return statusError("nvmlDeviceResetGpuLockedClocks", status)
	}
	return nil
}

func (a *api) SetLockedClocks(index int, minimum, maximum uint32) error {
	device, err := a.device(index)
	if err != nil {
		return err
	}
	if status := a.mlSetClocks(device.handle, minimum, maximum); status != nvmlSuccess {
		return statusError("nvmlDeviceSetGpuLockedClocks", status)
	}
	return nil
}

func (a *api) SetCoreOffset(index int, offset int32) error {
	device, err := a.device(index)
	if err != nil {
		return err
	}
	if status := a.mlSetCoreOffset(device.handle, offset); status != nvmlSuccess {
		return statusError("nvmlDeviceSetGpcClkVfOffset", status)
	}
	return nil
}

func (a *api) SetFan(index int, percent uint32) error {
	device, err := a.device(index)
	if err != nil {
		return err
	}
	for fan := uint32(0); fan < device.fans; fan++ {
		if status := a.mlSetFan(device.handle, fan, percent); status != nvmlSuccess {
			for rollback := uint32(0); rollback < fan; rollback++ {
				_ = a.mlFanAuto(device.handle, rollback)
			}
			return statusError("nvmlDeviceSetFanSpeed_v2", status)
		}
	}
	return nil
}

func (a *api) SetFanAuto(index int) error {
	device, err := a.device(index)
	if err != nil {
		return err
	}
	var result error
	for fan := uint32(0); fan < device.fans; fan++ {
		if status := a.mlFanAuto(device.handle, fan); status != nvmlSuccess {
			result = errors.Join(result, statusError("nvmlDeviceSetDefaultFanSpeed_v2", status))
		}
	}
	return result
}

func (a *api) Close() error {
	var result error
	if a.mlShutdown != nil {
		if status := a.mlShutdown(); status != nvmlSuccess {
			result = errors.Join(result, statusError("nvmlShutdown", status))
		}
		a.mlShutdown = nil
	}
	if a.ml != 0 {
		result = errors.Join(result, purego.Dlclose(a.ml))
		a.ml = 0
	}
	return result
}

func statusError(operation string, status int32) error {
	return fmt.Errorf("%s failed (0x%08x)", operation, uint32(status))
}

func (a *api) PerformanceState(index int) (uint32, error) {
	device, err := a.device(index)
	if err != nil {
		return 0, err
	}
	var state uint32
	if code := a.mlPstate(device.handle, &state); code != nvmlSuccess {
		return 0, statusError("nvmlDeviceGetPerformanceState", code)
	}
	return state, nil
}

// Activity preserves the existing read-only peak API.
func (a *api) Activity(index int) (uint32, uint32, error) {
	gpu, memory, _, err := a.ActivityTimestamp(index)
	return gpu, memory, err
}

// ActivityTimestamp uses an overlapping three-second history window. Asking
// only after the last timestamp can return NOT_FOUND during normal sample
// cadence; that is not a GPU failure. The original busy timestamp prevents
// repeated overlap samples from extending the configured idle delay.
func (a *api) ActivityTimestamp(index int) (uint32, uint32, time.Time, error) {
	d, err := a.device(index)
	if err != nil {
		return 0, 0, time.Time{}, err
	}
	since := uint64(time.Now().Add(-3 * time.Second).UnixMicro())
	gpu, _, gpuBusy, err := sampleActivityWithTime(a.mlSamples, d.handle, 1, since)
	if err != nil {
		return 0, 0, time.Time{}, err
	}
	memory, _, memBusy, err := sampleActivityWithTime(a.mlSamples, d.handle, 2, since)
	if err != nil {
		return 0, 0, time.Time{}, err
	}
	busy := max(gpuBusy, memBusy)
	stamp := time.Time{}
	if busy != 0 {
		stamp = time.UnixMicro(int64(busy))
	}
	return gpu, memory, stamp, nil
}
