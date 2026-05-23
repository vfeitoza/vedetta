package safepath

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestJoin_StaysUnderRoot(t *testing.T) {
	root := t.TempDir()
	got, err := Join(root, "cam", "clips", "2026-01-01")
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	want := filepath.Join(root, "cam", "clips", "2026-01-01")
	if got != want {
		t.Errorf("Join = %q, want %q", got, want)
	}
}

func TestJoin_EmptyRootRejected(t *testing.T) {
	for _, root := range []string{"", "   ", "\t"} {
		if _, err := Join(root, "x"); err == nil {
			t.Errorf("Join(%q) should reject empty root", root)
		}
	}
}

func TestJoin_RejectsTraversal(t *testing.T) {
	root := t.TempDir()
	cases := [][]string{
		{".."},
		{"..", ".."},
		{"cam", "..", "..", "etc"},
		{"../etc/passwd"},
		{"cam", "../../.."},
	}
	for _, elems := range cases {
		if got, err := Join(root, elems...); err == nil {
			t.Errorf("Join(root, %v) = %q, want traversal error", elems, got)
		}
	}
}

func TestJoin_AbsoluteElementIsContained(t *testing.T) {
	root := t.TempDir()
	// An absolute-looking element is joined under root, not treated as a new
	// root, so it cannot escape.
	got, err := Join(root, "/etc/passwd")
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if !strings.HasPrefix(got, root+string(filepath.Separator)) {
		t.Errorf("Join = %q, want a path under %q", got, root)
	}
	if got != filepath.Join(root, "etc", "passwd") {
		t.Errorf("Join = %q, want %q", got, filepath.Join(root, "etc", "passwd"))
	}
}

func TestJoin_InnerDotDotThatResolvesInsideIsAllowed(t *testing.T) {
	root := t.TempDir()
	got, err := Join(root, "a", "..", "b")
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if got != filepath.Join(root, "b") {
		t.Errorf("Join = %q, want %q", got, filepath.Join(root, "b"))
	}
}

func TestJoin_LeadingDotDotInNameNotTreatedAsTraversal(t *testing.T) {
	root := t.TempDir()
	// "..foo" is a legitimate filename, not a parent reference.
	got, err := Join(root, "..foo")
	if err != nil {
		t.Fatalf("Join(..foo): %v", err)
	}
	if got != filepath.Join(root, "..foo") {
		t.Errorf("Join = %q, want %q", got, filepath.Join(root, "..foo"))
	}
}

func TestFileComponent_PreservesSafeCharacters(t *testing.T) {
	cases := map[string]string{
		"person":     "person",
		"front-door": "front-door",
		"a.b_c-d":    "a.b_c-d",
		"Cam01":      "Cam01",
	}
	for in, want := range cases {
		if got := FileComponent(in); got != want {
			t.Errorf("FileComponent(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFileComponent_ReplacesUnsafeCharacters(t *testing.T) {
	cases := map[string]string{
		"Back Yard":  "Back_Yard",
		"garage:cam": "garage_cam",
		"a/b":        "a_b",
		"a\\b":       "a_b",
		"café":       "caf", // é -> '_' then trailing underscore trimmed
	}
	for in, want := range cases {
		if got := FileComponent(in); got != want {
			t.Errorf("FileComponent(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFileComponent_NeutralizesPathTraversal(t *testing.T) {
	// A traversal attempt routed through FileComponent must not retain any
	// separator or leading parent reference.
	got := FileComponent("../../etc/passwd")
	if strings.ContainsAny(got, `/\`) {
		t.Errorf("FileComponent kept a path separator: %q", got)
	}
	if strings.HasPrefix(got, "..") {
		t.Errorf("FileComponent kept a leading parent reference: %q", got)
	}
	if got != "etc_passwd" {
		t.Errorf("FileComponent(../../etc/passwd) = %q, want etc_passwd", got)
	}
}

func TestFileComponent_TrimsLeadingTrailingPunctuation(t *testing.T) {
	cases := map[string]string{
		"__foo__":  "foo",
		".-hidden": "hidden",
		"name...":  "name",
	}
	for in, want := range cases {
		if got := FileComponent(in); got != want {
			t.Errorf("FileComponent(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFileComponent_EmptyAndAllUnsafeFallBackToItem(t *testing.T) {
	for _, in := range []string{"", "   ", "!!!", "...", "/\\/"} {
		if got := FileComponent(in); got != "item" {
			t.Errorf("FileComponent(%q) = %q, want item", in, got)
		}
	}
}

func TestFileComponent_CapsLength(t *testing.T) {
	got := FileComponent(strings.Repeat("a", 500))
	if len(got) > 160 {
		t.Errorf("FileComponent length = %d, want <= 160", len(got))
	}
}
