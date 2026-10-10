//go:build linux

package audio

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// ModelHandle holds an open anonymous in-memory file descriptor for std.rnnn.
type ModelHandle struct {
	fd   int
	file *os.File
	path string
}

// OpenEmbeddedModel creates an anonymous memfd containing std.rnnn and returns its handle.
func OpenEmbeddedModel() (*ModelHandle, error) {
	fd, err := unix.MemfdCreate("lecturejet_std_rnnn", unix.MFD_CLOEXEC)
	if err != nil {
		return nil, fmt.Errorf("creating memfd for RNNoise model: %w", err)
	}
	file := os.NewFile(uintptr(fd), "lecturejet_std_rnnn")
	if _, err := file.Write(stdModelBytes); err != nil {
		file.Close()
		return nil, fmt.Errorf("writing embedded RNNoise model to memfd: %w", err)
	}
	return &ModelHandle{
		fd:   fd,
		file: file,
		path: fmt.Sprintf("/proc/self/fd/%d", fd),
	}, nil
}

// Path returns the /proc/self/fd/<fd> virtual path.
func (m *ModelHandle) Path() string {
	if m == nil {
		return ""
	}
	return m.path
}

// Close closes the file descriptor, freeing the anonymous in-memory buffer.
func (m *ModelHandle) Close() error {
	if m == nil || m.file == nil {
		return nil
	}
	err := m.file.Close()
	m.file = nil
	m.fd = -1
	m.path = ""
	return err
}
