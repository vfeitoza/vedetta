package recording

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/rvben/vedetta/internal/media"
)

// transcodeSubprocessTimeout bounds a single out-of-process transcode. Normal
// clips and segments finish in seconds; a child still running far past this is
// treated as a failure and killed, so a wedged transcode can never stall the
// recompression worker.
const transcodeSubprocessTimeout = 5 * time.Minute

// selfExecutable resolves the path of the running binary to re-exec. It is a
// package var so tests can point it at a freshly built binary.
var selfExecutable = os.Executable

// maxSubprocessStderr bounds how much child stderr is quoted into an error, so a
// fatal-error goroutine dump can't flood the log on a failed transcode.
const maxSubprocessStderr = 2048

// outOfProcessTranscode transcodes path in place by invoking this binary's
// hidden `transcode` subcommand as a short-lived child process. Recompression's
// OpenH264 encode path can corrupt the Go heap on certain inputs; running it in
// a throwaway process means such a crash kills only the child and fails one
// segment, rather than taking down the long-running NVR. It returns the child's
// TranscodeResult, or an error if the child failed, crashed, or timed out.
func outOfProcessTranscode(path string, targetW, targetH int) (media.TranscodeResult, error) {
	self, err := selfExecutable()
	if err != nil {
		return media.TranscodeResult{}, fmt.Errorf("locate self executable: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), transcodeSubprocessTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, self, "transcode",
		"-w", strconv.Itoa(targetW), "-h", strconv.Itoa(targetH), path)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return media.TranscodeResult{}, fmt.Errorf("transcode subprocess timed out after %s", transcodeSubprocessTimeout)
	}
	if runErr != nil {
		return media.TranscodeResult{}, fmt.Errorf("transcode subprocess failed: %w (stderr: %s)", runErr, trimStderr(stderr.String()))
	}

	res, err := parseTranscodeResult(stdout.Bytes())
	if err != nil {
		return media.TranscodeResult{}, fmt.Errorf("%w (stderr: %s)", err, trimStderr(stderr.String()))
	}
	return res, nil
}

// parseTranscodeResult extracts the marker-prefixed JSON result line from the
// child's stdout. Scanning for the marker tolerates any other lines the
// OpenH264 C library may emit to stdout.
func parseTranscodeResult(stdout []byte) (media.TranscodeResult, error) {
	for _, line := range strings.Split(string(stdout), "\n") {
		payload, ok := strings.CutPrefix(line, media.TranscodeResultMarker)
		if !ok {
			continue
		}
		var res media.TranscodeResult
		if err := json.Unmarshal([]byte(payload), &res); err != nil {
			return media.TranscodeResult{}, fmt.Errorf("decode transcode result %q: %w", payload, err)
		}
		return res, nil
	}
	return media.TranscodeResult{}, fmt.Errorf("transcode subprocess produced no result line")
}

// trimStderr bounds child stderr (OpenH264 warnings plus any crash traceback)
// to a tail that still contains the failure, keeping logs readable.
func trimStderr(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxSubprocessStderr {
		return s
	}
	return "..." + s[len(s)-maxSubprocessStderr:]
}
