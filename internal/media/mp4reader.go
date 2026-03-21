package media

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"time"

	gomp4 "github.com/abema/go-mp4"
)

// ProbeDuration reads the duration of an MP4 file.
// For standard MP4: reads moov/mvhd. For fMP4: computes from fragments.
func ProbeDuration(path string) (time.Duration, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	// Try moov-based duration first (standard MP4)
	dur, err := probeMoovDuration(f)
	if err == nil && dur > 0 {
		return dur, nil
	}

	// For fMP4 (moov duration is 0 or no moov), compute from fragments
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	return probeFMP4Duration(f)
}

func probeMoovDuration(r io.ReadSeeker) (time.Duration, error) {
	for {
		var boxHeader [8]byte
		if _, err := io.ReadFull(r, boxHeader[:]); err != nil {
			return 0, fmt.Errorf("read box header: %w", err)
		}

		size := int64(binary.BigEndian.Uint32(boxHeader[:4]))
		boxType := string(boxHeader[4:8])

		if size == 1 {
			var extSize [8]byte
			if _, err := io.ReadFull(r, extSize[:]); err != nil {
				return 0, fmt.Errorf("read extended size: %w", err)
			}
			size = int64(binary.BigEndian.Uint64(extSize[:]))
			size -= 16
		} else if size == 0 {
			return 0, fmt.Errorf("unsupported box size 0")
		} else {
			size -= 8
		}

		if boxType == "moov" {
			return findMvhdDuration(r, size)
		}

		if _, err := r.Seek(size, io.SeekCurrent); err != nil {
			return 0, fmt.Errorf("skip box %s: %w", boxType, err)
		}
	}
}

func findMvhdDuration(r io.ReadSeeker, moovSize int64) (time.Duration, error) {
	end, _ := r.Seek(0, io.SeekCurrent)
	end += moovSize

	for {
		pos, _ := r.Seek(0, io.SeekCurrent)
		if pos >= end {
			break
		}

		var boxHeader [8]byte
		if _, err := io.ReadFull(r, boxHeader[:]); err != nil {
			return 0, fmt.Errorf("read box header in moov: %w", err)
		}

		size := int64(binary.BigEndian.Uint32(boxHeader[:4]))
		boxType := string(boxHeader[4:8])

		if size == 1 {
			var extSize [8]byte
			if _, err := io.ReadFull(r, extSize[:]); err != nil {
				return 0, err
			}
			size = int64(binary.BigEndian.Uint64(extSize[:]))
			size -= 16
		} else {
			size -= 8
		}

		if boxType == "mvhd" {
			return parseMvhd(r)
		}

		if _, err := r.Seek(size, io.SeekCurrent); err != nil {
			return 0, err
		}
	}

	return 0, fmt.Errorf("mvhd box not found")
}

func parseMvhd(r io.Reader) (time.Duration, error) {
	var version [1]byte
	if _, err := io.ReadFull(r, version[:]); err != nil {
		return 0, err
	}

	var flags [3]byte
	if _, err := io.ReadFull(r, flags[:]); err != nil {
		return 0, err
	}

	if version[0] == 0 {
		var buf [16]byte
		if _, err := io.ReadFull(r, buf[:]); err != nil {
			return 0, err
		}
		timescale := binary.BigEndian.Uint32(buf[8:12])
		duration := binary.BigEndian.Uint32(buf[12:16])
		if timescale == 0 {
			return 0, nil
		}
		return time.Duration(float64(duration) / float64(timescale) * float64(time.Second)), nil
	}

	var buf [28]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return 0, err
	}
	timescale := binary.BigEndian.Uint32(buf[16:20])
	duration := binary.BigEndian.Uint64(buf[20:28])
	if timescale == 0 {
		return 0, nil
	}
	return time.Duration(float64(duration) / float64(timescale) * float64(time.Second)), nil
}

