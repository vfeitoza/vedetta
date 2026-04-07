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

	// Seek back to start and index all fragments
	if _, err := in.Seek(0, io.SeekStart); err != nil {
		return err
	}
	_, fragments, trackTimeScales, err := indexFile(in)
	if err != nil {
		return fmt.Errorf("index: %w", err)
	}

	// Create H264 decoder
	dec := NewH264Decoder()
	if dec == nil {
		return fmt.Errorf("failed to create H264 decoder")
	}
	defer dec.Close()

	// Create H264 encoder
	var ppEnc *openh264.ISVCEncoder
	if ret := openh264.WelsCreateSVCEncoder(&ppEnc); ret != 0 || ppEnc == nil {
		return fmt.Errorf("WelsCreateSVCEncoder failed: %d", ret)
	}
	defer openh264.WelsDestroySVCEncoder(ppEnc)

	videoTS := trackTimeScales[uint32(videoTrackID)]
	if videoTS == 0 {
		videoTS = 90000
	}
	fps := float32(videoTS) / float32(3000)
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
	if r := ppEnc.Initialize(&encParam); r != 0 {
		return fmt.Errorf("encoder Initialize failed: %d", r)
	}
	defer ppEnc.Uninitialize()

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

	videoFrags := make([]fragment, 0)
	audioFrags := make([]fragment, 0)
	for _, frag := range fragments {
		if int(frag.trackID) == videoTrackID {
			videoFrags = append(videoFrags, frag)
		} else if int(frag.trackID) == audioTrackID {
			audioFrags = append(audioFrags, frag)
		}
	}

	audioIdx := 0
	for gopIdx, vfrag := range videoFrags {
		rawMdat := make([]byte, vfrag.mdatSize-8)
		if _, err := in.Seek(vfrag.mdatOffset+8, io.SeekStart); err != nil {
			return err
		}
		if _, err := io.ReadFull(in, rawMdat); err != nil {
			return fmt.Errorf("read video mdat: %w", err)
		}

		annexB, err := avccToAnnexB(rawMdat)
		if err != nil {
			return fmt.Errorf("avcc to annexb: %w", err)
		}

		var newVideoSamples []*fmp4.Sample
		pinner := &runtime.Pinner{}

		frame := dec.Decode(annexB)
		if frame == nil {
			frame = dec.Flush()
		}

		if frame != nil {
			scaled := scaleYCbCr(frame, outW, outH)

			encSrcPic := openh264.SSourcePicture{
				IColorFormat: openh264.VideoFormatI420,
				IPicWidth:    int32(outW),
				IPicHeight:   int32(outH),
				UiTimeStamp:  int64(gopIdx * 1000),
			}
			encSrcPic.IStride[0] = int32(scaled.YStride)
			encSrcPic.IStride[1] = int32(scaled.CStride)
			encSrcPic.IStride[2] = int32(scaled.CStride)
			pinner.Pin(&scaled.Y[0])
			pinner.Pin(&scaled.Cb[0])
			pinner.Pin(&scaled.Cr[0])
			encSrcPic.PData[0] = (*uint8)(unsafe.Pointer(&scaled.Y[0]))
			encSrcPic.PData[1] = (*uint8)(unsafe.Pointer(&scaled.Cb[0]))
			encSrcPic.PData[2] = (*uint8)(unsafe.Pointer(&scaled.Cr[0]))

			encInfo := openh264.SFrameBSInfo{}
			if ret := ppEnc.EncodeFrame(&encSrcPic, &encInfo); ret == openh264.CmResultSuccess &&
				encInfo.EFrameType != openh264.VideoFrameTypeSkip {

				var nalBytes []byte
				for iLayer := 0; iLayer < int(encInfo.ILayerNum); iLayer++ {
					layer := &encInfo.SLayerInfo[iLayer]
					var layerSize int32
					nallens := unsafe.Slice(layer.PNalLengthInByte, layer.INalCount)
					for _, l := range nallens {
						layerSize += l
					}
					nalBytes = append(nalBytes, unsafe.Slice(layer.PBsBuf, layerSize)...)
				}

				if outSPS == nil {
					outSPS, outPPS = extractSPSPPS(nalBytes)
				}

				avccPayloadOut, err := annexBToAVCC(nalBytes)
				if err == nil && len(avccPayloadOut) > 0 {
					isNonSync := encInfo.EFrameType != openh264.VideoFrameTypeIDR
					newVideoSamples = append(newVideoSamples, &fmp4.Sample{
						Duration:        vfrag.duration,
						Payload:         avccPayloadOut,
						IsNonSyncSample: isNonSync,
					})
				}
			}
		}
		pinner.Unpin()

		var newAudioSamples []*fmp4.Sample
		var audioBase uint64
		for audioIdx < len(audioFrags) {
			afrag := audioFrags[audioIdx]
			audioBase = afrag.decodeTime
			rawAudio := make([]byte, afrag.mdatSize-8)
			if _, seekErr := in.Seek(afrag.mdatOffset+8, io.SeekStart); seekErr != nil {
				break
			}
			if _, readErr := io.ReadFull(in, rawAudio); readErr != nil {
				break
			}
			newAudioSamples = append(newAudioSamples, &fmp4.Sample{
				Duration:        afrag.duration,
				Payload:         append([]byte(nil), rawAudio...),
				IsNonSyncSample: false,
			})
			audioIdx++
			if afrag.decodeTime+uint64(afrag.duration) >= vfrag.decodeTime+uint64(vfrag.duration) {
				break
			}
		}

		if len(newVideoSamples) > 0 {
			gops = append(gops, encodedGOP{
				videoSamples: newVideoSamples,
				audioSamples: newAudioSamples,
				videoBase:    vfrag.decodeTime,
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
		tracks := []*fmp4.PartTrack{
			{ID: videoTrackID, BaseTime: gop.videoBase, Samples: gop.videoSamples},
		}
		if audioTrackID != 0 && len(gop.audioSamples) > 0 {
			tracks = append(tracks, &fmp4.PartTrack{
				ID:       audioTrackID,
				BaseTime: gop.audioBase,
				Samples:  gop.audioSamples,
			})
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
