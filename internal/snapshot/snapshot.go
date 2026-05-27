package snapshot

import (
	"bufio"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"os"
	"path/filepath"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"

	"github.com/rvben/vedetta/internal/detect"
)

// Label colors by detection class
var labelColors = map[string]color.RGBA{
	"person":     {R: 255, G: 0, B: 0, A: 255},   // Red
	"car":        {R: 0, G: 0, B: 255, A: 255},   // Blue
	"truck":      {R: 0, G: 100, B: 255, A: 255}, // Light blue
	"bicycle":    {R: 255, G: 165, B: 0, A: 255}, // Orange
	"motorcycle": {R: 255, G: 100, B: 0, A: 255}, // Dark orange
	"bus":        {R: 100, G: 0, B: 255, A: 255}, // Purple
	"cat":        {R: 0, G: 255, B: 0, A: 255},   // Green
	"dog":        {R: 0, G: 200, B: 100, A: 255}, // Teal
	"bird":       {R: 255, G: 255, B: 0, A: 255}, // Yellow
}

var defaultColor = color.RGBA{R: 0, G: 255, B: 255, A: 255} // Cyan

// DrawDetections draws bounding boxes and labels onto a copy of the image.
func DrawDetections(img *image.RGBA, detections []detect.Detection) *image.RGBA {
	bounds := img.Bounds()
	out := image.NewRGBA(bounds)
	draw.Draw(out, bounds, img, bounds.Min, draw.Src)

	DrawDetectionsInPlace(out, detections)

	return out
}

// DrawDetectionsInPlace draws bounding boxes and labels directly onto the image.
func DrawDetectionsInPlace(img *image.RGBA, detections []detect.Detection) {
	for _, d := range detections {
		c := colorForLabel(d.Label)
		drawBox(img, d.Box, c)
		label := fmt.Sprintf("%s %.0f%%", d.Label, d.Score*100)
		drawLabel(img, d.Box[0], d.Box[1]-2, label, c)
	}
}

// DrawDetectionsWithPrimary draws all detections but highlights the primary one
// (the detection that triggered the event) with a thicker, brighter box.
func DrawDetectionsWithPrimary(img *image.RGBA, detections []detect.Detection, primaryBox [4]int) {
	for _, d := range detections {
		if d.Box == primaryBox {
			// Primary: bright green, thick
			c := color.RGBA{R: 0, G: 255, B: 100, A: 255}
			drawThickBox(img, d.Box, c, 4)
			label := fmt.Sprintf("%s %.0f%%", d.Label, d.Score*100)
			drawLabel(img, d.Box[0], d.Box[1]-2, label, c)
		} else {
			// Secondary: dimmed
			c := colorForLabel(d.Label)
			c.A = 180
			drawBox(img, d.Box, c)
		}
	}
}

func colorForLabel(label string) color.RGBA {
	if c, ok := labelColors[label]; ok {
		return c
	}
	return defaultColor
}

// drawBox draws a 2-pixel thick rectangle on the image using direct Pix slice access.
func drawBox(img *image.RGBA, box [4]int, c color.RGBA) {
	x1, y1, x2, y2 := box[0], box[1], box[2], box[3]
	bounds := img.Bounds()

	// Clamp to image bounds
	if x1 < bounds.Min.X {
		x1 = bounds.Min.X
	}
	if y1 < bounds.Min.Y {
		y1 = bounds.Min.Y
	}
	if x2 > bounds.Max.X {
		x2 = bounds.Max.X
	}
	if y2 > bounds.Max.Y {
		y2 = bounds.Max.Y
	}

	if x1 >= x2 || y1 >= y2 {
		return
	}

	stride := img.Stride
	pix := img.Pix
	minX := bounds.Min.X
	minY := bounds.Min.Y
	thickness := 2

	setPixel := func(x, y int) {
		off := (y-minY)*stride + (x-minX)*4
		pix[off] = c.R
		pix[off+1] = c.G
		pix[off+2] = c.B
		pix[off+3] = c.A
	}

	// Top edge
	for t := 0; t < thickness; t++ {
		y := y1 + t
		if y >= bounds.Max.Y {
			break
		}
		for x := x1; x < x2; x++ {
			setPixel(x, y)
		}
	}
	// Bottom edge
	for t := 0; t < thickness; t++ {
		y := y2 - 1 - t
		if y < bounds.Min.Y {
			break
		}
		for x := x1; x < x2; x++ {
			setPixel(x, y)
		}
	}
	// Left edge
	for t := 0; t < thickness; t++ {
		x := x1 + t
		if x >= bounds.Max.X {
			break
		}
		for y := y1; y < y2; y++ {
			setPixel(x, y)
		}
	}
	// Right edge
	for t := 0; t < thickness; t++ {
		x := x2 - 1 - t
		if x < bounds.Min.X {
			break
		}
		for y := y1; y < y2; y++ {
			setPixel(x, y)
		}
	}
}

