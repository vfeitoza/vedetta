package media

import (
	"fmt"
	"image"
	"io"
	"os"
	"runtime"
	"unsafe"

	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/mp4/codecs"
	openh264 "github.com/y9o/go-openh264"
)

// scaleYCbCr scales a YCbCr I420 image to fit within (targetW, targetH) while
// preserving aspect ratio. Output dimensions are always even (required by H264).
// Uses nearest-neighbour sampling — sufficient for downscaling security footage.
func scaleYCbCr(src *image.YCbCr, targetW, targetH int) *image.YCbCr {
	srcW := src.Rect.Dx()
	srcH := src.Rect.Dy()

	// Compute scale to fit within target box, preserve aspect ratio
	scaleW := float64(targetW) / float64(srcW)
	scaleH := float64(targetH) / float64(srcH)
	scale := scaleW
	if scaleH < scaleW {
		scale = scaleH
	}

	outW := int(float64(srcW)*scale/2) * 2 // round down to even
	outH := int(float64(srcH)*scale/2) * 2

	if outW <= 0 {
		outW = 2
	}
	if outH <= 0 {
		outH = 2
	}

	dst := image.NewYCbCr(image.Rect(0, 0, outW, outH), image.YCbCrSubsampleRatio420)

	for dy := range outH {
		sy := dy * srcH / outH
		for dx := range outW {
			sx := dx * srcW / outW
			dst.Y[dy*dst.YStride+dx] = src.Y[sy*src.YStride+sx]
		}
	}
	// Chroma planes (half resolution for I420)
	for dy := range outH / 2 {
		sy := dy * (srcH / 2) / (outH / 2)
		for dx := range outW / 2 {
			sx := dx * (srcW / 2) / (outW / 2)
			dst.Cb[dy*dst.CStride+dx] = src.Cb[sy*src.CStride+sx]
			dst.Cr[dy*dst.CStride+dx] = src.Cr[sy*src.CStride+sx]
		}
	}

	return dst
}

// shouldTranscode reports whether transcoding from (srcW, srcH) to (targetW, targetH)
// is worth doing. Returns (skip=true, 0, 0) when the source is already at or below
// the target size, or when the area reduction is less than 25%.
// When skip=false, returns the actual output dimensions (aspect-ratio-corrected, even).
func shouldTranscode(srcW, srcH, targetW, targetH int) (skip bool, outW, outH int) {
	// Already at or below target in both dimensions
	if srcW <= targetW && srcH <= targetH {
		return true, 0, 0
	}

	// Compute output dimensions preserving aspect ratio
	scaleW := float64(targetW) / float64(srcW)
	scaleH := float64(targetH) / float64(srcH)
	scale := scaleW
	if scaleH < scaleW {
		scale = scaleH
	}
	outW = int(float64(srcW)*scale/2) * 2
	outH = int(float64(srcH)*scale/2) * 2
	if outW <= 0 {
		outW = 2
	}
	if outH <= 0 {
		outH = 2
	}

	// Skip if area reduction is less than 25%
	srcArea := srcW * srcH
	outArea := outW * outH
	if float64(outArea) >= float64(srcArea)*0.75 {
		return true, 0, 0
	}

	return false, outW, outH
}

// readSourceResolution reads the H264 video resolution from an fMP4 file
// by parsing the init segment and decoding the SPS from the H264 codec config.
func readSourceResolution(path string) (width, height int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	var init fmp4.Init
	if err := init.Unmarshal(f); err != nil {
		return 0, 0, fmt.Errorf("unmarshal init: %w", err)
	}

	for _, track := range init.Tracks {
		h264Codec, ok := track.Codec.(*codecs.H264)
		if !ok {
			continue
		}
		var sps h264.SPS
		if err := sps.Unmarshal(h264Codec.SPS); err != nil {
			return 0, 0, fmt.Errorf("parse SPS: %w", err)
		}
		return sps.Width(), sps.Height(), nil
	}

	return 0, 0, fmt.Errorf("no H264 video track found in init segment")
}

// TranscodeResult holds the outcome of a TranscodeSegment call.
type TranscodeResult struct {
	OriginalSize int64
	NewSize      int64
	Skipped      bool // true when resolution check determined transcoding isn't worth it
}

