package recording

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rvben/vedetta/internal/media"
)

var (
	sharedBinOnce sync.Once
	sharedBin     string
	sharedBinErr  error
)

// sharedVedettaBinary builds the vedetta binary once for the subprocess tests.
// os.Executable() inside `go test` is the test binary, which has no `transcode`
// subcommand, so the out-of-process path must re-exec a real build.
func sharedVedettaBinary(t *testing.T) string {
	t.Helper()
	sharedBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "vedetta-bin")
		if err != nil {
			sharedBinErr = err
			return
		}
		bin := filepath.Join(dir, "vedetta")
		if out, err := exec.Command("go", "build", "-o", bin, "github.com/rvben/vedetta/cmd/vedetta").CombinedOutput(); err != nil {
			sharedBinErr = fmt.Errorf("build vedetta: %v\n%s", err, out)
			return
		}
		sharedBin = bin
	})
	if sharedBinErr != nil {
		t.Fatalf("%v", sharedBinErr)
	}
	return sharedBin
}

// useTestBinary points selfExecutable at the freshly built binary for the
// duration of a test.
func useTestBinary(t *testing.T) {
	t.Helper()
	bin := sharedVedettaBinary(t)
	prev := selfExecutable
	selfExecutable = func() (string, error) { return bin, nil }
	t.Cleanup(func() { selfExecutable = prev })
}

func ensureOpenH264OrSkip(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	status, err := media.InstallOpenH264(ctx)
	if err != nil {
		t.Skipf("OpenH264 unavailable (skipping, not a failure): %v", err)
	}
	if !status.Available {
		t.Skip("OpenH264 reported unavailable after install")
	}
}

// TestOutOfProcessTranscode_MatchesInProcess verifies the isolated child
// produces the same result the in-process transcoder would, on the committed
// fixture.
func TestOutOfProcessTranscode_MatchesInProcess(t *testing.T) {
	ensureOpenH264OrSkip(t)
	useTestBinary(t)

	fixture := filepath.Join("..", "media", "testdata", "sample_segment.mp4")
	src, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	refPath := filepath.Join(t.TempDir(), "ref.mp4")
	if err := os.WriteFile(refPath, src, 0o644); err != nil {
		t.Fatal(err)
	}
	ref, err := media.TranscodeSegment(refPath, 1280, 720)
	if err != nil {
		t.Fatalf("in-process transcode: %v", err)
	}

	oopPath := filepath.Join(t.TempDir(), "oop.mp4")
	if err := os.WriteFile(oopPath, src, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := outOfProcessTranscode(oopPath, 1280, 720)
	if err != nil {
		t.Fatalf("out-of-process transcode: %v", err)
	}

	if got.Skipped != ref.Skipped || got.OriginalSize != ref.OriginalSize || got.NewSize != ref.NewSize {
		t.Errorf("out-of-process result %+v != in-process %+v", got, ref)
	}
	if got.NewSize <= 0 {
		t.Errorf("out-of-process produced non-positive NewSize: %d", got.NewSize)
	}

	// The child rewrites the file in place; its size must match the reported NewSize.
	info, err := os.Stat(oopPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != got.NewSize {
		t.Errorf("file size %d != reported NewSize %d", info.Size(), got.NewSize)
	}
}

// TestOutOfProcessTranscode_BadInputReturnsError verifies a child that fails
// (here, unparseable input) surfaces as an error rather than a panic or a
// false success. This is the path that, for a heap-corruption crash, keeps the
// NVR alive by failing a single clip.
func TestOutOfProcessTranscode_BadInputReturnsError(t *testing.T) {
	useTestBinary(t)

	badPath := filepath.Join(t.TempDir(), "bad.mp4")
	if err := os.WriteFile(badPath, []byte("this is not an mp4 file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := outOfProcessTranscode(badPath, 1280, 720); err == nil {
		t.Fatal("expected an error for garbage input, got nil")
	}
}

func TestParseTranscodeResult(t *testing.T) {
	t.Run("marker among noise", func(t *testing.T) {
		stdout := "[OpenH264] this = 0x123, Warning: blah\n" +
			media.TranscodeResultMarker + `{"original_size":100,"new_size":40,"skipped":false}` + "\n" +
			"trailing noise\n"
		res, err := parseTranscodeResult([]byte(stdout))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if res.OriginalSize != 100 || res.NewSize != 40 || res.Skipped {
			t.Errorf("unexpected result: %+v", res)
		}
	})

	t.Run("skipped result", func(t *testing.T) {
		res, err := parseTranscodeResult([]byte(media.TranscodeResultMarker + `{"skipped":true}` + "\n"))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if !res.Skipped {
			t.Errorf("expected Skipped=true, got %+v", res)
		}
	})

	t.Run("no marker is an error", func(t *testing.T) {
		if _, err := parseTranscodeResult([]byte("no result line here\n")); err == nil {
			t.Error("expected error when marker absent")
		}
	})
}
