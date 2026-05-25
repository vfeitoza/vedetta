package stream

import "testing"

// A slow MSE viewer whose send buffer is full must have its frames dropped
// rather than blocking the broadcaster, and every drop must be counted so the
// silent degradation surfaces on /metrics.
func TestMSEManager_CountsClientDrops(t *testing.T) {
	m := NewMSEManager(nil, nil, nil)

	// conn is nil: send() only touches the channel and the drop counter.
	c := newMSEClient(nil, &m.dropped)

	// Fill the client buffer to capacity, then overflow it by 5 frames.
	for i := 0; i < mseClientChanSize+5; i++ {
		c.send([]byte("frame"))
	}

	if got := m.DroppedFrames(); got != 5 {
		t.Errorf("DroppedFrames() = %d, want 5 (%d buffered, 5 dropped)", got, mseClientChanSize)
	}
}