// TranscodeSegment re-encodes the video track of the fMP4 at path to (targetW, targetH),
// copying audio verbatim. Writes output to path+".tmp", verifies it, then atomically
// renames it over path. If the segment is already small enough (resolution check),
// returns TranscodeResult{Skipped: true} without touching the file.
func TranscodeSegment(path string, targetW, targetH int) (TranscodeResult, error) {
	srcW, srcH, err := readSourceResolution(path)
	if err != nil {
		return TranscodeResult{}, fmt.Errorf("read source resolution: %w", err)
	}

	skip, outW, outH := shouldTranscode(srcW, srcH, targetW, targetH)
	if skip {
		return TranscodeResult{Skipped: true}, nil
	}

	origInfo, err := os.Stat(path)
	if err != nil {
		return TranscodeResult{}, fmt.Errorf("stat source: %w", err)
	}
	origSize := origInfo.Size()

	tmpPath := path + ".tmp"
	if err := transcodeFile(path, tmpPath, outW, outH); err != nil {
		_ = os.Remove(tmpPath)
		return TranscodeResult{}, fmt.Errorf("transcode: %w", err)
	}

	if err := verifyFMP4(tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		return TranscodeResult{}, fmt.Errorf("verify output: %w", err)
	}

	newInfo, err := os.Stat(tmpPath)
	if err != nil {
		_ = os.Remove(tmpPath)
		return TranscodeResult{}, fmt.Errorf("stat output: %w", err)
	}
	newSize := newInfo.Size()

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return TranscodeResult{}, fmt.Errorf("rename: %w", err)
	}

	return TranscodeResult{OriginalSize: origSize, NewSize: newSize}, nil
}

func verifyFMP4(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, frags, _, err := indexFile(f)
	if err != nil {
		return fmt.Errorf("index: %w", err)
	}
	if len(frags) == 0 {
		return fmt.Errorf("output has no fragments")
	}
	return nil
}

