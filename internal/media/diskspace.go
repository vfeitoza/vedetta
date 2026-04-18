package media

import (
	"sync"
	"syscall"
	"time"
)

const (
	diskCheckInterval      = 5 * time.Second
	thresholdCacheInterval = 5 * time.Minute
	thresholdLookbackHours = 1
	defaultStaticFloor     = 256 * 1024 * 1024 // matches the old MinDiskSpace constant
)

// LargestSegmentProvider returns the largest segment size (bytes) seen
// since the given time. Allows DiskSpace to size the dynamic threshold
// without importing the storage package.
type LargestSegmentProvider func(since time.Time) (int64, error)

// DiskSpace provides cached filesystem space queries and a dynamic
// minimum-free-space threshold. Multiple recording consumers share one
// monitor per recording path, avoiding redundant syscalls on every packet.
type DiskSpace struct {
	path string

	mu        sync.Mutex
	available uint64
	checkedAt time.Time

	thMu          sync.Mutex
	staticFloor   uint64
	provider      LargestSegmentProvider
	cachedMin     uint64
	thresholdTime time.Time
}

// NewDiskSpace creates a monitor for the filesystem containing path.
// The default static threshold is 256 MiB; configure with SetThreshold.
func NewDiskSpace(path string) *DiskSpace {
	ds := &DiskSpace{path: path, staticFloor: defaultStaticFloor}
	ds.refresh()
	return ds
}

// Available returns the bytes available on the filesystem. The value is
// cached for diskCheckInterval to avoid excessive syscalls.
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

// SetThreshold configures the minimum-free-space threshold. `staticFloor`
// is the always-enforced minimum (typically from config). `provider` may
// be nil; when set, MinRequired() periodically queries it to bump the
// threshold above 2× the largest recent segment. Calling SetThreshold
// invalidates the threshold cache.
func (ds *DiskSpace) SetThreshold(staticFloor uint64, provider LargestSegmentProvider) {
	ds.thMu.Lock()
	defer ds.thMu.Unlock()
	if staticFloor == 0 {
		staticFloor = defaultStaticFloor
	}
	ds.staticFloor = staticFloor
	ds.provider = provider
	ds.cachedMin = 0
	ds.thresholdTime = time.Time{}
}

// MinRequired returns the effective minimum free-disk threshold. The
// value is cached for thresholdCacheInterval to avoid hammering the DB.
func (ds *DiskSpace) MinRequired() uint64 {
	ds.thMu.Lock()
	defer ds.thMu.Unlock()

	if ds.cachedMin > 0 && time.Since(ds.thresholdTime) < thresholdCacheInterval {
		return ds.cachedMin
	}

	min := ds.staticFloor
	if ds.provider != nil {
		since := time.Now().Add(-thresholdLookbackHours * time.Hour)
		if largest, err := ds.provider(since); err == nil && largest > 0 {
			dyn := uint64(largest) * 2
			if dyn > min {
				min = dyn
			}
		}
	}

	ds.cachedMin = min
	ds.thresholdTime = time.Now()
	return min
}
