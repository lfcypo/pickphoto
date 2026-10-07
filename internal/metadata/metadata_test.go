package metadata

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
)

func TestReadOrientationAcrossContainers(t *testing.T) {
	tiff := []byte{
		'I', 'I', 42, 0, 8, 0, 0, 0,
		1, 0,
		0x12, 0x01, 3, 0, 1, 0, 0, 0, 6, 0, 0, 0,
		0, 0, 0, 0,
	}
	jpegSegment := append([]byte("Exif\x00\x00"), tiff...)
	jpeg := []byte{0xff, 0xd8, 0xff, 0xe1, 0, byte(len(jpegSegment) + 2)}
	jpeg = append(jpeg, jpegSegment...)
	jpeg = append(jpeg, 0xff, 0xd9)
	var png bytes.Buffer
	png.Write([]byte("\x89PNG\r\n\x1a\n"))
	_ = binary.Write(&png, binary.BigEndian, uint32(len(tiff)))
	png.WriteString("eXIf")
	png.Write(tiff)
	png.Write(make([]byte, 4))
	var webp bytes.Buffer
	webp.WriteString("RIFF")
	_ = binary.Write(&webp, binary.LittleEndian, uint32(len(tiff)+12))
	webp.WriteString("WEBP")
	webp.WriteString("EXIF")
	_ = binary.Write(&webp, binary.LittleEndian, uint32(len(tiff)))
	webp.Write(tiff)
	for _, test := range []struct {
		name string
		data []byte
	}{{"a.jpg", jpeg}, {"b.png", png.Bytes()}, {"c.webp", webp.Bytes()}} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), test.name)
			if err := os.WriteFile(path, test.data, 0o600); err != nil {
				t.Fatal(err)
			}
			result, err := Read(path)
			if err != nil {
				t.Fatal(err)
			}
			if result.Orientation != 6 || len(result.Fields) == 0 {
				t.Fatalf("方向或字段缺失: %+v", result)
			}
		})
	}
}

func TestPreviewUsesEmbeddedJPEGThumbnail(t *testing.T) {
	var thumbnail bytes.Buffer
	photo := image.NewRGBA(image.Rect(0, 0, 2, 1))
	photo.Set(0, 0, color.RGBA{R: 255, A: 255})
	photo.Set(1, 0, color.RGBA{B: 255, A: 255})
	if err := jpeg.Encode(&thumbnail, photo, nil); err != nil {
		t.Fatal(err)
	}
	tiff := make([]byte, 56+thumbnail.Len())
	copy(tiff[:2], "II")
	binary.LittleEndian.PutUint16(tiff[2:4], 42)
	binary.LittleEndian.PutUint32(tiff[4:8], 8)
	binary.LittleEndian.PutUint16(tiff[8:10], 1)
	binary.LittleEndian.PutUint16(tiff[10:12], 0x0112)
	binary.LittleEndian.PutUint16(tiff[12:14], 3)
	binary.LittleEndian.PutUint32(tiff[14:18], 1)
	binary.LittleEndian.PutUint16(tiff[18:20], 6)
	binary.LittleEndian.PutUint32(tiff[22:26], 26)
	binary.LittleEndian.PutUint16(tiff[26:28], 2)
	binary.LittleEndian.PutUint16(tiff[28:30], 0x0201)
	binary.LittleEndian.PutUint16(tiff[30:32], 4)
	binary.LittleEndian.PutUint32(tiff[32:36], 1)
	binary.LittleEndian.PutUint32(tiff[36:40], 56)
	binary.LittleEndian.PutUint16(tiff[40:42], 0x0202)
	binary.LittleEndian.PutUint16(tiff[42:44], 4)
	binary.LittleEndian.PutUint32(tiff[44:48], 1)
	binary.LittleEndian.PutUint32(tiff[48:52], uint32(thumbnail.Len()))
	copy(tiff[56:], thumbnail.Bytes())
	segment := append([]byte("Exif\x00\x00"), tiff...)
	jpegData := []byte{0xff, 0xd8, 0xff, 0xe1, 0, 0}
	binary.BigEndian.PutUint16(jpegData[4:6], uint16(len(segment)+2))
	jpegData = append(jpegData, segment...)
	jpegData = append(jpegData, 0xff, 0xd9)
	path := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(path, jpegData, 0o600); err != nil {
		t.Fatal(err)
	}
	orientation, embedded := Preview(path)
	if orientation != 6 || !bytes.Equal(embedded, thumbnail.Bytes()) {
		t.Fatalf("内嵌预览或方向错误: orientation=%d bytes=%d", orientation, len(embedded))
	}
}
