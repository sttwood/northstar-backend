package httpapi

import (
	"bytes"
	"image"
	"image/jpeg"
	"testing"
)

func TestProfilePhotoOrientationAndMetadataRemoval(t *testing.T) {
	var raw bytes.Buffer
	_ = jpeg.Encode(&raw, image.NewNRGBA(image.Rect(0, 0, 20, 10)), nil)
	for orientation := 1; orientation <= 8; orientation++ {
		exif := []byte{'E', 'x', 'i', 'f', 0, 0, 'I', 'I', 42, 0, 8, 0, 0, 0, 1, 0, 0x12, 1, 3, 0, 1, 0, 0, 0, byte(orientation), 0, 0, 0, 0, 0, 0, 0}
		segment := append([]byte{0xff, 0xe1, 0, byte(len(exif) + 2)}, exif...)
		input := append(append(append([]byte{}, raw.Bytes()[:2]...), segment...), raw.Bytes()[2:]...)
		if profileJPEGOrientation(input) != orientation {
			t.Fatal("orientation not decoded")
		}
		normalized, err := normalizeProfileImage(input)
		if err != nil {
			t.Fatal(err)
		}
		cfg, _, err := image.DecodeConfig(bytes.NewReader(normalized))
		if err != nil {
			t.Fatal(err)
		}
		w, h := 20, 10
		if orientation >= 5 {
			w, h = h, w
		}
		if cfg.Width != w || cfg.Height != h || bytes.Contains(normalized, []byte("Exif")) {
			t.Fatal("orientation or metadata stripping failed")
		}
	}
	for _, malformed := range [][]byte{{}, {0xff, 0xd8, 0xff, 0xe1, 0xff, 0xff}, {0xff, 0xd8, 0xff, 0xe1, 0, 1}} {
		if profileJPEGOrientation(malformed) != 1 {
			t.Fatal("malformed metadata accepted")
		}
	}
}
