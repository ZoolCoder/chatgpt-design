package imgdiff

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func encode(t *testing.T, w, h int, paint func(*image.NRGBA)) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 0xff
	}
	if paint != nil {
		paint(img)
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestCompare(t *testing.T) {
	white := encode(t, 10, 10, nil)
	r, err := Compare(white, white)
	if err != nil || r.Changed != 0 || r.SizeChanged {
		t.Fatalf("same: %+v %v", r, err)
	}
	// A quarter of the pixels go black; a faint shift stays under the tolerance.
	quarter := encode(t, 10, 10, func(img *image.NRGBA) {
		for y := range 5 {
			for x := range 5 {
				img.Set(x, y, color.Black)
			}
		}
		img.Set(9, 9, color.NRGBA{0xf0, 0xf0, 0xf0, 0xff})
	})
	r, err = Compare(white, quarter)
	if err != nil || r.Changed != 25 {
		t.Fatalf("quarter: %+v %v", r, err)
	}
	out, err := png.Decode(bytes.NewReader(r.PNG))
	if err != nil {
		t.Fatal(err)
	}
	if got := color.RGBAModel.Convert(out.At(0, 0)).(color.RGBA); got != mark {
		t.Errorf("changed pixel drawn as %v", got)
	}
	if got := color.RGBAModel.Convert(out.At(9, 9)).(color.RGBA); got == mark {
		t.Error("pixel under the tolerance marked")
	}
	r, err = Compare(white, encode(t, 10, 20, nil))
	if err != nil || !r.SizeChanged || r.Changed != 50 {
		t.Fatalf("taller: %+v %v", r, err)
	}
	if _, err := Compare([]byte("not an image"), white); err == nil {
		t.Error("garbage accepted")
	}
}
