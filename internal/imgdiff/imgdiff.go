// Package imgdiff shows what changed between two screenshots of the same page.
package imgdiff

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
)

// tolerance is how far a channel may move before a pixel counts as changed; it absorbs
// antialiasing and image-compression noise.
const tolerance = 32

var mark = color.RGBA{0xff, 0x2d, 0x55, 0xff}

// Result is one comparison.
type Result struct {
	Changed     float64 // share of pixels that changed, 0 to 100
	SizeChanged bool
	PNG         []byte // the new image faded to grey, with changed pixels in red
}

// Compare diffs before and after. Where the sizes differ, the area only one of them covers counts
// as changed.
func Compare(before, after []byte) (*Result, error) {
	a, err := decode(before)
	if err != nil {
		return nil, err
	}
	b, err := decode(after)
	if err != nil {
		return nil, err
	}
	w, h := max(a.Rect.Dx(), b.Rect.Dx()), max(a.Rect.Dy(), b.Rect.Dy())
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	changed := 0
	for y := range h {
		for x := range w {
			inA, inB := x < a.Rect.Dx() && y < a.Rect.Dy(), x < b.Rect.Dx() && y < b.Rect.Dy()
			o := out.PixOffset(x, y)
			if inA && inB && !differs(a.Pix[a.PixOffset(x, y):], b.Pix[b.PixOffset(x, y):]) {
				p := b.Pix[b.PixOffset(x, y):]
				// Fade to a light grey so the red marks stand out.
				g := uint8(200 + (int(p[0])*299+int(p[1])*587+int(p[2])*114)/1000*55/255)
				copy(out.Pix[o:o+4], []uint8{g, g, g, 0xff})
				continue
			}
			changed++
			copy(out.Pix[o:o+4], []uint8{mark.R, mark.G, mark.B, mark.A})
		}
	}
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, out); err != nil {
		return nil, err
	}
	return &Result{
		Changed:     100 * float64(changed) / float64(w*h),
		SizeChanged: a.Rect.Size() != b.Rect.Size(),
		PNG:         buf.Bytes(),
	}, nil
}

func differs(p, q []uint8) bool {
	for i := range 3 {
		if d := int(p[i]) - int(q[i]); d > tolerance || d < -tolerance {
			return true
		}
	}
	return false
}

func decode(data []byte) (*image.RGBA, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if rgba, ok := img.(*image.RGBA); ok && rgba.Rect.Min == (image.Point{}) {
		return rgba, nil
	}
	r := image.NewRGBA(image.Rect(0, 0, img.Bounds().Dx(), img.Bounds().Dy()))
	draw.Draw(r, r.Rect, img, img.Bounds().Min, draw.Src)
	return r, nil
}
