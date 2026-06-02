package camera

import "testing"

func TestEventCategory(t *testing.T) {
	cases := []struct {
		label string
		moved bool
		want  string
	}{
		{"car", false, CategoryDetection},        // parked car -> low priority
		{"car", true, CategoryAlert},             // car traveling -> alert
		{"truck", false, CategoryDetection},      // parked truck -> low priority
		{"motorcycle", false, CategoryDetection}, // parked motorcycle -> low priority
		{"bus", true, CategoryAlert},             // moving bus -> alert
		{"person", false, CategoryAlert},         // stationary person is still an alert
		{"person", true, CategoryAlert},
		{"dog", false, CategoryAlert},     // animals are always alerts
		{"bicycle", false, CategoryAlert}, // not in the stationary tier
	}
	for _, c := range cases {
		if got := eventCategory(c.label, c.moved); got != c.want {
			t.Errorf("eventCategory(%q, moved=%v) = %q, want %q", c.label, c.moved, got, c.want)
		}
	}
}
