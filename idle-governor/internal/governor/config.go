package governor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

const maxControlSize = 4096

var Profiles = map[string]int{
	"cmp50-1x20gb": 1,
	"cmp50-2x20gb": 2,
	"cmp50-4x20gb": 4,
	"cmp50-6x20gb": 6,
	"cmp50-8x20gb": 8,
}

type Control struct {
	Enabled        bool   `json:"enabled"`
	Profile        string `json:"profile"`
	IdleFanPercent int    `json:"idle_fan_percent"`
}

func LoadControl(path string) (Control, error) {
	var control Control
	directory, err := openDirectory(filepath.Dir(path))
	if err != nil {
		return control, err
	}
	defer directory.Close()

	fd, err := syscall.Openat(int(directory.Fd()), filepath.Base(path),
		syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return control, err
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return control, errors.New("control request must be a regular file")
	}
	if info.Size() > maxControlSize {
		return control, errors.New("control request is too large")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxControlSize+1))
	if err != nil {
		return control, err
	}
	if len(data) > maxControlSize {
		return control, errors.New("control request is too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&control); err != nil {
		return control, fmt.Errorf("decode control request: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return control, errors.New("control request contains trailing JSON")
	}
	if _, ok := Profiles[control.Profile]; !ok {
		return control, errors.New("unsupported CMP50 profile")
	}
	if control.IdleFanPercent != 0 && (control.IdleFanPercent < 15 || control.IdleFanPercent > 100) {
		return control, errors.New("idle fan must be automatic (0) or 15..100 percent")
	}
	return control, nil
}

func openDirectory(path string) (*os.File, error) {
	fd, err := syscall.Open(path,
		syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}
