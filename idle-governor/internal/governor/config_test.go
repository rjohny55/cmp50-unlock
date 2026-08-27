package governor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadControl(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "control.json")
	if err := os.WriteFile(path, []byte(`{"enabled":true,"profile":"cmp50-4x20gb","idle_fan_percent":20}`), 0600); err != nil {
		t.Fatal(err)
	}
	control, err := LoadControl(path)
	if err != nil {
		t.Fatal(err)
	}
	if !control.Enabled || control.Profile != "cmp50-4x20gb" || control.IdleFanPercent != 20 {
		t.Fatalf("unexpected control: %+v", control)
	}
}

func TestLoadControlRejectsUnknownAndSymlink(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, []byte(`{"enabled":false,"profile":"cmp50-1x20gb","idle_fan_percent":0,"command":"oops"}`), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "control.json")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadControl(path); err == nil {
		t.Fatal("expected symlink rejection")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadControl(path); err == nil {
		t.Fatal("expected unknown field rejection")
	}
}

func TestParseClock(t *testing.T) {
	minimum, maximum, present, err := parseClock("1700,1980")
	if err != nil || !present || minimum != 1700 || maximum != 1980 {
		t.Fatalf("unexpected result: %d %d %v %v", minimum, maximum, present, err)
	}
	for _, value := range []string{"99", "2000,1000", "1,2,3", "bad"} {
		if _, _, _, err := parseClock(value); err == nil {
			t.Errorf("expected %q to fail", value)
		}
	}
}
