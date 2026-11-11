package governor

import (
	"io"
	"log"
	"testing"
	"time"
)

func TestAnyCoreOrMemoryActivityRestartsIdleDelay(t *testing.T) {
	now := time.Unix(1000, 0)
	h := &fakeHardware{gpus: []GPU{supportedGPU(0)}}
	g, e := New(h, Options{IdleDelay: 5 * time.Second, Now: func() time.Time { return now }, ControlDirectory: t.TempDir(), Poll: time.Second, IdleAfter: 2, UtilThreshold: 0, Logger: log.New(io.Discard, "", 0)})
	if e != nil {
		t.Fatal(e)
	}
	g.control, g.active = Control{Enabled: true, Profile: "cmp50-1x20gb"}, true
	poll := func() {
		t.Helper()
		if e := g.poll(); e != nil {
			t.Fatal(e)
		}
	}
	poll()
	h.util = 1
	poll()
	if g.states[0].stage != "active" {
		t.Fatal("1% core work ignored")
	}
	h.util = 0
	now = now.Add(4 * time.Second)
	poll()
	if g.states[0].stage != "active" {
		t.Fatal("between-token gap capped clocks")
	}
	h.memUtil = 1
	poll()
	h.memUtil = 0
	now = now.Add(4 * time.Second)
	poll()
	if g.states[0].stage != "active" {
		t.Fatal("memory work did not refresh grace")
	}
	now = now.Add(time.Second)
	poll()
	if g.states[0].stage != "warm" {
		t.Fatal("quiet window never re-enabled saving")
	}
	now = now.Add(24 * time.Hour)
	poll()
	if g.states[0].stage != "warm" || len(h.pstates) != 0 {
		t.Fatal("long idle forced a P-state")
	}
	h.memUtil = 1
	poll()
	if g.states[0].stage != "active" || g.states[0].clockManaged {
		t.Fatal("memory work did not restore auto clocks")
	}
}