// transcodeFile reads src fMP4, re-encodes video track at (outW, outH), copies audio verbatim, writes to dst.
func transcodeFile(src, dst string, outW, outH int) error {
	if !ensureOpenH264() {
		return fmt.Errorf("OpenH264 not available")
	}

	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open src: %w", err)
	}
	defer in.Close()

	// Parse init segment
	var srcInit fmp4.Init
	if err := srcInit.Unmarshal(in); err != nil {
		return fmt.Errorf("unmarshal init: %w", err)
	}

	var videoTrackID, audioTrackID int
	var srcH264Codec *codecs.H264
	for _, tr := range srcInit.Tracks {
		if c, ok := tr.Codec.(*codecs.H264); ok {
			videoTrackID = tr.ID
			srcH264Codec = c
		} else {
			audioTrackID = tr.ID
		}
	}
	if videoTrackID == 0 || srcH264Codec == nil {
		return fmt.Errorf("no H264 video track in source")
	}

	// Seek back to start and index moof+mdat locations
	if _, err := in.Seek(0, io.SeekStart); err != nil {
		return err
	}
	_, indexedFrags, trackTimeScales, err := indexFile(in)
	if err != nil {
		return fmt.Errorf("index: %w", err)
	}

	// Build a deduplicated list of moof+mdat block locations. indexFile emits
	// one fragment entry per traf but multi-track moofs share a single mdat.
	// We collect unique moof offsets so each moof+mdat block is parsed once.
	type moofBlock struct {
		moofOffset int64
		moofSize   int64
		mdatOffset int64
		mdatSize   int64
	}
	seen := make(map[int64]bool)
	var blocks []moofBlock
	for _, f := range indexedFrags {
		if f.mdatSize == 0 || seen[f.moofOffset] {
			continue
		}
		seen[f.moofOffset] = true
		blocks = append(blocks, moofBlock{
			moofOffset: f.moofOffset,
			moofSize:   f.moofSize,
			mdatOffset: f.mdatOffset,
			mdatSize:   f.mdatSize,
		})
	}

	// Create H264 decoder
	dec := NewH264Decoder()
	if dec == nil {
		return fmt.Errorf("failed to create H264 decoder")
	}
	defer dec.Close()

	// Create H264 encoder
	OpenH264Lock()
	var ppEnc *openh264.ISVCEncoder
	if ret := openh264.WelsCreateSVCEncoder(&ppEnc); ret != 0 || ppEnc == nil {
		OpenH264Unlock()
		return fmt.Errorf("WelsCreateSVCEncoder failed: %d", ret)
	}
	OpenH264Unlock()
	defer func() {
		OpenH264Lock()
		openh264.WelsDestroySVCEncoder(ppEnc)
		OpenH264Unlock()
	}()

	videoTS := trackTimeScales[uint32(videoTrackID)]
	if videoTS == 0 {
		videoTS = 90000
	}
	// Compute fps from the actual first video fragment's sample duration so that
	// the encoder's rate-control is accurate for non-30fps sources (e.g. 25fps).
	fps := float32(15)
	for _, f := range indexedFrags {
		if int(f.trackID) == videoTrackID && f.duration > 0 {
			fps = float32(videoTS) / float32(f.duration)
			break
		}
	}
	if fps <= 0 || fps > 60 {
		fps = 15
	}

	encParam := openh264.SEncParamBase{
		IUsageType:     openh264.CAMERA_VIDEO_REAL_TIME,
		IPicWidth:      int32(outW),
		IPicHeight:     int32(outH),
		ITargetBitrate: targetBitrate(outW, outH),
		FMaxFrameRate:  fps,
	}
	OpenH264Lock()
	if r := ppEnc.Initialize(&encParam); r != 0 {
		OpenH264Unlock()
		return fmt.Errorf("encoder Initialize failed: %d", r)
	}
	OpenH264Unlock()
	defer func() {
		OpenH264Lock()
		ppEnc.Uninitialize()
		OpenH264Unlock()
	}()

	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create dst: %w", err)
	}
	defer out.Close()

	type encodedGOP struct {
		videoSamples []*fmp4.Sample
		audioSamples []*fmp4.Sample
		videoBase    uint64
		audioBase    uint64
		seqNum       uint32
	}

	var gops []encodedGOP
	var outSPS, outPPS []byte
	var seqNum uint32 = 1

	// GC-SAFE ENCODER I/O BUFFERS — DO NOT "SIMPLIFY" TO TYPED STRUCTS.
	//
	// OpenH264 writes C-owned pointers (PBsBuf, PNalLengthInByte) into the
	// SFrameBSInfo struct, and we write Go-owned Pinner-pinned pointers
	// (PData[0..2]) into SSourcePicture. If either struct is allocated as
	// its Go type, those fields appear in the GC pointer bitmap and the
	// concurrent GC (Go 1.26+) will scan them during marking.
	//
	// For SFrameBSInfo the scanned pointers are C-owned addresses that
	// look like they might be inside the Go heap; the GC calls findObject
	// on them and crashes with "found pointer to free object" or "found
	// bad pointer in Go heap", or the process gets SIGKILLed.
	//
	// By allocating the structs as [N]byte arrays, the GC bitmap for the
	// allocation is all zeros — no words are ever scanned as pointers, no
	// matter where the memory lives (stack or heap). We cast to the typed
	// pointer only for field writes; Go's GC scans allocations by their
	// original type metadata, not by what pointers later alias them.
	//
	// Regression test: TestTranscodeSegment_Fixture in transcoder_real_test.go.
	var encSrcPicBuf [unsafe.Sizeof(openh264.SSourcePicture{})]byte
	var encInfoBuf [unsafe.Sizeof(openh264.SFrameBSInfo{})]byte

	for gopIdx, blk := range blocks {
		// Read the full moof+mdat block and unmarshal it as an fmp4.Part so that
		// per-track sample payloads are properly separated regardless of whether
		// the moof uses single-track or multi-track (video+audio) trafs.
		blockSize := (blk.mdatOffset - blk.moofOffset) + blk.mdatSize
		if _, err := in.Seek(blk.moofOffset, io.SeekStart); err != nil {
			return err
		}
		blockBuf := make([]byte, blockSize)
		if _, err := io.ReadFull(in, blockBuf); err != nil {
			return fmt.Errorf("read block %d: %w", gopIdx, err)
		}

		var parts fmp4.Parts
		if err := parts.Unmarshal(blockBuf); err != nil {
			return fmt.Errorf("unmarshal block %d: %w", gopIdx, err)
		}

		// Collect video and audio samples from this block
		var videoAVCC []byte
		var videoBase uint64
		var videoDuration uint32
		var newAudioSamples []*fmp4.Sample
		var audioBase uint64

		for _, part := range parts {
			for _, tr := range part.Tracks {
				switch tr.ID {
				case videoTrackID:
					videoBase = tr.BaseTime
					for _, s := range tr.Samples {
						videoDuration += s.Duration
						videoAVCC = append(videoAVCC, s.Payload...)
					}
				case audioTrackID:
					audioBase = tr.BaseTime
					for _, s := range tr.Samples {
						newAudioSamples = append(newAudioSamples, &fmp4.Sample{
							Duration:        s.Duration,
							Payload:         append([]byte(nil), s.Payload...),
							IsNonSyncSample: s.IsNonSyncSample,
						})
					}
				}
			}
		}

		if len(videoAVCC) == 0 {
			continue
		}

		// Convert AVCC samples to Annex B for the decoder
		annexB, err := avccToAnnexB(videoAVCC)
		if err != nil {
			continue
		}

		// Reorder NALs: SPS and PPS must precede slice data for the decoder
		// to initialise. Recordings may place SPS/PPS at the end of a GOP
		// (intended for the next GOP), so we extract them and always prepend
		// the init segment's SPS/PPS before all slice NALs.
		annexB = reorderAnnexBWithSPSPPS(annexB, srcH264Codec.SPS, srcH264Codec.PPS)

		var newVideoSamples []*fmp4.Sample
		pinner := &runtime.Pinner{}

		// Build an access unit containing only SPS, PPS, and the IDR slice.
		// P-frames are discarded — the recompressed file stores one keyframe
		// per GOP. This keeps memory bounded and avoids decoding 30+ frames
		// per GOP just to discard them.
		startCode := []byte{0, 0, 0, 1}
		var idrAU []byte
		for _, nal := range splitAnnexB(annexB) {
			if len(nal) == 0 {
				continue
			}
			nalType := nal[0] & 0x1f
			switch nalType {
			case 7, 8, 5: // SPS, PPS, IDR
				idrAU = append(idrAU, startCode...)
				idrAU = append(idrAU, nal...)
			}
		}
		if len(idrAU) == 0 {
			continue
		}

		frame := dec.Decode(idrAU)
		if frame == nil {
			frame = dec.Flush()
		}

		if frame != nil {
			scaled := scaleYCbCr(frame, outW, outH)

			// Guard against degenerate frames that would panic on &slice[0].
			if len(scaled.Y) == 0 || len(scaled.Cb) == 0 || len(scaled.Cr) == 0 {
				pinner.Unpin()
				continue
			}

			// Zero-clear the reused encoder I/O buffers before each use to ensure
			// no data from a previous iteration leaks into the new frame.
			for i := range encSrcPicBuf {
				encSrcPicBuf[i] = 0
			}
			for i := range encInfoBuf {
				encInfoBuf[i] = 0
			}
			encSrcPic := (*openh264.SSourcePicture)(unsafe.Pointer(&encSrcPicBuf[0]))
			encSrcPic.IColorFormat = openh264.VideoFormatI420
			encSrcPic.IPicWidth = int32(outW)
			encSrcPic.IPicHeight = int32(outH)
			encSrcPic.UiTimeStamp = int64(gopIdx * 1000)
			encSrcPic.IStride[0] = int32(scaled.YStride)
			encSrcPic.IStride[1] = int32(scaled.CStride)
			encSrcPic.IStride[2] = int32(scaled.CStride)
			pinner.Pin(&scaled.Y[0])
			pinner.Pin(&scaled.Cb[0])
			pinner.Pin(&scaled.Cr[0])
			encSrcPic.PData[0] = (*uint8)(unsafe.Pointer(&scaled.Y[0]))
			encSrcPic.PData[1] = (*uint8)(unsafe.Pointer(&scaled.Cb[0]))
			encSrcPic.PData[2] = (*uint8)(unsafe.Pointer(&scaled.Cr[0]))

			encInfo := (*openh264.SFrameBSInfo)(unsafe.Pointer(&encInfoBuf[0]))

			OpenH264Lock()
			encRet := ppEnc.EncodeFrame(encSrcPic, encInfo)
			// Extract the layer count and frame type while still inside the lock,
			// before the C-owned NAL buffers could be invalidated by another call.
			nLayers := int(encInfo.ILayerNum)
			encFrameType := encInfo.EFrameType
			const maxLayers = 128
			if nLayers > maxLayers {
				nLayers = maxLayers
			}
			// Capture C pointer values as uintptr (not pointer-typed — the GC
			// does not trace uintptr fields) before doing any allocation.
			var (
				layerBufPtrs   [maxLayers]uintptr
				layerLenPtrs   [maxLayers]uintptr
				layerNalCounts [maxLayers]int32
			)
			for iLayer := 0; iLayer < nLayers; iLayer++ {
				layer := &encInfo.SLayerInfo[iLayer]
				layerBufPtrs[iLayer] = uintptr(unsafe.Pointer(layer.PBsBuf))
				layerLenPtrs[iLayer] = uintptr(unsafe.Pointer(layer.PNalLengthInByte))
				layerNalCounts[iLayer] = layer.INalCount
			}
			OpenH264Unlock()

			// Unpin after the encode call; the C library no longer needs the
			// Go-managed plane data. Do this before any further allocations to
			// release the pinning obligation promptly.
			pinner.Unpin()
			pinner = &runtime.Pinner{}

			// Copy NAL bytes from the C-owned buffers into Go-owned slices.
			// The C buffers remain valid until the next EncodeFrame or
			// WelsDestroySVCEncoder call (serialized by OpenH264Lock above).
			//
			// We access C memory via uintptr pointer arithmetic rather than
			// unsafe.Slice to avoid creating any slice header whose Data field
			// holds a C pointer (even transiently on the Go heap).
			var nalBytes []byte
			if encRet == openh264.CmResultSuccess && encFrameType != openh264.VideoFrameTypeSkip {
				for iLayer := 0; iLayer < nLayers; iLayer++ {
					nalCount := layerNalCounts[iLayer]
					nalLenPtr := layerLenPtrs[iLayer]
					bufPtr := layerBufPtrs[iLayer]
					if nalCount <= 0 || nalLenPtr == 0 || bufPtr == 0 {
						continue
					}
					// Sum NAL unit lengths by stepping through the C-owned int32 array
					// one element at a time. No slice header with a C data pointer.
					var layerSize int32
					for i := int32(0); i < nalCount; i++ {
						l := *(*int32)(unsafe.Pointer(nalLenPtr + uintptr(i)*4)) //nolint:govet // C pointer stored as uintptr; GC cannot move C-owned memory
						layerSize += l
					}
					if layerSize <= 0 {
						continue
					}
					// Copy NAL bytes from the C-owned buffer into a Go-owned slice.
					layerCopy := make([]byte, layerSize)
					for i := int32(0); i < layerSize; i++ {
						layerCopy[i] = *(*uint8)(unsafe.Pointer(bufPtr + uintptr(i))) //nolint:govet // C pointer stored as uintptr; GC cannot move C-owned memory
					}
					nalBytes = append(nalBytes, layerCopy...)
				}
			}

			if encRet == openh264.CmResultSuccess && encFrameType != openh264.VideoFrameTypeSkip {

				if outSPS == nil {
					outSPS, outPPS = extractSPSPPS(nalBytes)
				}

				avccPayloadOut, err := annexBToAVCC(nalBytes)
				if err == nil && len(avccPayloadOut) > 0 {
					isNonSync := encFrameType != openh264.VideoFrameTypeIDR
					newVideoSamples = append(newVideoSamples, &fmp4.Sample{
						Duration:        videoDuration,
						Payload:         avccPayloadOut,
						IsNonSyncSample: isNonSync,
					})
				}
			}
		}
		pinner.Unpin()

		if len(newVideoSamples) > 0 {
			gops = append(gops, encodedGOP{
				videoSamples: newVideoSamples,
				audioSamples: newAudioSamples,
				videoBase:    videoBase,
				audioBase:    audioBase,
				seqNum:       seqNum,
			})
			seqNum++
		}
	}

	if len(gops) == 0 || outSPS == nil {
		return fmt.Errorf("no frames encoded successfully")
	}

	outInit := fmp4.Init{
		Tracks: []*fmp4.InitTrack{
			{ID: videoTrackID, TimeScale: videoTS, Codec: &codecs.H264{SPS: outSPS, PPS: outPPS}},
		},
	}
	if audioTrackID != 0 {
		for _, tr := range srcInit.Tracks {
			if tr.ID == audioTrackID {
				outInit.Tracks = append(outInit.Tracks, &fmp4.InitTrack{
					ID:        tr.ID,
					TimeScale: tr.TimeScale,
					Codec:     tr.Codec,
				})
				break
			}
		}
	}

	if err := outInit.Marshal(out); err != nil {
		return fmt.Errorf("write init: %w", err)
	}

	for _, gop := range gops {
		// Validate samples before marshalling — nil payloads panic the fmp4 library
		validVideo := make([]*fmp4.Sample, 0, len(gop.videoSamples))
		for _, s := range gop.videoSamples {
			if len(s.Payload) > 0 {
				validVideo = append(validVideo, s)
			}
		}
		if len(validVideo) == 0 {
			continue
		}

		tracks := []*fmp4.PartTrack{
			{ID: videoTrackID, BaseTime: gop.videoBase, Samples: validVideo},
		}
		if audioTrackID != 0 && len(gop.audioSamples) > 0 {
			validAudio := make([]*fmp4.Sample, 0, len(gop.audioSamples))
			for _, s := range gop.audioSamples {
				if len(s.Payload) > 0 {
					validAudio = append(validAudio, s)
				}
			}
			if len(validAudio) > 0 {
				tracks = append(tracks, &fmp4.PartTrack{
					ID:       audioTrackID,
					BaseTime: gop.audioBase,
					Samples:  validAudio,
				})
			}
		}
		part := fmp4.Part{SequenceNumber: gop.seqNum, Tracks: tracks}
		if err := part.Marshal(out); err != nil {
			return fmt.Errorf("write part %d: %w", gop.seqNum, err)
		}
	}

	return nil
}

