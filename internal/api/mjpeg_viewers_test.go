package api

import "testing"

func TestMJPEGViewers(t *testing.T) {
	v := newMJPEGViewers()
	v.add("front")
	v.add("front")
	v.add("back")
	if c := v.counts(); c["front"] != 2 || c["back"] != 1 {
		t.Fatalf("counts() = %v, want front=2 back=1", c)
	}
	v.remove("front")
	if c := v.counts(); c["front"] != 1 {
		t.Fatalf("after remove counts()[front] = %d, want 1", c["front"])
	}
	v.remove("back")
	if _, ok := v.counts()["back"]; ok {
		t.Fatalf("back should be absent after dropping to zero")
	}
}
