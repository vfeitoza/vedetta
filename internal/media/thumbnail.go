package media

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"os"
	"sync"
	"time"
)

// segmentInit caches SPS/PPS and timescale per segment file.
type segmentInit struct {
	sps, pps  []byte
	timeScale uint32
}

var (
	initCacheMu sync.Mutex
	initCache   = make(map[string]*segmentInit) // key: segment path
)

func getCachedInit(segmentPath string, f *os.File) (*segmentInit, error) {
	initCacheMu.Lock()
	if cached, ok := initCache[segmentPath]; ok {
		initCacheMu.Unlock()
		return cached, nil
	}
	initCacheMu.Unlock()

	sps, pps, err := readAvcC(f)
	if err != nil {
		return nil, fmt.Errorf("read avcC: %w", err)
	}
	if sps == nil || pps == nil {
		return nil, fmt.Errorf("no SPS/PPS found in init segment")
	}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	ts := readTimeScaleFast(f)
	if ts == 0 {
		ts = 90000
	}

	init := &segmentInit{sps: sps, pps: pps, timeScale: ts}

	initCacheMu.Lock()
	// Evict old entries if cache grows too large
	if len(initCache) > 200 {
		for k := range initCache {
			delete(initCache, k)
			break
		}
	}
	initCache[segmentPath] = init
	initCacheMu.Unlock()

	return init, nil
}

// thumbCache is an LRU cache for decoded JPEG thumbnails.
// Key: "segmentPath:keyframeOffset"
type thumbEntry struct {
	key  string
	data []byte
}

var (
	thumbCacheMu sync.Mutex
	thumbMap     = make(map[string]*thumbEntry)
	thumbOrder   []string // oldest first
)

const maxThumbCache = 200

func getThumbCache(key string) ([]byte, bool) {
	thumbCacheMu.Lock()
	defer thumbCacheMu.Unlock()
	if e, ok := thumbMap[key]; ok {
		return e.data, true
	}
	return nil, false
}

func putThumbCache(key string, data []byte) {
	thumbCacheMu.Lock()
	defer thumbCacheMu.Unlock()
	if _, ok := thumbMap[key]; ok {
		return // already cached
	}
	// Evict oldest if at capacity
	for len(thumbMap) >= maxThumbCache && len(thumbOrder) > 0 {
		oldest := thumbOrder[0]
		thumbOrder = thumbOrder[1:]
		delete(thumbMap, oldest)
	}
	thumbMap[key] = &thumbEntry{key: key, data: data}
	thumbOrder = append(thumbOrder, key)
}