func targetBitrate(w, h int) int32 {
	pixels := w * h
	switch {
	case pixels >= 1920*1080:
		return 2_000_000
	case pixels >= 1280*720:
		return 1_000_000
	case pixels >= 854*480:
		return 500_000
	default:
		return 300_000
	}
}

// reorderAnnexBWithSPSPPS ensures SPS and PPS precede all slice NALs.
// It strips any in-stream SPS/PPS and prepends the canonical ones from
// the init segment. This handles recordings where SPS/PPS appear after
// slice data (at the end of a GOP, intended for the next GOP).
func reorderAnnexBWithSPSPPS(annexB, sps, pps []byte) []byte {
	startCode := []byte{0, 0, 0, 1}
	var out []byte
	out = append(out, startCode...)
	out = append(out, sps...)
	out = append(out, startCode...)
	out = append(out, pps...)
	for _, nal := range splitAnnexB(annexB) {
		if len(nal) == 0 {
			continue
		}
		nalType := nal[0] & 0x1f
		if nalType == 7 || nalType == 8 {
			continue // skip in-stream SPS/PPS, we already prepended
		}
		out = append(out, startCode...)
		out = append(out, nal...)
	}
	return out
}

func avccToAnnexB(avcc []byte) ([]byte, error) {
	var out []byte
	pos := 0
	for pos+4 <= len(avcc) {
		nalLen := int(avcc[pos])<<24 | int(avcc[pos+1])<<16 | int(avcc[pos+2])<<8 | int(avcc[pos+3])
		pos += 4
		if pos+nalLen > len(avcc) {
			return nil, fmt.Errorf("AVCC NAL length %d exceeds buffer", nalLen)
		}
		out = append(out, 0, 0, 0, 1)
		out = append(out, avcc[pos:pos+nalLen]...)
		pos += nalLen
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no NAL units in AVCC payload")
	}
	return out, nil
}

