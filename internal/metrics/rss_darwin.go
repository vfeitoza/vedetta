//go:build darwin && cgo

package metrics

/*
#include <mach/mach.h>

static int vedetta_read_rss(unsigned long long *out) {
	mach_task_basic_info_data_t info;
	mach_msg_type_number_t count = MACH_TASK_BASIC_INFO_COUNT;
	kern_return_t kr = task_info(mach_task_self(), MACH_TASK_BASIC_INFO,
		(task_info_t)&info, &count);
	if (kr != KERN_SUCCESS) {
		return 0;
	}
	*out = (unsigned long long)info.resident_size;
	return 1;
}
*/
import "C"

// readRSS returns current resident set size in bytes via the Mach
// MACH_TASK_BASIC_INFO task_info call.
func readRSS() (uint64, bool) {
	var out C.ulonglong
	if C.vedetta_read_rss(&out) == 0 {
		return 0, false
	}
	return uint64(out), true
}
