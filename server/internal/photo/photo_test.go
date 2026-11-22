package photo

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func encode(t *testing.T, w, h int, asPNG bool) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		img.Set(x, x%h, color.RGBA{200, 100, 50, 255})
	}
	var buf bytes.Buffer
	if asPNG {
		png.Encode(&buf, img)
	} else {
		jpeg.Encode(&buf, img, nil)
	}
	return buf.Bytes()
}

func TestProcess(t *testing.T) {
	// A JPEG with an APP1 (EXIF) segment after SOI.
	src := encode(t, 1280, 960, false)
	exif := append([]byte{0xFF, 0xE1, 0x00, 0x0A}, []byte("Exif\x00\x00GPS!")[:8]...)
	withExif := append(append(append([]byte{}, src[:2]...), exif...), src[2:]...)
	p, err := Process(withExif)
	if err != nil {
		t.Fatal(err)
	}
	if p.Width != 1280 || p.Height != 960 || bytes.Contains(p.JPEG, []byte("Exif")) {
		t.Fatalf("processed %dx%d, exif kept %v", p.Width, p.Height, bytes.Contains(p.JPEG, []byte("Exif")))
	}
	if _, err := jpeg.Decode(bytes.NewReader(p.JPEG)); err != nil {
		t.Fatal(err)
	}
}

func TestProcessRejects(t *testing.T) {
	for name, data := range map[string][]byte{
		"png":      encode(t, 100, 100, true),
		"too wide": encode(t, 1601, 100, false),
		"garbage":  []byte("not an image"),
		"too big":  make([]byte, MaxBytes+1),
	} {
		if _, err := Process(data); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if Key("u", "p") != "meal-photos/u/p.jpg" {
		t.Error("Key")
	}
}
