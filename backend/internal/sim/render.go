package sim

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"math/rand/v2"
	"strconv"
	"strings"
)

const (
	frameW = 640
	frameH = 360
)

var (
	floorColor  = color.RGBA{58, 62, 68, 255}
	gridColor   = color.RGBA{70, 75, 82, 255}
	shelfColor  = color.RGBA{92, 78, 60, 255}
	bodyColor   = color.RGBA{230, 120, 40, 255} // hi-vis vest
	skinColor   = color.RGBA{214, 170, 140, 255}
	helmetColor = color.RGBA{250, 210, 40, 255}
	carColor    = color.RGBA{60, 110, 200, 255}
	truckColor  = color.RGBA{200, 60, 60, 255}
	glassColor  = color.RGBA{30, 40, 55, 255}
	boxColor    = color.RGBA{34, 197, 94, 255}
	alertColor  = color.RGBA{239, 68, 68, 255}
)

// renderJPEG draws the synthetic scene. With annotate=true (incident snapshots) zones and
// bounding boxes are burned in, highlighted tracks in red; live frames stay clean because
// the dashboard draws its own overlay from the frame messages.
func renderJPEG(c *camera, highlight map[int]bool, annotate bool) []byte {
	img := image.NewRGBA(image.Rect(0, 0, frameW, frameH))
	draw.Draw(img, img.Bounds(), &image.Uniform{floorColor}, image.Point{}, draw.Src)
	for x := 0; x < frameW; x += 40 {
		fill(img, x, 0, x+1, frameH, gridColor)
	}
	for y := 0; y < frameH; y += 40 {
		fill(img, 0, y, frameW, y+1, gridColor)
	}
	// Static "shelving", deterministic per camera.
	r := rand.New(rand.NewPCG(c.seed, 7)) //nolint:gosec // decorative layout
	for range 3 {
		x := 20 + r.IntN(frameW-140)
		y := 20 + r.IntN(frameH-60)
		fill(img, x, y, x+80+r.IntN(60), y+14, shelfColor)
	}
	if annotate {
		for _, z := range c.cfg.Zones {
			col := parseHex(z.Color, boxColor)
			n := len(z.Points)
			for i := range n {
				if z.Kind == "line" && i == n-1 {
					break
				}
				a, b := z.Points[i], z.Points[(i+1)%n]
				line(img, int(a[0]*frameW), int(a[1]*frameH), int(b[0]*frameW), int(b[1]*frameH), col)
			}
		}
	}
	for _, t := range c.tracks {
		x1, y1 := int((t.cx-t.w/2)*frameW), int((t.cy-t.h/2)*frameH)
		x2, y2 := int((t.cx+t.w/2)*frameW), int((t.cy+t.h/2)*frameH)
		if t.cls == "person" {
			headH := (y2 - y1) / 4
			fill(img, x1+(x2-x1)/4, y1, x2-(x2-x1)/4, y1+headH, skinColor)
			if t.helmet {
				fill(img, x1+(x2-x1)/5, y1, x2-(x2-x1)/5, y1+headH/2, helmetColor)
			}
			fill(img, x1, y1+headH, x2, y2, bodyColor)
		} else {
			col := carColor
			if t.cls == "truck" {
				col = truckColor
			}
			fill(img, x1, y1, x2, y2, col)
			fill(img, x1+(x2-x1)/5, y1+(y2-y1)/5, x2-(x2-x1)/5, y1+(y2-y1)/2, glassColor)
		}
		if annotate {
			col := boxColor
			if highlight[t.id] {
				col = alertColor
			}
			rect(img, x1-2, y1-2, x2+2, y2+2, col)
		}
	}
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 70})
	return buf.Bytes()
}

func fill(img *image.RGBA, x1, y1, x2, y2 int, col color.Color) {
	draw.Draw(img, image.Rect(x1, y1, x2, y2).Intersect(img.Bounds()), &image.Uniform{col}, image.Point{}, draw.Src)
}

func rect(img *image.RGBA, x1, y1, x2, y2 int, col color.Color) {
	fill(img, x1, y1, x2, y1+2, col)
	fill(img, x1, y2-2, x2, y2, col)
	fill(img, x1, y1, x1+2, y2, col)
	fill(img, x2-2, y1, x2, y2, col)
}

func line(img *image.RGBA, x1, y1, x2, y2 int, col color.Color) {
	dx, dy := abs(x2-x1), -abs(y2-y1)
	sx, sy := sign(x2-x1), sign(y2-y1)
	e := dx + dy
	for {
		fill(img, x1-1, y1-1, x1+1, y1+1, col)
		if x1 == x2 && y1 == y2 {
			return
		}
		e2 := 2 * e
		if e2 >= dy {
			e += dy
			x1 += sx
		}
		if e2 <= dx {
			e += dx
			y1 += sy
		}
	}
}

func parseHex(s string, def color.RGBA) color.RGBA {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return def
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return def
	}
	return color.RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 255} //nolint:gosec // masked bytes
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func sign(v int) int {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	}
	return 0
}