func annexBToAVCC(annexB []byte) ([]byte, error) {
	nals := splitAnnexB(annexB)
	var out []byte
	for _, nal := range nals {
		if len(nal) == 0 {
			continue
		}
		nalType := nal[0] & 0x1f
		if nalType == 7 || nalType == 8 {
			continue // SPS/PPS go in init, not samples
		}
		l := len(nal)
		out = append(out, byte(l>>24), byte(l>>16), byte(l>>8), byte(l))
		out = append(out, nal...)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no non-SPS/PPS NAL units in Annex B data")
	}
	return out, nil
}

func extractSPSPPS(annexB []byte) (sps, pps []byte) {
	nals := splitAnnexB(annexB)
	for _, nal := range nals {
		if len(nal) == 0 {
			continue
		}
		switch nal[0] & 0x1f {
		case 7:
			sps = append([]byte(nil), nal...)
		case 8:
			pps = append([]byte(nil), nal...)
		}
	}
	return
}

func splitAnnexB(data []byte) [][]byte {
	var nals [][]byte
	i := 0
	for i < len(data) {
		startLen := 0
		if i+4 <= len(data) && data[i] == 0 && data[i+1] == 0 && data[i+2] == 0 && data[i+3] == 1 {
			startLen = 4
		} else if i+3 <= len(data) && data[i] == 0 && data[i+1] == 0 && data[i+2] == 1 {
			startLen = 3
		}
		if startLen > 0 {
			nals = append(nals, nil)
			i += startLen
			continue
		}
		if len(nals) > 0 {
			nals[len(nals)-1] = append(nals[len(nals)-1], data[i])
		}
		i++
	}
	return nals
}
