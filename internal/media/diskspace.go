package media

import (
	"sync"
	"syscall"
	"time"
)

// DiskSpace provides cached filesystem space queries.
// Multiple recording consumers share one monitor per recording path,
// avoiding redundant syscalls on every packet.
type DiskSpace struct {
	path string

	mu        sync.Mutex
	available uint64
	checkedAt time.Time
}

const diskCheckInterval = 5 * time.Second

// NewDiskSpace creates a monitor for the filesystem containing path.
func NewDiskSpace(path string) *DiskSpace {
	ds := &DiskSpace{path: path}
	ds.refresh()
	return ds
}

// Available returns the bytes available on the filesystem.
// The value is cached for diskCheckInterval to avoid excessive syscalls.
func (ds *DiskSpace) Available() uint64 {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	if time.Since(ds.checkedAt) > diskCheckInterval {
		ds.refresh()
	}
	return ds.available
}

func (ds *DiskSpace) refresh() {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(ds.path, &stat); err != nil {
		return
	}
	ds.available = stat.Bavail * uint64(stat.Bsize)
	ds.checkedAt = time.Now()
}
