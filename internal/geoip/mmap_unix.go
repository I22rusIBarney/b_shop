//go:build unix

package geoip

import (
	"os"
	"syscall"
)

// readFile maps the database read-only: a 130 MB city database then costs address space, not memory.
func readFile(path string) ([]byte, func() error, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	if st.Size() < 64 || st.Size() > 1<<31-1 {
		return nil, nil, os.ErrInvalid
	}
	b, err := syscall.Mmap(int(f.Fd()), 0, int(st.Size()), syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		return nil, nil, err
	}
	return b, func() error { return syscall.Munmap(b) }, nil
}
