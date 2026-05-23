package media

import (
	"bytes"
	"testing"

	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/mp4/codecs"
)

func TestIdentifyTracks_PicksH264AndAudio(t *testing.T) {
	srcInit := fmp4.Init{
		Tracks: []*fmp4.InitTrack{
			{ID: 1, TimeScale: 90000, Codec: &codecs.H264{SPS: []byte{nalSPS}, PPS: []byte{nalPPS}}},
			{ID: 2, TimeScale: 48000, Codec: &codecs.Opus{ChannelCount: 2}},
		},
	}
	videoID, audioID, codec, err := identifyTracks(srcInit)
	if err != nil {
		t.Fatalf("identifyTracks: %v", err)
	}
	if videoID != 1 {
		t.Errorf("videoTrackID = %d, want 1", videoID)
	}
	if audioID != 2 {
		t.Errorf("audioTrackID = %d, want 2", audioID)
	}
	if codec == nil || !bytes.Equal(codec.SPS, []byte{nalSPS}) {
		t.Errorf("h264 codec = %v, want SPS [67]", codec)
	}
}

func TestIdentifyTracks_ErrorsWithoutVideo(t *testing.T) {
	srcInit := fmp4.Init{
		Tracks: []*fmp4.InitTrack{
			{ID: 1, TimeScale: 48000, Codec: &codecs.Opus{ChannelCount: 2}},
		},
	}
	if _, _, _, err := identifyTracks(srcInit); err == nil {
		t.Fatal("expected error when no H264 video track is present")
	}
}

func TestCollectMoofBlocks_DedupsByMoofOffsetAndSkipsEmptyMdat(t *testing.T) {
	frags := []fragment{
		// Two trafs of the same moof (multi-track) share one mdat → one block.
		{moofOffset: 100, moofSize: 40, mdatOffset: 140, mdatSize: 500},
		{moofOffset: 100, moofSize: 40, mdatOffset: 140, mdatSize: 500},
		// An empty mdat is skipped entirely.
		{moofOffset: 700, moofSize: 40, mdatOffset: 740, mdatSize: 0},
		// A distinct moof produces another block.
		{moofOffset: 800, moofSize: 40, mdatOffset: 840, mdatSize: 300},
	}
	blocks := collectMoofBlocks(frags)
	if len(blocks) != 2 {
		t.Fatalf("got %d blocks, want 2", len(blocks))
	}
	if blocks[0].moofOffset != 100 || blocks[0].mdatSize != 500 {
		t.Errorf("block 0 = %+v, want moofOffset 100 mdatSize 500", blocks[0])
	}
	if blocks[1].moofOffset != 800 || blocks[1].mdatSize != 300 {
		t.Errorf("block 1 = %+v, want moofOffset 800 mdatSize 300", blocks[1])
	}
}

func TestComputeVideoFPS_FromFirstVideoTrafDuration(t *testing.T) {
	frags := []fragment{
		{trafs: []trafEntry{{trackID: 1, duration: 3000}}},
	}
	if got := computeVideoFPS(frags, 1, 90000); got != 30 {
		t.Errorf("fps = %v, want 30 (90000/3000)", got)
	}
}

func TestComputeVideoFPS_ClampsImplausibleRates(t *testing.T) {
	// 90000/1000 = 90 fps is implausible for a recording → clamp to the 15 default.
	frags := []fragment{{trafs: []trafEntry{{trackID: 1, duration: 1000}}}}
	if got := computeVideoFPS(frags, 1, 90000); got != 15 {
		t.Errorf("fps = %v, want clamp to 15", got)
	}
}

func TestComputeVideoFPS_DefaultsWhenNoVideoTraf(t *testing.T) {
	frags := []fragment{{trafs: []trafEntry{{trackID: 2, duration: 3000}}}}
	if got := computeVideoFPS(frags, 1, 90000); got != 15 {
		t.Errorf("fps = %v, want default 15 when no video traf", got)
	}
}

func TestExtractIDRAccessUnit_KeepsParameterSetsAndIDROnly(t *testing.T) {
	annexB := bytes.Join([][]byte{
		annexBStartCode4(nalSPS, 0x11),
		annexBStartCode4(nalPPS, 0x22),
		annexBStartCode4(nalIDR, 0xAA),
		annexBStartCode4(nalSlice, 0xBB), // P-frame, must be dropped
		annexBStartCode4(0x06, 0xCC),     // SEI (type 6), must be dropped
	}, nil)

	au := extractIDRAccessUnit(annexB)
	nals := splitAnnexB(au)
	if len(nals) != 3 {
		t.Fatalf("got %d NALs in access unit, want 3 (SPS, PPS, IDR)", len(nals))
	}
	types := []byte{nals[0][0] & 0x1f, nals[1][0] & 0x1f, nals[2][0] & 0x1f}
	if !bytes.Equal(types, []byte{7, 8, 5}) {
		t.Errorf("NAL types = %v, want [7 8 5]", types)
	}
}

func TestExtractIDRAccessUnit_EmptyWhenNoKeyframe(t *testing.T) {
	annexB := annexBStartCode4(nalSlice, 0xBB) // only a P-frame
	if au := extractIDRAccessUnit(annexB); len(au) != 0 {
		t.Errorf("access unit = %v, want empty when no SPS/PPS/IDR", au)
	}
}

func TestBuildOutputInit_VideoFirstAudioPreserved(t *testing.T) {
	srcInit := fmp4.Init{
		Tracks: []*fmp4.InitTrack{
			{ID: 1, TimeScale: 90000, Codec: &codecs.H264{}},
			{ID: 2, TimeScale: 48000, Codec: &codecs.Opus{ChannelCount: 2}},
		},
	}
	out := buildOutputInit(srcInit, 1, 2, 90000, []byte{nalSPS, 0x11}, []byte{nalPPS, 0x22})
	if len(out.Tracks) != 2 {
		t.Fatalf("got %d output tracks, want 2", len(out.Tracks))
	}
	vt := out.Tracks[0]
	if vt.ID != 1 || vt.TimeScale != 90000 {
		t.Errorf("video track = ID %d TS %d, want 1/90000", vt.ID, vt.TimeScale)
	}
	h264, ok := vt.Codec.(*codecs.H264)
	if !ok || !bytes.Equal(h264.SPS, []byte{nalSPS, 0x11}) || !bytes.Equal(h264.PPS, []byte{nalPPS, 0x22}) {
		t.Errorf("video codec = %+v, want H264 with the supplied SPS/PPS", vt.Codec)
	}
	at := out.Tracks[1]
	if at.ID != 2 || at.TimeScale != 48000 {
		t.Errorf("audio track = ID %d TS %d, want 2/48000", at.ID, at.TimeScale)
	}
}

func TestBuildOutputInit_VideoOnlyWhenNoAudio(t *testing.T) {
	srcInit := fmp4.Init{
		Tracks: []*fmp4.InitTrack{
			{ID: 1, TimeScale: 90000, Codec: &codecs.H264{}},
		},
	}
	out := buildOutputInit(srcInit, 1, 0, 90000, []byte{nalSPS}, []byte{nalPPS})
	if len(out.Tracks) != 1 {
		t.Fatalf("got %d output tracks, want 1 (video only)", len(out.Tracks))
	}
}