// ExtractThumbnail reads an fMP4 segment file and extracts a JPEG thumbnail
// for the frame closest to the given offset from the segment start.
// Uses proportional seeking for large files and single-pass keyframe tracking.
func ExtractThumbnail(segmentPath string, offset time.Duration) ([]byte, error) {
	f, err := os.Open(segmentPath)
	if err != nil {
		return nil, fmt.Errorf("open segment: %w", err)
	}
	defer f.Close()

	// Use cached SPS/PPS and timescale
	init, err := getCachedInit(segmentPath, f)
	if err != nil {
		return nil, err
	}

	targetTick := uint64(offset.Seconds() * float64(init.timeScale))

	// For large files, use proportional seek to skip early fragments
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	fileSize := fi.Size()

	startPos := int64(0)
	if fileSize > 10<<20 && offset > 30*time.Second {
		// Assume ~10min segments. Seek to (target - 30s) for keyframe margin.
		const segmentDuration = 600.0
		marginTime := offset.Seconds() - 30.0
		if marginTime > 0 {
			estimatedPos := int64(marginTime / segmentDuration * float64(fileSize))
			if pos := findMoofNear(f, estimatedPos, fileSize); pos >= 0 {
				startPos = pos
			}
		}
	}

	lastKFOffset, lastKFSize := scanForKeyframe(f, startPos, targetTick)

	// Fallback: if proportional seek missed the keyframe, retry from beginning
	if lastKFSize == 0 && startPos > 0 {
		lastKFOffset, lastKFSize = scanForKeyframe(f, 0, targetTick)
	}

	if lastKFSize == 0 {
		return nil, fmt.Errorf("no keyframe found near target time")
	}

	// Check thumbnail cache (many timestamps map to the same keyframe)
	cacheKey := fmt.Sprintf("%s:%d", segmentPath, lastKFOffset)
	if cached, ok := getThumbCache(cacheKey); ok {
		return cached, nil
	}

	// Read the keyframe mdat payload
	payloadOffset := lastKFOffset + 8
	payloadSize := lastKFSize - 8
	if payloadSize <= 0 {
		return nil, fmt.Errorf("empty mdat")
	}

	if _, err := f.Seek(payloadOffset, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek to keyframe mdat: %w", err)
	}
	avccData := make([]byte, payloadSize)
	if _, err := io.ReadFull(f, avccData); err != nil {
		return nil, fmt.Errorf("read keyframe mdat: %w", err)
	}

	// Decode with OpenH264
	dec := NewH264Decoder()
	if dec == nil {
		return nil, fmt.Errorf("OpenH264 unavailable")
	}
	defer dec.Close()

	startCode := []byte{0, 0, 0, 1}
	var ycbcr *image.YCbCr

	// Check if mdat contains SPS
	hasSPS := false
	pos := 0
	for pos+4 <= len(avccData) {
		nalLen := int(binary.BigEndian.Uint32(avccData[pos : pos+4]))
		pos += 4
		if nalLen == 0 || pos+nalLen > len(avccData) {
			break
		}
		if avccData[pos]&0x1F == 7 {
			hasSPS = true
			break
		}
		pos += nalLen
	}

	if !hasSPS {
		var paramStream []byte
		paramStream = append(paramStream, startCode...)
		paramStream = append(paramStream, init.sps...)
		paramStream = append(paramStream, startCode...)
		paramStream = append(paramStream, init.pps...)
		dec.Decode(paramStream)
	}

	pos = 0
	for pos+4 <= len(avccData) {
		nalLen := int(binary.BigEndian.Uint32(avccData[pos : pos+4]))
		pos += 4
		if nalLen == 0 || pos+nalLen > len(avccData) {
			break
		}
		nal := avccData[pos : pos+nalLen]
		pos += nalLen

		var nalStream []byte
		nalStream = append(nalStream, startCode...)
		nalStream = append(nalStream, nal...)

		result := dec.Decode(nalStream)
		if result != nil {
			ycbcr = result
		}
	}

	if ycbcr == nil {
		ycbcr = dec.Flush()
	}

	if ycbcr == nil {
		return nil, fmt.Errorf("decode produced no frame")
	}

	jpegData, err := encodeJPEGThumbnail(ycbcr, 320, 60)
	if err != nil {
		return nil, err
	}

	putThumbCache(cacheKey, jpegData)
	return jpegData, nil
}

// scanForKeyframe scans from startPos looking for the last keyframe at or before targetTick.
func scanForKeyframe(f *os.File, startPos int64, targetTick uint64) (kfOffset, kfSize int64) {
	if _, err := f.Seek(startPos, io.SeekStart); err != nil {
		return 0, 0
	}

	for {
		boxOffset, boxSize, boxType, err := readBoxHeader(f)
		if err != nil {
			break
		}

		if boxType == "moof" {
			moofEnd := boxOffset + boxSize
			decodeTime, trackID, err := parseMoofTiming(f, moofEnd)
			if err != nil || trackID > 1 {
				if _, err := f.Seek(moofEnd, io.SeekStart); err != nil {
					break
				}
				continue
			}

			if _, err := f.Seek(moofEnd, io.SeekStart); err != nil {
				break
			}

			mdatOffset, mdatSize, mdatType, err := readBoxHeader(f)
			if err != nil || mdatType != "mdat" {
				continue
			}

			// Peek at first NAL to check for keyframe
			if mdatSize > 13 {
				var header [5]byte
				if _, err := io.ReadFull(f, header[:]); err == nil {
					nalType := header[4] & 0x1F
					if nalType == 5 || nalType == 7 {
						kfOffset = mdatOffset
						kfSize = mdatSize
					}
				}
			}

			if decodeTime > targetTick {
				break
			}

			if _, err := f.Seek(mdatOffset+mdatSize, io.SeekStart); err != nil {
				break
			}
		} else {
			if _, err := f.Seek(boxOffset+boxSize, io.SeekStart); err != nil {
				break
			}
		}
	}

	return kfOffset, kfSize
}

// findMoofNear scans for a moof box boundary near the given file position.
// Returns the byte offset of the moof box, or -1 if not found.
func findMoofNear(f *os.File, pos, fileSize int64) int64 {
	if pos < 0 {
		pos = 0
	}
	if pos >= fileSize {
		return -1
	}

	if _, err := f.Seek(pos, io.SeekStart); err != nil {
		return -1
	}

	buf := make([]byte, 1<<20) // 1MB scan window
	n, _ := f.Read(buf)
	if n < 8 {
		return -1
	}

	moofTag := []byte("moof")
	for i := 4; i+4 <= n; i++ {
		if !bytes.Equal(buf[i:i+4], moofTag) {
			continue
		}
		// Validate: preceding 4 bytes should be a reasonable box size
		boxSize := binary.BigEndian.Uint32(buf[i-4 : i])
		if boxSize > 16 && boxSize < 200000 {
			return pos + int64(i-4)
		}
	}
	return -1
}

