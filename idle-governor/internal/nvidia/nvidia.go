// Package nvidia is the only package that crosses the Go/C ABI boundary.
// The public governor remains ordinary safe Go and can be race-tested with a
// fake Hardware implementation without loading an NVIDIA driver.
package nvidia

import (
	"errors"
	"fmt"
	"runtime"
	"strings"

	"github.com/ebitengine/purego"
	"github.com/rjohny55/cmp50-unlock/idle-governor/internal/governor"
)

const (
	nvmlSuccess        = 0
	nvmlTemperatureGPU = 0
	nvapiOK            = 0
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
	ml, nvapi       uintptr
	mlShutdown      func() int32
	mlCount         func(*uint32) int32
	mlHandle        func(uint32, *uintptr) int32
	mlPCI           func(uintptr, *pciInfo) int32
	mlMemory        func(uintptr, *memoryInfo) int32
	mlUtil          func(uintptr, *utilization) int32
	mlTemperature   func(uintptr, uint32, *uint32) int32
	mlFans          func(uintptr, *uint32) int32
	mlSetFan        func(uintptr, uint32, uint32) int32
	mlFanAuto       func(uintptr, uint32) int32
	mlResetClocks   func(uintptr) int32
	mlSetClocks     func(uintptr, uint32, uint32) int32
	mlSetCoreOffset func(uintptr, int32) int32
	nvUnload        func() int32
	nvEnum          func(*[maxPhysicalGPUs]uintptr, *uint32) int32
	nvBus           func(uintptr, *uint32) int32
	nvPState        func(uintptr, uint32, uint32) int32
	devices         []device
}

type device struct {
	info     governor.GPU
	handle   uintptr
	nvHandle uintptr
	fans     uint32
}

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
		"nvmlDeviceGetUtilizationRates": &a.mlUtil, "nvmlDeviceGetTemperature": &a.mlTemperature,
		"nvmlDeviceGetNumFans": &a.mlFans, "nvmlDeviceSetFanSpeed_v2": &a.mlSetFan,
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

	a.nvapi, err = purego.Dlopen("libnvidia-api.so.1", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return nil, fmt.Errorf("load NVAPI: %w", err)
	}
	var query func(uint32) uintptr
	if err := register(a.nvapi, "nvapi_QueryInterface", &query); err != nil {
		return nil, err
	}
	bindNV := func(id uint32, target any) error {
		address := query(id)
		if address == 0 {
			return fmt.Errorf("NVAPI function 0x%08x is unavailable", id)
		}
		purego.RegisterFunc(target, address)
		return nil
	}
	var nvInit func() int32
	for id, target := range map[uint32]any{
		0x0150E828: &nvInit, 0xD22BDD7E: &a.nvUnload,
		0xE5AC921F: &a.nvEnum, 0x1BE0B8E5: &a.nvBus, 0x025BFB10: &a.nvPState,
	} {
		if err := bindNV(id, target); err != nil {
			return nil, err
		}
	}
	if status := nvInit(); status != nvapiOK {
		return nil, statusError("NvAPI_Initialize", status)
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
	var nvHandles [maxPhysicalGPUs]uintptr
	var nvCount uint32
	if status := a.nvEnum(&nvHandles, &nvCount); status != nvapiOK {
		return statusError("NvAPI_EnumPhysicalGPUs", status)
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
		var nvHandle uintptr
		for nvIndex := uint32(0); nvIndex < nvCount && nvIndex < maxPhysicalGPUs; nvIndex++ {
			var bus uint32
			if a.nvBus(nvHandles[nvIndex], &bus) == nvapiOK && bus == pci.Bus {
				nvHandle = nvHandles[nvIndex]
				break
			}
		}
		if nvHandle == 0 {
			return fmt.Errorf("NVAPI handle for NVIDIA GPU %d bus %02x was not found", index, pci.Bus)
		}
		busID := cString(pci.BusID[:])
		a.devices = append(a.devices, device{
			info: governor.GPU{Index: int(index), BusID: busID, DeviceID: pci.DeviceID >> 16,
				SubsystemID: pci.SubsystemID, MemoryBytes: memory.Total},
			handle: handle, nvHandle: nvHandle, fans: fans,
		})
	}
	runtime.KeepAlive(nvHandles)
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

func (a *api) SetPState(index, pstate int) error {
	device, err := a.device(index)
	if err != nil {
		return err
	}
	status := a.nvPState(device.nvHandle, uint32(pstate), 0)
	if status != nvapiOK {
		return statusError("NvAPI_GPU_SetForcePstate", status)
	}
	return nil
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
	if a.nvUnload != nil {
		if status := a.nvUnload(); status != nvapiOK {
			result = errors.Join(result, statusError("NvAPI_Unload", status))
		}
		a.nvUnload = nil
	}
	if a.mlShutdown != nil {
		if status := a.mlShutdown(); status != nvmlSuccess {
			result = errors.Join(result, statusError("nvmlShutdown", status))
		}
		a.mlShutdown = nil
	}
	if a.nvapi != 0 {
		result = errors.Join(result, purego.Dlclose(a.nvapi))
		a.nvapi = 0
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
