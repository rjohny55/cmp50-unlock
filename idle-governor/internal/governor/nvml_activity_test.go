package governor

import (
	"io"
	"log"
	"testing"
	"time"
)

func TestAnyCoreOrMemoryActivityRestartsTheEntireIdleWindow(t *testing.T) {
	hardware := &fakeHardware{gpus: []GPU{supportedGPU(0)}}
	g, err := New(hardware, Options{ControlDirectory: t.TempDir(), Poll: 30 * time.Second, IdleAfter: 2, UtilThreshold: 0, Logger: log.New(io.Discard, "", 0)})
	if err != nil {
		t.Fatal(err)
	}
	g.control, g.active = Control{Enabled: true, Profile: "cmp50-1x20gb"}, true
	poll := func() {
		t.Helper()
		if err := g.poll(); err != nil {
			t.Fatal(err)
		}
	}
	poll() // First fully quiet window.
	hardware.util = 1
	poll()
	if g.states[0].idlePolls != 0 || len(hardware.pstates) != 0 {
		t.Fatal("1% core activity was counted as idle")
	}
	hardware.util = 0
	poll()
	hardware.memUtil = 1
	poll()
	if g.states[0].idlePolls != 0 || len(hardware.pstates) != 0 {
		t.Fatal("1% memory-controller activity was counted as idle")
	}
	hardware.memUtil = 0
	poll()
	if g.states[0].lowPower {
		t.Fatal("one quiet interval is not enough")
	}
	poll()
	if !g.states[0].lowPower {
		t.Fatal("idle power saving never resumed")
	}
	hardware.memUtil = 1
	poll()
	if g.states[0].lowPower || hardware.pstates[len(hardware.pstates)-1] != 16 {
		t.Fatal("memory work did not release P8")
	}
}
