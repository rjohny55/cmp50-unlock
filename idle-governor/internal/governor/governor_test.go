package governor

import (
	"context"
	"errors"
	"io"
	"log"
	"sync"
	"testing"
	"time"
)

type fakeHardware struct {
	mu          sync.Mutex
	gpus        []GPU
	util        uint32
	temp        uint32
	pstates     []int
	fans        []uint32
	autoFans    int
	resetClocks int
	setClocks   [][2]uint32
	coreOffsets []int32
}

func (f *fakeHardware) GPUs() []GPU { return append([]GPU(nil), f.gpus...) }
func (f *fakeHardware) Utilization(int) (uint32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.util, nil
}
func (f *fakeHardware) Temperature(int) (uint32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.temp, nil
}
func (f *fakeHardware) SetPState(_ int, state int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pstates = append(f.pstates, state)
	return nil
}
func (f *fakeHardware) ResetLockedClocks(int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resetClocks++
	return nil
}
func (f *fakeHardware) SetLockedClocks(_ int, min, max uint32) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setClocks = append(f.setClocks, [2]uint32{min, max})
	return nil
}
func (f *fakeHardware) SetCoreOffset(_ int, offset int32) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.coreOffsets = append(f.coreOffsets, offset)
	return nil
}
func (f *fakeHardware) SetFan(_ int, percent uint32) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fans = append(f.fans, percent)
	return nil
}
func (f *fakeHardware) SetFanAuto(int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.autoFans++
	return nil
}
func (*fakeHardware) Close() error { return nil }

func supportedGPU(index int) GPU {
	return GPU{Index: index, DeviceID: 0x1e09, SubsystemID: 0x155410de, MemoryBytes: 20 * 1024 * 1024 * 1024}
}

func TestPollIdleBusyAndFanTemperature(t *testing.T) {
	hardware := &fakeHardware{gpus: []GPU{supportedGPU(0)}, temp: 28}
	g, err := New(hardware, Options{ControlDirectory: t.TempDir(), Poll: time.Second, IdleAfter: 2, UtilThreshold: 5, FanAutoTemp: 55, LoadClock: "1900", LoadCoreOffset: "225", Logger: log.New(io.Discard, "", 0)})
	if err != nil {
		t.Fatal(err)
	}
	g.control, g.active = Control{Enabled: true, Profile: "cmp50-1x20gb", IdleFanPercent: 20}, true
	if err := g.poll(); err != nil {
		t.Fatal(err)
	}
	if err := g.poll(); err != nil {
		t.Fatal(err)
	}
	hardware.mu.Lock()
	if len(hardware.pstates) != 1 || hardware.pstates[0] != 8 {
		t.Fatalf("P-states: %v", hardware.pstates)
	}
	if len(hardware.fans) != 1 || hardware.fans[0] != 20 {
		t.Fatalf("fans: %v", hardware.fans)
	}
	if len(hardware.coreOffsets) != 1 || hardware.coreOffsets[0] != 0 {
		t.Fatalf("idle offsets: %v", hardware.coreOffsets)
	}
	hardware.util = 100
	hardware.mu.Unlock()
	if err := g.poll(); err != nil {
		t.Fatal(err)
	}
	hardware.mu.Lock()
	defer hardware.mu.Unlock()
	if len(hardware.pstates) != 2 || hardware.pstates[1] != 16 {
		t.Fatalf("P-states: %v", hardware.pstates)
	}
	if hardware.autoFans != 1 {
		t.Fatalf("auto fan calls: %d", hardware.autoFans)
	}
	if len(hardware.setClocks) != 1 || hardware.setClocks[0] != [2]uint32{1900, 1900} {
		t.Fatalf("clock restore: %v", hardware.setClocks)
	}
	if len(hardware.coreOffsets) != 2 || hardware.coreOffsets[1] != 225 {
		t.Fatalf("restored offsets: %v", hardware.coreOffsets)
	}
}

func TestValidateRejectsWrongHardware(t *testing.T) {
	hardware := &fakeHardware{gpus: []GPU{{Index: 0, DeviceID: 0x220d, SubsystemID: 0x155410de, MemoryBytes: 20 * 1024 * 1024 * 1024}}}
	g, err := New(hardware, Options{ControlDirectory: t.TempDir(), Poll: time.Second, IdleAfter: 1, Logger: log.New(io.Discard, "", 0)})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.validate(Control{Profile: "cmp50-1x20gb"}); err == nil {
		t.Fatal("expected hardware rejection")
	}
}

func TestRunStopsWithoutGoroutineLeak(t *testing.T) {
	hardware := &fakeHardware{gpus: []GPU{supportedGPU(0)}}
	directory := t.TempDir()
	g, err := New(hardware, Options{ControlDirectory: directory, Poll: time.Millisecond, IdleAfter: 1, Logger: log.New(io.Discard, "", 0)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- g.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not stop")
	}
}