// readBoxHeader reads an MP4 box header and returns offset, total size, type, error.
func readBoxHeader(r io.ReadSeeker) (offset, size int64, boxType string, err error) {
	offset, err = r.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, 0, "", err
	}

	var header [8]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return 0, 0, "", err
	}

	size = int64(binary.BigEndian.Uint32(header[:4]))
	boxType = string(header[4:8])

	if size == 1 {
		var extSize [8]byte
		if _, err := io.ReadFull(r, extSize[:]); err != nil {
			return 0, 0, "", err
		}
		size = int64(binary.BigEndian.Uint64(extSize[:]))
	}

	return offset, size, boxType, nil
}

// parseMoofTiming reads decode time and track ID from a moof box.
// Reader should be positioned right after the moof box header (at first child).
func parseMoofTiming(r io.ReadSeeker, moofEnd int64) (decodeTime uint64, trackID uint32, err error) {
	for {
		pos, _ := r.Seek(0, io.SeekCurrent)
		if pos >= moofEnd {
			break
		}

		childOffset, childSize, childType, err := readBoxHeader(r)
		if err != nil {
			return 0, 0, err
		}

		if childType == "traf" {
			// Parse traf children
			trafEnd := childOffset + childSize
			for {
				tpos, _ := r.Seek(0, io.SeekCurrent)
				if tpos >= trafEnd {
					break
				}
				gcOffset, gcSize, gcType, err := readBoxHeader(r)
				if err != nil {
					break
				}
				switch gcType {
				case "tfhd":
					// version(1) + flags(3) + trackID(4)
					var buf [8]byte
					if _, err := io.ReadFull(r, buf[:]); err == nil {
						trackID = binary.BigEndian.Uint32(buf[4:8])
					}
				case "tfdt":
					var vf [4]byte
					if _, err := io.ReadFull(r, vf[:]); err == nil {
						version := vf[0]
						if version == 0 {
							var buf [4]byte
							if _, err := io.ReadFull(r, buf[:]); err == nil {
								decodeTime = uint64(binary.BigEndian.Uint32(buf[:]))
							}
						} else {
							var buf [8]byte
							if _, err := io.ReadFull(r, buf[:]); err == nil {
								decodeTime = binary.BigEndian.Uint64(buf[:])
							}
						}
					}
				}
				if _, err := r.Seek(gcOffset+gcSize, io.SeekStart); err != nil {
					break
				}
			}
			return decodeTime, trackID, nil
		}

		if _, err := r.Seek(childOffset+childSize, io.SeekStart); err != nil {
			return 0, 0, err
		}
	}
	return 0, 0, fmt.Errorf("no traf found")
}

// readTimeScaleFast extracts the video track timescale from the mdhd box
// by scanning the first 64KB of the file.
func readTimeScaleFast(r io.ReadSeeker) uint32 {
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return 0
	}
	buf := make([]byte, 64*1024)
	n, _ := io.ReadFull(r, buf)
	buf = buf[:n]

	mdhdTag := []byte("mdhd")
	for i := 4; i+4 <= len(buf); i++ {
		if !bytes.Equal(buf[i:i+4], mdhdTag) {
			continue
		}
		// mdhd box: [4 size][4 "mdhd"][1 version][3 flags][...]
		dataStart := i + 4 // after "mdhd"
		if dataStart+4 > len(buf) {
			continue
		}
		version := buf[dataStart]
		if version == 0 {
			// v0: [4 creation][4 modification][4 timescale][4 duration]
			tsOffset := dataStart + 4 + 8 // skip version+flags(4) + creation+modification(8)
			if tsOffset+4 > len(buf) {
				continue
			}
			return binary.BigEndian.Uint32(buf[tsOffset : tsOffset+4])
		}
		// v1: [8 creation][8 modification][4 timescale][8 duration]
		tsOffset := dataStart + 4 + 16 // skip version+flags(4) + creation+modification(16)
		if tsOffset+4 > len(buf) {
			continue
		}
		return binary.BigEndian.Uint32(buf[tsOffset : tsOffset+4])
	}
	return 0
}