// probeFMP4Duration computes duration from fMP4 fragments.
func probeFMP4Duration(r io.ReadSeeker) (time.Duration, error) {
	var maxDecodeTime uint64
	var lastDuration uint32
	var timeScale uint32

	_, err := gomp4.ReadBoxStructure(r, func(h *gomp4.ReadHandle) (interface{}, error) {
		switch h.BoxInfo.Type {
		case gomp4.BoxTypeMoov():
			return h.Expand()
		case gomp4.BoxTypeTrak():
			return h.Expand()
		case gomp4.BoxTypeMdia():
			return h.Expand()
		case gomp4.BoxTypeMdhd():
			box, _, err := h.ReadPayload()
			if err != nil {
				return nil, err
			}
			mdhd := box.(*gomp4.Mdhd)
			if timeScale == 0 {
				timeScale = mdhd.Timescale
			}
			return nil, nil
		case gomp4.BoxTypeMoof():
			return h.Expand()
		case gomp4.BoxTypeTraf():
			return h.Expand()
		case gomp4.BoxTypeTfdt():
			box, _, err := h.ReadPayload()
			if err != nil {
				return nil, err
			}
			tfdt := box.(*gomp4.Tfdt)
			decodeTime := tfdt.GetBaseMediaDecodeTime()
			if decodeTime >= maxDecodeTime {
				maxDecodeTime = decodeTime
			}
			return nil, nil
		case gomp4.BoxTypeTrun():
			box, _, err := h.ReadPayload()
			if err != nil {
				return nil, err
			}
			trun := box.(*gomp4.Trun)
			var totalDur uint32
			for _, e := range trun.Entries {
				totalDur += e.SampleDuration
			}
			lastDuration = totalDur
			return nil, nil
		}
		return nil, nil
	})
	if err != nil {
		return 0, err
	}

	if timeScale == 0 {
		timeScale = 90000
	}

	totalTicks := maxDecodeTime + uint64(lastDuration)
	return time.Duration(float64(totalTicks) / float64(timeScale) * float64(time.Second)), nil
}

// fragment represents a moof+mdat pair with timing metadata.
type fragment struct {
	moofOffset int64
	moofSize   int64
	mdatOffset int64
	mdatSize   int64
	decodeTime uint64
	duration   uint32
	trackID    uint32
}

// boxLoc stores the position and size of a top-level box.
type boxLoc struct {
	offset int64
	size   int64
}

// TrimMP4 copies fragments from an fMP4 file that fall within [start, start+duration].
func TrimMP4(inputPath, outputPath string, start, duration time.Duration) error {
	in, err := os.Open(inputPath)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer out.Close()

	timeScale, _ := readTimeScale(in)
	if timeScale == 0 {
		timeScale = 90000
	}
	if _, err := in.Seek(0, io.SeekStart); err != nil {
		return err
	}

	startTick := uint64(start.Seconds() * float64(timeScale))
	endTick := startTick + uint64(duration.Seconds()*float64(timeScale))

	// Index all box locations
	initBoxes, fragments, err := indexFile(in)
	if err != nil {
		return fmt.Errorf("index file: %w", err)
	}

	// Copy init segment boxes
	for _, loc := range initBoxes {
		if _, err := in.Seek(loc.offset, io.SeekStart); err != nil {
			return err
		}
		if _, err := io.CopyN(out, in, loc.size); err != nil {
			return err
		}
	}

	// Copy matching fragments, adjusting timestamps
	var newSeqNum uint32 = 1
	var newBaseTime uint64
	for _, frag := range fragments {
		fragEnd := frag.decodeTime + uint64(frag.duration)
		if frag.decodeTime >= endTick || fragEnd <= startTick {
			continue
		}

		if err := copyFragmentAdjusted(in, out, frag, newSeqNum, newBaseTime); err != nil {
			return fmt.Errorf("copy fragment: %w", err)
		}
		newSeqNum++
		newBaseTime += uint64(frag.duration)
	}

	return nil
}

// TrimMP4ToWriter writes a trimmed fMP4 starting at the given offset to w.
// This is used for HTTP playback so the browser receives video starting at
// the requested position without needing client-side seeking.
func TrimMP4ToWriter(inputPath string, w io.Writer, start time.Duration) error {
	in, err := os.Open(inputPath)
	if err != nil {
		return err
	}
	defer in.Close()

	timeScale, _ := readTimeScale(in)
	if timeScale == 0 {
		timeScale = 90000
	}
	if _, err := in.Seek(0, io.SeekStart); err != nil {
		return err
	}

	startTick := uint64(start.Seconds() * float64(timeScale))

	initBoxes, fragments, err := indexFile(in)
	if err != nil {
		return fmt.Errorf("index file: %w", err)
	}

	// Copy init segment boxes (ftyp, moov)
	for _, loc := range initBoxes {
		if _, err := in.Seek(loc.offset, io.SeekStart); err != nil {
			return err
		}
		if _, err := io.CopyN(w, in, loc.size); err != nil {
			return err
		}
	}

	// Copy fragments from the start offset onward
	var newSeqNum uint32 = 1
	var newBaseTime uint64
	for _, frag := range fragments {
		if frag.decodeTime+uint64(frag.duration) <= startTick {
			continue
		}

		if err := copyFragmentAdjusted(in, w, frag, newSeqNum, newBaseTime); err != nil {
			return fmt.Errorf("copy fragment: %w", err)
		}
		newSeqNum++
		newBaseTime += uint64(frag.duration)
	}

	return nil
}

