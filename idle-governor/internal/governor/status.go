package governor

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type StatusWriter struct{ Directory string }

func (s StatusWriter) Active(control Control) error {
	if err := s.writeAtomic("active-profile", control.Profile+"\n"); err != nil {
		return err
	}
	if err := s.writeAtomic("active-fan-percent", fmt.Sprintf("%d\n", control.IdleFanPercent)); err != nil {
		return err
	}
	return s.remove("last-error")
}

func (s StatusWriter) Inactive() error {
	if err := s.remove("active-profile"); err != nil {
		return err
	}
	return s.remove("active-fan-percent")
}

func (s StatusWriter) Error(err error) {
	_ = s.Inactive()
	message := strings.TrimSpace(err.Error())
	if len(message) > 1000 {
		message = message[:1000]
	}
	_ = s.writeAtomic("last-error", message+"\n")
}

func (s StatusWriter) writeAtomic(name, content string) error {
	directory, err := openDirectory(s.Directory)
	if err != nil {
		return err
	}
	defer directory.Close()
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return err
	}
	temporaryName := fmt.Sprintf(".%s-%x.tmp", name, random)
	fd, err := syscall.Openat(int(directory.Fd()), temporaryName,
		syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0644)
	if err != nil {
		return err
	}
	temporary := os.NewFile(uintptr(fd), temporaryName)
	defer syscall.Unlinkat(int(directory.Fd()), temporaryName)
	if _, err := temporary.WriteString(content); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return syscall.Renameat(int(directory.Fd()), temporaryName, int(directory.Fd()), name)
}

func (s StatusWriter) remove(name string) error {
	directory, err := openDirectory(filepath.Clean(s.Directory))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer directory.Close()
	err = syscall.Unlinkat(int(directory.Fd()), name)
	if errors.Is(err, syscall.ENOENT) {
		return nil
	}
	return err
}
