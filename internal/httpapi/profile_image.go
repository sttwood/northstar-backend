package httpapi

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
)

func profileImageType(b []byte) (string, error) {
	if len(b) > 2*1024*1024 {
		return "", errors.New("profile photo must be smaller than 2 MB")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || (format != "jpeg" && format != "png") || config.Width < 1 || config.Height < 1 || config.Width > 4096 || config.Height > 4096 || config.Width*config.Height > 16000000 {
		return "", errors.New("choose a valid PNG or JPEG profile photo up to 4096 pixels")
	}
	if format == "jpeg" {
		return "image/jpeg", nil
	}
	return "image/png", nil
}
func normalizeProfileImage(b []byte) ([]byte, error) {
	return normalizeImage(b, 512)
}
func normalizeCommunityImage(b []byte) ([]byte, error) {
	result, err := normalizeImage(b, 1600)
	if err != nil {
		return nil, errors.New("community image could not be decoded")
	}
	return result, nil
}
func normalizeImage(b []byte, maxSide int) ([]byte, error) {
	if _, err := profileImageType(b); err != nil {
		return nil, err
	}
	source, format, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, errors.New("profile photo could not be decoded")
	}
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	orientation := 1
	if format == "jpeg" {
		orientation = profileJPEGOrientation(b)
	}
	if orientation >= 5 {
		width, height = height, width
	}
	orientedWidth, orientedHeight := width, height
	if width > maxSide || height > maxSide {
		if width >= height {
			height = max(1, height*maxSide/width)
			width = maxSide
		} else {
			width = max(1, width*maxSide/height)
			height = maxSide
		}
	}
	target := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			ox, oy := x*orientedWidth/width, y*orientedHeight/height
			sx, sy := ox, oy
			switch orientation {
			case 2:
				sx = bounds.Dx() - 1 - ox
			case 3:
				sx, sy = bounds.Dx()-1-ox, bounds.Dy()-1-oy
			case 4:
				sy = bounds.Dy() - 1 - oy
			case 5:
				sx, sy = oy, ox
			case 6:
				sx, sy = oy, bounds.Dy()-1-ox
			case 7:
				sx, sy = bounds.Dx()-1-oy, bounds.Dy()-1-ox
			case 8:
				sx, sy = bounds.Dx()-1-oy, ox
			}
			target.Set(x, y, source.At(bounds.Min.X+sx, bounds.Min.Y+sy))
		}
	}
	var out bytes.Buffer
	if format == "jpeg" {
		err = jpeg.Encode(&out, target, &jpeg.Options{Quality: 88})
	} else {
		err = png.Encode(&out, target)
	}
	return out.Bytes(), err
}

// Read only the bounded TIFF orientation entry. All metadata is discarded on encoding.
func profileJPEGOrientation(b []byte) int {
	if len(b) < 4 || b[0] != 0xff || b[1] != 0xd8 {
		return 1
	}
	for pos := 2; pos+4 <= len(b); {
		if b[pos] != 0xff {
			return 1
		}
		marker := b[pos+1]
		if marker == 0xff {
			pos++
			continue
		}
		if marker == 0xda || marker == 0xd9 {
			return 1
		}
		if marker == 0x01 || marker >= 0xd0 && marker <= 0xd7 {
			pos += 2
			continue
		}
		length := int(binary.BigEndian.Uint16(b[pos+2 : pos+4]))
		if length < 2 || pos+2+length > len(b) {
			return 1
		}
		segment := b[pos+4 : pos+2+length]
		pos += length + 2
		if marker != 0xe1 || len(segment) < 14 || !bytes.HasPrefix(segment, []byte("Exif\x00\x00")) {
			continue
		}
		data := segment[6:]
		var order binary.ByteOrder
		if string(data[:2]) == "II" {
			order = binary.LittleEndian
		} else if string(data[:2]) == "MM" {
			order = binary.BigEndian
		} else {
			return 1
		}
		if order.Uint16(data[2:4]) != 42 {
			return 1
		}
		offset := uint64(order.Uint32(data[4:8]))
		if offset+2 > uint64(len(data)) {
			return 1
		}
		count := int(order.Uint16(data[offset : offset+2]))
		start := int(offset) + 2
		for n := 0; n < count; n++ {
			at := start + n*12
			if at+12 > len(data) {
				return 1
			}
			entry := data[at : at+12]
			if order.Uint16(entry[:2]) == 0x0112 && order.Uint16(entry[2:4]) == 3 && order.Uint32(entry[4:8]) == 1 {
				orientation := int(order.Uint16(entry[8:10]))
				if orientation >= 1 && orientation <= 8 {
					return orientation
				}
				return 1
			}
		}
	}
	return 1
}
