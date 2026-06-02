package logging

import (
	"fmt"
	"os"
	"sync"
)

// RotatingWriter is a size-based rotating log writer. When the active file would
// exceed maxBytes it is renamed to "<path>.1" (shifting older backups up) and a
// fresh file is opened, keeping at most maxBackups rotated files. It is safe for
// concurrent use, so it can back an slog handler directly.
//
// It exists so vedetta's own logs can never grow without bound (the unbounded
// log was a real operational liability). Crash dumps still go to the process's
// real stderr, which the out-of-process supervisor keeps small by killing a
// wedged process promptly.
type RotatingWriter struct {
	path       string
	maxBytes   int64
	maxBackups int

	mu   sync.Mutex
	file *os.File
	size int64
}

// NewRotatingWriter opens (or creates) path for appending and rotates it at
// maxBytes, keeping maxBackups rotated files. A non-positive maxBytes disables
// rotation; a negative maxBackups is treated as zero.
func NewRotatingWriter(path string, maxBytes int64, maxBackups int) (*RotatingWriter, error) {
	if maxBackups < 0 {
		maxBackups = 0
	}
	rw := &RotatingWriter{path: path, maxBytes: maxBytes, maxBackups: maxBackups}
	if err := rw.open(); err != nil {
		return nil, err
	}
	return rw, nil
}

func (rw *RotatingWriter) open() error {
	f, err := os.OpenFile(rw.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open log file %s: %w", rw.path, err)
	}
	size := int64(0)
	if info, statErr := f.Stat(); statErr == nil {
		size = info.Size()
	}
	rw.file = f
	rw.size = size
	return nil
}

// Write appends p, rotating first if it would push the active file over the cap.
func (rw *RotatingWriter) Write(p []byte) (int, error) {
	rw.mu.Lock()
	defer rw.mu.Unlock()

	if rw.maxBytes > 0 && rw.size > 0 && rw.size+int64(len(p)) > rw.maxBytes {
		if err := rw.rotate(); err != nil {
			// Rotation failed (e.g. disk issue); keep writing to the current file
			// rather than dropping logs.
			_ = err
		}
	}

	n, err := rw.file.Write(p)
	rw.size += int64(n)
	return n, err
}

// rotate closes the active file, shifts backups up by one (dropping the oldest),
// renames the active file to "<path>.1", and opens a fresh active file.
func (rw *RotatingWriter) rotate() error {
	if err := rw.file.Close(); err != nil {
		return err
	}

	if rw.maxBackups <= 0 {
		// No backups kept: just truncate by reopening.
		return rw.reopenTruncated()
	}

	_ = os.Remove(fmt.Sprintf("%s.%d", rw.path, rw.maxBackups)) // drop the oldest
	for i := rw.maxBackups - 1; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", rw.path, i), fmt.Sprintf("%s.%d", rw.path, i+1))
	}
	_ = os.Rename(rw.path, fmt.Sprintf("%s.1", rw.path))

	return rw.reopenTruncated()
}

func (rw *RotatingWriter) reopenTruncated() error {
	f, err := os.OpenFile(rw.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("reopen log file %s: %w", rw.path, err)
	}
	rw.file = f
	rw.size = 0
	return nil
}

// Close closes the active log file.
func (rw *RotatingWriter) Close() error {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	if rw.file == nil {
		return nil
	}
	err := rw.file.Close()
	rw.file = nil
	return err
}
