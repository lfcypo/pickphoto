package metadata

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	exif "github.com/dsoprea/go-exif/v3"
	"github.com/lfcypo/pickphoto/internal/photofile"
)

const maxEXIFSize = 16 << 20

type Field struct {
	Name  string
	Value string
}

type Result struct {
	Fields      []Field
	Orientation int
}

func Read(path string) (result Result, err error) {
	result.Orientation = 1
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("读取 EXIF 失败: %v", recovered)
		}
	}()
	file, err := photofile.Open(path)
	if err != nil {
		return result, err
	}
	defer file.Close()
	header := make([]byte, 12)
	if _, err := io.ReadFull(file, header); err != nil {
		return result, err
	}
	var raw []byte
	switch {
	case bytes.HasPrefix(header, []byte{0xff, 0xd8}):
		_, _ = file.Seek(2, io.SeekStart)
		raw, err = jpegEXIF(file)
	case bytes.HasPrefix(header, []byte("\x89PNG\r\n\x1a\n")):
		_, _ = file.Seek(8, io.SeekStart)
		raw, err = pngEXIF(file)
	case string(header[:4]) == "RIFF" && string(header[8:12]) == "WEBP":
		raw, err = webpEXIF(file)
	default:
		return result, nil
	}
	if err != nil || len(raw) == 0 {
		return result, err
	}
	tags, _, err := exif.GetFlatExifData(raw, nil)
	if err != nil {
		return result, err
	}
	for _, tag := range tags {
		value := strings.TrimSpace(tag.Formatted)
		if value == "" {
			continue
		}
		result.Fields = append(result.Fields, Field{Name: tag.IfdPath + "/" + tag.TagName, Value: value})
		if tag.TagName == "Orientation" {
			if parsed, parseErr := strconv.Atoi(strings.Trim(value, "[] ")); parseErr == nil && parsed >= 1 && parsed <= 8 {
				result.Orientation = parsed
			}
		}
	}
	slices.SortFunc(result.Fields, func(a, b Field) int { return strings.Compare(a.Name, b.Name) })
	return result, nil
}

func Preview(path string) (orientation int, thumbnail []byte) {
	orientation = 1
	file, err := photofile.Open(path)
	if err != nil {
		return orientation, nil
	}
	defer file.Close()
	header := make([]byte, 12)
	if _, err := io.ReadFull(file, header); err != nil {
		return orientation, nil
	}
	var raw []byte
	isJPEG := bytes.HasPrefix(header, []byte{0xff, 0xd8})
	switch {
	case isJPEG:
		_, _ = file.Seek(2, io.SeekStart)
		raw, err = jpegEXIF(file)
	case bytes.HasPrefix(header, []byte("\x89PNG\r\n\x1a\n")):
		_, _ = file.Seek(8, io.SeekStart)
		raw, err = pngEXIF(file)
	case string(header[:4]) == "RIFF" && string(header[8:12]) == "WEBP":
		raw, err = webpEXIF(file)
	}
	if err != nil || len(raw) < 8 {
		return orientation, nil
	}
	var order binary.ByteOrder
	switch string(raw[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return orientation, nil
	}
	if order.Uint16(raw[2:4]) != 42 {
		return orientation, nil
	}
	rootEntries, nextIFD, ok := tiffEntries(raw, order, order.Uint32(raw[4:8]))
	if !ok {
		return orientation, nil
	}
	for _, entry := range rootEntries {
		if order.Uint16(entry[:2]) == 0x0112 && order.Uint16(entry[2:4]) == 3 && order.Uint32(entry[4:8]) == 1 {
			value := int(order.Uint16(entry[8:10]))
			if value >= 1 && value <= 8 {
				orientation = value
			}
			break
		}
	}
	if !isJPEG || nextIFD == 0 {
		return orientation, nil
	}
	entries, _, ok := tiffEntries(raw, order, nextIFD)
	if !ok {
		return orientation, nil
	}
	var offset, size uint32
	for _, entry := range entries {
		if order.Uint16(entry[2:4]) != 4 || order.Uint32(entry[4:8]) != 1 {
			continue
		}
		switch order.Uint16(entry[:2]) {
		case 0x0201:
			offset = order.Uint32(entry[8:12])
		case 0x0202:
			size = order.Uint32(entry[8:12])
		}
	}
	if size < 4 || uint64(offset)+uint64(size) > uint64(len(raw)) {
		return orientation, nil
	}
	thumbnail = raw[offset : offset+size]
	if !bytes.HasPrefix(thumbnail, []byte{0xff, 0xd8}) {
		thumbnail = nil
	}
	return orientation, thumbnail
}

func tiffEntries(raw []byte, order binary.ByteOrder, offset uint32) ([][]byte, uint32, bool) {
	if uint64(offset)+2 > uint64(len(raw)) {
		return nil, 0, false
	}
	count := uint64(order.Uint16(raw[offset : offset+2]))
	end := uint64(offset) + 2 + count*12
	if end+4 > uint64(len(raw)) {
		return nil, 0, false
	}
	entries := make([][]byte, 0, count)
	for position := uint64(offset) + 2; position < end; position += 12 {
		entries = append(entries, raw[position:position+12])
	}
	return entries, order.Uint32(raw[end : end+4]), true
}

func jpegEXIF(file *os.File) ([]byte, error) {
	for {
		var marker [2]byte
		if _, err := io.ReadFull(file, marker[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return nil, nil
			}
			return nil, err
		}
		if marker[0] != 0xff || marker[1] == 0xda || marker[1] == 0xd9 {
			return nil, nil
		}
		if marker[1] == 0xff || marker[1] == 0x00 || marker[1] == 0xd8 || marker[1] == 0x01 {
			continue
		}
		var sizeBytes [2]byte
		if _, err := io.ReadFull(file, sizeBytes[:]); err != nil {
			return nil, err
		}
		size := int(binary.BigEndian.Uint16(sizeBytes[:])) - 2
		if size < 0 {
			return nil, errors.New("JPEG 段长度无效")
		}
		if marker[1] == 0xe1 && size >= 6 && size <= maxEXIFSize+6 {
			data := make([]byte, size)
			if _, err := io.ReadFull(file, data); err != nil {
				return nil, err
			}
			if bytes.HasPrefix(data, []byte("Exif\x00\x00")) {
				return data[6:], nil
			}
			continue
		}
		if _, err := file.Seek(int64(size), io.SeekCurrent); err != nil {
			return nil, err
		}
	}
}

func pngEXIF(file *os.File) ([]byte, error) {
	for {
		var header [8]byte
		if _, err := io.ReadFull(file, header[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return nil, nil
			}
			return nil, err
		}
		size := binary.BigEndian.Uint32(header[:4])
		kind := string(header[4:])
		if kind == "IEND" {
			return nil, nil
		}
		if kind == "eXIf" && size <= maxEXIFSize {
			data := make([]byte, size)
			_, err := io.ReadFull(file, data)
			return data, err
		}
		if _, err := file.Seek(int64(size)+4, io.SeekCurrent); err != nil {
			return nil, err
		}
	}
}

func webpEXIF(file *os.File) ([]byte, error) {
	for {
		var header [8]byte
		if _, err := io.ReadFull(file, header[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return nil, nil
			}
			return nil, err
		}
		size := binary.LittleEndian.Uint32(header[4:])
		if string(header[:4]) == "EXIF" && size <= maxEXIFSize {
			data := make([]byte, size)
			_, err := io.ReadFull(file, data)
			return data, err
		}
		if _, err := file.Seek(int64(size)+int64(size%2), io.SeekCurrent); err != nil {
			return nil, err
		}
	}
}
