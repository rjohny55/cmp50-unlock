package governor

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultThirtySecondGraceAndLongIdleNeverForcesPState(t *testing.T) {
	now := time.Unix(1000, 0)
	h := &fakeHardware{gpus: []GPU{supportedGPU(0)}}
	g, e := New(h, Options{Now: func() time.Time { return now }, ControlDirectory: t.TempDir(), Poll: time.Second, IdleAfter: 2, Logger: log.New(io.Discard, "", 0)})
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
	if h.setClocks[0] != [2]uint32{700, 700} {
		t.Fatal(h.setClocks)
	}
	h.util = 1
	poll()
	if g.states[0].clockManaged || h.resetClocks != 1 {
		t.Fatal("load must use stock automatic clocks")
	}
	h.util = 0
	now = now.Add(29 * time.Second)
	poll()
	if g.states[0].stage != "active" || g.states[0].clockManaged {
		t.Fatal("warm limit applied during grace")
	}
	now = now.Add(time.Second)
	poll()
	if g.states[0].stage != "warm" || !g.states[0].clockManaged {
		t.Fatal("idle frequency ceiling missing at 30s")
	}
	now = now.Add(24 * time.Hour)
	poll()
	if len(h.pstates) != 0 || g.states[0].stage != "warm" {
		t.Fatal("long idle tried a private P-state")
	}
	if e := g.status.Active(g.control); e != nil {
		t.Fatal(e)
	}
	if e := g.Release(); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"active-profile", "active-fan-percent", "power-stages.json"} {
		if _, e := os.Stat(filepath.Join(g.options.ControlDirectory, name)); !os.IsNotExist(e) {
			t.Fatal("stale active marker", name, e)
		}
	}
}

func TestWarmClockUsesEachCardsLimitAndAllowsSingleOverride(t *testing.T) {
	first, second := supportedGPU(0), supportedGPU(1)
	first.MaxGraphicsClock = 1800
	second.MaxGraphicsClock = 2100
	h := &fakeHardware{gpus: []GPU{first, second}}
	g, e := New(h, Options{ControlDirectory: t.TempDir(), Poll: time.Second, IdleAfter: 1})
	if e != nil {
		t.Fatal(e)
	}
	g.control = Control{Enabled: true, Profile: "cmp50-2x20gb"}
	if e = g.poll(); e != nil {
		t.Fatal(e)
	}
	if h.setClocks[0] != [2]uint32{600, 600} || h.setClocks[1] != [2]uint32{700, 700} {
		t.Fatal(h.setClocks)
	}
	g.options.WarmClock = "900"
	if e = g.poll(); e != nil {
		t.Fatal(e)
	}
	if h.setClocks[len(h.setClocks)-1] != [2]uint32{900, 900} {
		t.Fatal(h.setClocks)
	}
	raw, e := os.ReadFile(filepath.Join(g.options.ControlDirectory, "power-stages.json"))
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(raw), `"p8_enabled":false`) {
		t.Fatal(string(raw))
	}
}
