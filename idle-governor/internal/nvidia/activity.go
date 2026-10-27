package nvidia

import (
	"errors"
	"fmt"
	"runtime"
	"time"

	"github.com/ebitengine/purego"
)

// nvmlSample_t: timestamp followed by an 8-byte nvmlValue_t union.
type activitySample struct{ Timestamp, Value uint64 }
type samplesFn func(uintptr, uint32, uint64, *uint32, *uint32, *activitySample) int32

func activityPeak(samples []activitySample, since, now uint64) (uint32, uint64, error) {
	var peak uint32
	var first, last uint64
	for _, sample := range samples {
		if sample.Timestamp <= since {
			continue
		}
		if first == 0 || sample.Timestamp < first {
			first = sample.Timestamp
		}
		if sample.Timestamp > last {
			last = sample.Timestamp
		}
		value := uint32(sample.Value) // NVML_VALUE_TYPE_UNSIGNED_INT (1).
		if value > 100 {
			return 0, last, errors.New("NVML history utilization is invalid")
		}
		if value > peak {
			peak = value
		}
	}
	// Missing, stale or truncated history is not evidence of a quiet interval.
	if first == 0 || first > since+2500000 || last+5000000 < now {
		return 0, last, errors.New("NVML history does not cover the requested interval")
	}
	return peak, last, nil
}

func sampleActivity(fn samplesFn, handle uintptr, kind uint32, since uint64) (uint32, uint64, error) {
	buffer := make([]activitySample, 512)
	count, valueType := uint32(len(buffer)), uint32(0)
	status := fn(handle, kind, since, &valueType, &count, &buffer[0])
	runtime.KeepAlive(buffer)
	if status != nvmlSuccess || count > uint32(len(buffer)) || valueType != 1 {
		return 0, 0, fmt.Errorf("NVML activity history unavailable (status %d, type %d)", status, valueType)
	}
	return activityPeak(buffer[:count], since, uint64(time.Now().UnixMicro()))
}

type ActivitySample struct {
	Index       int
	GPU, Memory uint32
}

// RecentActivity is a read-only NVML operation for the legacy shell service.
// No NVAPI initialization, power/clock changes, HTTP, ports or disk state.
func RecentActivity(window time.Duration) ([]ActivitySample, error) {
	lib, err := purego.Dlopen("libnvidia-ml.so.1", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return nil, err
	}
	defer purego.Dlclose(lib)
	var init, shutdown func() int32
	var countFn func(*uint32) int32
	var handleFn func(uint32, *uintptr) int32
	var sampleFn samplesFn
	for name, target := range map[string]any{"nvmlInit_v2": &init, "nvmlShutdown": &shutdown,
		"nvmlDeviceGetCount_v2": &countFn, "nvmlDeviceGetHandleByIndex_v2": &handleFn, "nvmlDeviceGetSamples": &sampleFn} {
		if err := register(lib, name, target); err != nil {
			return nil, err
		}
	}
	if status := init(); status != 0 {
		return nil, statusError("nvmlInit_v2", status)
	}
	defer shutdown()
	var count uint32
	if status := countFn(&count); status != 0 {
		return nil, statusError("nvmlDeviceGetCount_v2", status)
	}
	if count > maxPhysicalGPUs {
		return nil, errors.New("too many GPUs")
	}
	since := uint64(time.Now().Add(-window).UnixMicro())
	rows := make([]ActivitySample, 0, count)
	for index := uint32(0); index < count; index++ {
		var handle uintptr
		if status := handleFn(index, &handle); status != 0 {
			return nil, statusError("nvmlDeviceGetHandleByIndex_v2", status)
		}
		gpu, _, err := sampleActivity(sampleFn, handle, 1, since)
		if err != nil {
			return nil, err
		}
		memory, _, err := sampleActivity(sampleFn, handle, 2, since)
		if err != nil {
			return nil, err
		}
		rows = append(rows, ActivitySample{Index: int(index), GPU: gpu, Memory: memory})
	}
	return rows, nil
}