// ConcatMP4 concatenates multiple fMP4 files with continuous timing.
func ConcatMP4(inputs []string, outputPath string, start, duration time.Duration) error {
	if len(inputs) == 0 {
		return fmt.Errorf("no inputs")
	}

	out, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer out.Close()

	var globalSeqNum uint32 = 1
	var globalBaseTime uint64
	var timeScale uint32
	initWritten := false

	for _, path := range inputs {
		in, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open %s: %w", path, err)
		}

		// Read timescale from the first file
		if timeScale == 0 {
			ts, _ := readTimeScale(in)
			if ts > 0 {
				timeScale = ts
			}
			if _, err := in.Seek(0, io.SeekStart); err != nil {
				in.Close()
				return err
			}
		}

		initBoxes, fragments, err := indexFile(in)
		if err != nil {
			in.Close()
			return fmt.Errorf("index %s: %w", path, err)
		}

		if !initWritten {
			for _, loc := range initBoxes {
				if _, err := in.Seek(loc.offset, io.SeekStart); err != nil {
					in.Close()
					return err
				}
				if _, err := io.CopyN(out, in, loc.size); err != nil {
					in.Close()
					return err
				}
			}
			initWritten = true
		}

		for _, frag := range fragments {
			if err := copyFragmentAdjusted(in, out, frag, globalSeqNum, globalBaseTime); err != nil {
				in.Close()
				return fmt.Errorf("copy fragment from %s: %w", path, err)
			}
			globalSeqNum++
			globalBaseTime += uint64(frag.duration)
		}

		in.Close()
	}

	// Apply start/duration trimming if requested
	if start > 0 || duration > 0 {
		if timeScale == 0 {
			timeScale = 90000
		}
		totalDur := time.Duration(float64(globalBaseTime) / float64(timeScale) * float64(time.Second))
		if start > 0 || (duration > 0 && duration < totalDur) {
			// Close output before rename so TrimMP4 can rewrite it
			out.Close()
			tmpPath := outputPath + ".tmp"
			if err := os.Rename(outputPath, tmpPath); err != nil {
				return err
			}
			defer os.Remove(tmpPath)
			return TrimMP4(tmpPath, outputPath, start, duration)
		}
	}

	return nil
}

// readTimeScale extracts the track timescale from moov/trak/mdia/mdhd.
func readTimeScale(r io.ReadSeeker) (uint32, error) {
	var ts uint32
	_, err := gomp4.ReadBoxStructure(r, func(h *gomp4.ReadHandle) (interface{}, error) {
		switch h.BoxInfo.Type {
		case gomp4.BoxTypeMoov():
			return h.Expand()
		case gomp4.BoxTypeTrak():
			return h.Expand()
		case gomp4.BoxTypeMdia():
			return h.Expand()
		case gomp4.BoxTypeMdhd():
			box, _, err := h.ReadPayload()
			if err != nil {
				return nil, err
			}
			mdhd := box.(*gomp4.Mdhd)
			if ts == 0 {
				ts = mdhd.Timescale
			}
			return nil, nil
		}
		return nil, nil
	})
	return ts, err
}

