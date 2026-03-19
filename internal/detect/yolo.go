package detect

import (
	"image"
	"math"
	"sort"
)

const (
	modelInputSize = 640
	numClasses     = 80
	numDetections  = 8400 // YOLOv8 outputs 8400 candidate detections
)

// prepareInput converts an RGBA image to a float32 tensor in CHW format
// normalized to [0, 1], resized to 640x640 with letterboxing.
func prepareInput(img *image.RGBA) ([]float32, float64, float64, float64) {
	bounds := img.Bounds()
	origW := float64(bounds.Dx())
	origH := float64(bounds.Dy())

	// Compute scale to fit within 640x640 while preserving aspect ratio
	scale := math.Min(float64(modelInputSize)/origW, float64(modelInputSize)/origH)
	newW := int(origW * scale)
	newH := int(origH * scale)

	// Offsets for centering the image (letterboxing)
	padX := (modelInputSize - newW) / 2
	padY := (modelInputSize - newH) / 2

	// Allocate CHW tensor: 3 channels x 640 x 640
	tensorSize := 3 * modelInputSize * modelInputSize
	input := make([]float32, tensorSize)

	// Fill with gray (0.5) for letterbox padding
	for i := range input {
		input[i] = 0.5
	}

	channelStride := modelInputSize * modelInputSize

	// Nearest-neighbor resize + normalize to [0, 1]
	for y := 0; y < newH; y++ {
		srcY := int(float64(y) / scale)
		if srcY >= bounds.Dy() {
			srcY = bounds.Dy() - 1
		}
		for x := 0; x < newW; x++ {
			srcX := int(float64(x) / scale)
			if srcX >= bounds.Dx() {
				srcX = bounds.Dx() - 1
			}

			srcIdx := (srcY*bounds.Dx() + srcX) * 4 // RGBA stride
			r := float32(img.Pix[srcIdx+0]) / 255.0
			g := float32(img.Pix[srcIdx+1]) / 255.0
			b := float32(img.Pix[srcIdx+2]) / 255.0

			dstY := y + padY
			dstX := x + padX
			dstIdx := dstY*modelInputSize + dstX

			input[0*channelStride+dstIdx] = r
			input[1*channelStride+dstIdx] = g
			input[2*channelStride+dstIdx] = b
		}
	}

	return input, scale, float64(padX), float64(padY)
}

// processOutput extracts detections from the raw YOLOv8 output tensor.
// The output shape is (1, 84, 8400): 4 bbox coords + 80 class scores per detection.
func processOutput(output []float32, scoreThreshold float32, scale, padX, padY float64) []Detection {
	var detections []Detection

	for i := 0; i < numDetections; i++ {
		// Find the class with highest confidence
		maxScore := float32(0)
		maxClass := 0
		for c := 0; c < numClasses; c++ {
			// Output layout: row-major (84, 8400), so element [row][col] = output[row*8400 + col]
			score := output[(4+c)*numDetections+i]
			if score > maxScore {
				maxScore = score
				maxClass = c
			}
		}

		if maxScore < scoreThreshold {
			continue
		}

		// Extract bounding box (center_x, center_y, width, height)
		cx := float64(output[0*numDetections+i])
		cy := float64(output[1*numDetections+i])
		w := float64(output[2*numDetections+i])
		h := float64(output[3*numDetections+i])

		// Convert from center format to corner format
		x1 := cx - w/2
		y1 := cy - h/2
		x2 := cx + w/2
		y2 := cy + h/2

		// Remove letterbox padding and scale back to original image coordinates
		x1 = (x1 - padX) / scale
		y1 = (y1 - padY) / scale
		x2 = (x2 - padX) / scale
		y2 = (y2 - padY) / scale

		label := "unknown"
		if maxClass < len(CocoLabels) {
			label = CocoLabels[maxClass]
		}

		detections = append(detections, Detection{
			Label: label,
			Score: maxScore,
			Box:   [4]int{int(x1), int(y1), int(x2), int(y2)},
		})
	}

	// Apply Non-Maximum Suppression
	detections = nms(detections, 0.5)

	return detections
}

// nms applies Non-Maximum Suppression to filter overlapping detections.
func nms(detections []Detection, iouThreshold float64) []Detection {
	if len(detections) == 0 {
		return nil
	}

	// Sort by score descending
	sort.Slice(detections, func(i, j int) bool {
		return detections[i].Score > detections[j].Score
	})

	keep := make([]bool, len(detections))
	for i := range keep {
		keep[i] = true
	}

	for i := 0; i < len(detections); i++ {
		if !keep[i] {
			continue
		}
		for j := i + 1; j < len(detections); j++ {
			if !keep[j] {
				continue
			}
			if detections[i].Label == detections[j].Label {
				if iou(detections[i].Box, detections[j].Box) > iouThreshold {
					keep[j] = false
				}
			}
		}
	}

	var result []Detection
	for i, d := range detections {
		if keep[i] {
			result = append(result, d)
		}
	}
	return result
}

// iou computes Intersection over Union between two bounding boxes.
func iou(a, b [4]int) float64 {
	x1 := math.Max(float64(a[0]), float64(b[0]))
	y1 := math.Max(float64(a[1]), float64(b[1]))
	x2 := math.Min(float64(a[2]), float64(b[2]))
	y2 := math.Min(float64(a[3]), float64(b[3]))

	intersection := math.Max(0, x2-x1) * math.Max(0, y2-y1)
	areaA := float64(a[2]-a[0]) * float64(a[3]-a[1])
	areaB := float64(b[2]-b[0]) * float64(b[3]-b[1])
	union := areaA + areaB - intersection

	if union == 0 {
		return 0
	}
	return intersection / union
}