// readAvcC extracts SPS and PPS from the fMP4 init segment's avcC box.
// Uses a fast byte scan to find the 'avcC' box marker in the moov segment.
func readAvcC(r io.ReadSeeker) (sps, pps []byte, err error) {
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, nil, err
	}

	// Read the first 64KB which contains the moov/init segment
	buf := make([]byte, 64*1024)
	n, _ := io.ReadFull(r, buf)
	buf = buf[:n]

	// Search for "avcC" box marker
	avcCTag := []byte("avcC")
	for i := 4; i+4 <= len(buf); i++ {
		if !bytes.Equal(buf[i:i+4], avcCTag) {
			continue
		}

		// Found avcC box at i-4 (size field is at i-4)
		boxStart := i - 4
		if boxStart < 0 {
			continue
		}
		boxSize := int(binary.BigEndian.Uint32(buf[boxStart : boxStart+4]))
		dataStart := i + 4 // after "avcC" tag
		if dataStart+8 > len(buf) || boxStart+boxSize > len(buf) {
			continue
		}

		// Parse AVCDecoderConfigurationRecord
		data := buf[dataStart:]
		if len(data) < 7 {
			continue
		}
		// data[0] = configurationVersion
		// data[1] = AVCProfileIndication
		// data[2] = profile_compatibility
		// data[3] = AVCLevelIndication
		// data[4] = lengthSizeMinusOne (lower 2 bits)
		// data[5] = numOfSequenceParameterSets (lower 5 bits)
		numSPS := int(data[5] & 0x1F)
		pos := 6
		for j := 0; j < numSPS && pos+2 <= len(data); j++ {
			spsLen := int(binary.BigEndian.Uint16(data[pos : pos+2]))
			pos += 2
			if pos+spsLen > len(data) {
				break
			}
			if sps == nil {
				sps = make([]byte, spsLen)
				copy(sps, data[pos:pos+spsLen])
			}
			pos += spsLen
		}
		if pos >= len(data) {
			continue
		}
		numPPS := int(data[pos])
		pos++
		for j := 0; j < numPPS && pos+2 <= len(data); j++ {
			ppsLen := int(binary.BigEndian.Uint16(data[pos : pos+2]))
			pos += 2
			if pos+ppsLen > len(data) {
				break
			}
			if pps == nil {
				pps = make([]byte, ppsLen)
				copy(pps, data[pos:pos+ppsLen])
			}
			pos += ppsLen
		}

		if sps != nil && pps != nil {
			return sps, pps, nil
		}
	}

	return nil, nil, fmt.Errorf("avcC box not found")
}

// encodeJPEGThumbnail scales a YCbCr image to the target width and encodes as JPEG.
func encodeJPEGThumbnail(img *image.YCbCr, targetWidth, quality int) ([]byte, error) {
	srcW := img.Rect.Dx()
	srcH := img.Rect.Dy()
	if srcW <= 0 || srcH <= 0 {
		return nil, fmt.Errorf("invalid image dimensions: %dx%d", srcW, srcH)
	}

	targetH := targetWidth * srcH / srcW
	if targetH <= 0 {
		targetH = 1
	}

	rgba := image.NewRGBA(image.Rect(0, 0, targetWidth, targetH))
	for dy := 0; dy < targetH; dy++ {
		sy := dy * srcH / targetH
		for dx := 0; dx < targetWidth; dx++ {
			sx := dx * srcW / targetWidth

			yi := sy*img.YStride + sx
			ci := (sy/2)*img.CStride + (sx / 2)

			yy := int(img.Y[yi])
			cbb := int(img.Cb[ci]) - 128
			crr := int(img.Cr[ci]) - 128

			r := yy + ((91881*crr + 32768) >> 16)
			g := yy - ((22554*cbb + 46802*crr + 32768) >> 16)
			b := yy + ((116130*cbb + 32768) >> 16)

			if r < 0 {
				r = 0
			} else if r > 255 {
				r = 255
			}
			if g < 0 {
				g = 0
			} else if g > 255 {
				g = 255
			}
			if b < 0 {
				b = 0
			} else if b > 255 {
				b = 255
			}

			off := dy*rgba.Stride + dx*4
			rgba.Pix[off] = byte(r)
			rgba.Pix[off+1] = byte(g)
			rgba.Pix[off+2] = byte(b)
			rgba.Pix[off+3] = 255
		}
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, rgba, &jpeg.Options{Quality: quality}); err != nil {
		return nil, fmt.Errorf("encode JPEG: %w", err)
	}
	return buf.Bytes(), nil
}