// indexFile scans an fMP4 file and returns init box locations and fragment metadata.
// Uses manual box iteration to avoid ReadBoxStructure's seek interference.
func indexFile(r io.ReadSeeker) (initBoxes []boxLoc, fragments []fragment, err error) {
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, nil, err
	}

	// First pass with ReadBoxStructure to get fragment timing info
	var currentFrag *fragment
	_, err = gomp4.ReadBoxStructure(r, func(h *gomp4.ReadHandle) (interface{}, error) {
		switch h.BoxInfo.Type {
		case gomp4.BoxTypeFtyp(), gomp4.BoxTypeMoov(), gomp4.BoxTypeStyp():
			initBoxes = append(initBoxes, boxLoc{
				offset: int64(h.BoxInfo.Offset),
				size:   int64(h.BoxInfo.Size),
			})
			if h.BoxInfo.Type == gomp4.BoxTypeMoov() {
				return h.Expand()
			}
			return nil, nil

		case gomp4.BoxTypeMoof():
			currentFrag = &fragment{
				moofOffset: int64(h.BoxInfo.Offset),
				moofSize:   int64(h.BoxInfo.Size),
			}
			return h.Expand()

		case gomp4.BoxTypeTraf():
			return h.Expand()

		case gomp4.BoxTypeTfhd():
			if currentFrag != nil {
				box, _, err := h.ReadPayload()
				if err != nil {
					return nil, err
				}
				tfhd := box.(*gomp4.Tfhd)
				currentFrag.trackID = tfhd.TrackID
			}
			return nil, nil

		case gomp4.BoxTypeTfdt():
			if currentFrag != nil {
				box, _, err := h.ReadPayload()
				if err != nil {
					return nil, err
				}
				tfdt := box.(*gomp4.Tfdt)
				currentFrag.decodeTime = tfdt.GetBaseMediaDecodeTime()
			}
			return nil, nil

		case gomp4.BoxTypeTrun():
			if currentFrag != nil {
				box, _, err := h.ReadPayload()
				if err != nil {
					return nil, err
				}
				trun := box.(*gomp4.Trun)
				var totalDur uint32
				for _, e := range trun.Entries {
					totalDur += e.SampleDuration
				}
				currentFrag.duration += totalDur
			}
			return nil, nil

		case gomp4.BoxTypeMdat():
			if currentFrag != nil {
				currentFrag.mdatOffset = int64(h.BoxInfo.Offset)
				currentFrag.mdatSize = int64(h.BoxInfo.Size)
				fragments = append(fragments, *currentFrag)
				currentFrag = nil
			}
			return nil, nil
		}
		return nil, nil
	})

	return initBoxes, fragments, err
}

// copyFragmentAdjusted copies a moof+mdat pair, rewriting the sequence number
// and base decode time.
func copyFragmentAdjusted(src io.ReadSeeker, dst io.Writer, frag fragment, seqNum uint32, baseTime uint64) error {
	// Read the entire moof into memory (typically small)
	if _, err := src.Seek(frag.moofOffset, io.SeekStart); err != nil {
		return err
	}
	moofData := make([]byte, frag.moofSize)
	if _, err := io.ReadFull(src, moofData); err != nil {
		return err
	}

	patchMoof(moofData, seqNum, baseTime)

	if _, err := dst.Write(moofData); err != nil {
		return err
	}

	// Copy mdat as-is
	if _, err := src.Seek(frag.mdatOffset, io.SeekStart); err != nil {
		return err
	}
	_, err := io.CopyN(dst, src, frag.mdatSize)
	return err
}

// patchMoof modifies mfhd.SequenceNumber and tfdt.BaseMediaDecodeTime in raw moof bytes.
func patchMoof(data []byte, seqNum uint32, baseTime uint64) {
	i := 8 // Skip moof box header
	for i+8 <= len(data) {
		boxSize := int(binary.BigEndian.Uint32(data[i : i+4]))
		boxType := string(data[i+4 : i+8])

		if boxSize < 8 || i+boxSize > len(data) {
			break
		}

		switch boxType {
		case "mfhd":
			if boxSize >= 16 {
				binary.BigEndian.PutUint32(data[i+12:i+16], seqNum)
			}
		case "traf":
			patchTraf(data[i+8:i+boxSize], baseTime)
		}

		i += boxSize
	}
}

func patchTraf(data []byte, baseTime uint64) {
	i := 0
	for i+8 <= len(data) {
		boxSize := int(binary.BigEndian.Uint32(data[i : i+4]))
		boxType := string(data[i+4 : i+8])

		if boxSize < 8 || i+boxSize > len(data) {
			break
		}

		if boxType == "tfdt" {
			if boxSize >= 16 {
				version := data[i+8]
				if version == 0 {
					binary.BigEndian.PutUint32(data[i+12:i+16], uint32(baseTime))
				} else if boxSize >= 20 {
					binary.BigEndian.PutUint64(data[i+12:i+20], baseTime)
				}
			}
		}

		i += boxSize
	}
}