// drawThickBox draws a rectangle with variable thickness.
func drawThickBox(img *image.RGBA, box [4]int, c color.RGBA, thickness int) {
	x1, y1, x2, y2 := box[0], box[1], box[2], box[3]
	bounds := img.Bounds()

	if x1 < bounds.Min.X {
		x1 = bounds.Min.X
	}
	if y1 < bounds.Min.Y {
		y1 = bounds.Min.Y
	}
	if x2 > bounds.Max.X {
		x2 = bounds.Max.X
	}
	if y2 > bounds.Max.Y {
		y2 = bounds.Max.Y
	}
	if x1 >= x2 || y1 >= y2 {
		return
	}

	stride := img.Stride
	pix := img.Pix
	minX := bounds.Min.X
	minY := bounds.Min.Y

	setPixel := func(x, y int) {
		off := (y-minY)*stride + (x-minX)*4
		pix[off] = c.R
		pix[off+1] = c.G
		pix[off+2] = c.B
		pix[off+3] = c.A
	}

	for t := 0; t < thickness; t++ {
		if y := y1 + t; y < bounds.Max.Y {
			for x := x1; x < x2; x++ {
				setPixel(x, y)
			}
		}
		if y := y2 - 1 - t; y >= bounds.Min.Y {
			for x := x1; x < x2; x++ {
				setPixel(x, y)
			}
		}
		if x := x1 + t; x < bounds.Max.X {
			for y := y1; y < y2; y++ {
				setPixel(x, y)
			}
		}
		if x := x2 - 1 - t; x >= bounds.Min.X {
			for y := y1; y < y2; y++ {
				setPixel(x, y)
			}
		}
	}
}

// drawLabel draws text with a background rectangle above a bounding box.
func drawLabel(img *image.RGBA, x, y int, text string, c color.RGBA) {
	face := basicfont.Face7x13
	textWidth := font.MeasureString(face, text).Ceil()
	textHeight := face.Metrics().Height.Ceil()

	// Background rectangle
	bgY := y - textHeight
	if bgY < 0 {
		bgY = 0
		y = textHeight
	}

	for by := bgY; by < y; by++ {
		for bx := x; bx < x+textWidth+4; bx++ {
			if bx >= 0 && bx < img.Bounds().Max.X && by >= 0 && by < img.Bounds().Max.Y {
				img.SetRGBA(bx, by, c)
			}
		}
	}

	// Draw text in contrasting color
	textColor := color.RGBA{R: 0, G: 0, B: 0, A: 255}
	if c.R < 128 && c.G < 128 && c.B < 128 {
		textColor = color.RGBA{R: 255, G: 255, B: 255, A: 255}
	}

	drawer := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(textColor),
		Face: face,
		Dot:  fixed.P(x+2, y-2),
	}
	drawer.DrawString(text)
}

// SaveSnapshot encodes an image as JPEG and writes it to disk.
func SaveSnapshot(img *image.RGBA, path string, quality int) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}

	if quality <= 0 || quality > 100 {
		quality = 85
	}

	w := bufio.NewWriter(f)
	if err := jpeg.Encode(w, img, &jpeg.Options{Quality: quality}); err != nil {
		_ = f.Close()
		return fmt.Errorf("encode jpeg: %w", err)
	}

	if err := w.Flush(); err != nil {
		_ = f.Close()
		return fmt.Errorf("flush buffer: %w", err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("close file: %w", err)
	}

	return nil
}
