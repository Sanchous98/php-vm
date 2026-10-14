package php

import (
	"bytes"
	"compress/zlib"
	"io"
	"os"
	"strconv"
	"strings"

	"phpvm/internal/vm"
)

// Файл содержит getimagesize/getimagesizefromstring/image_type_to_mime_type/
// iptcparse — порт ext/standard/image.c (PHP 8.3).

// Типы изображений (IMAGE_FILETYPE_*).
const (
	imageFiletypeUnknown = 0
	imageFiletypeGIF     = 1
	imageFiletypeJPEG    = 2
	imageFiletypePNG     = 3
	imageFiletypeSWF     = 4
	imageFiletypePSD     = 5
	imageFiletypeBMP     = 6
	imageFiletypeTIFFII  = 7
	imageFiletypeTIFFMM  = 8
	imageFiletypeJPC     = 9
	imageFiletypeJP2     = 10
	imageFiletypeJPX     = 11
	imageFiletypeJB2     = 12
	imageFiletypeSWC     = 13
	imageFiletypeIFF     = 14
	imageFiletypeWBMP    = 15
	imageFiletypeXBM     = 16
	imageFiletypeICO     = 17
	imageFiletypeWEBP    = 18
	imageFiletypeAVIF    = 19
)

// imageTypeToMimeType повторяет php_image_type_to_mime_type.
func imageTypeToMimeType(t int) string {
	switch t {
	case imageFiletypeGIF:
		return "image/gif"
	case imageFiletypeJPEG:
		return "image/jpeg"
	case imageFiletypePNG:
		return "image/png"
	case imageFiletypeSWF, imageFiletypeSWC:
		return "application/x-shockwave-flash"
	case imageFiletypePSD:
		return "image/psd"
	case imageFiletypeBMP:
		return "image/bmp"
	case imageFiletypeTIFFII, imageFiletypeTIFFMM:
		return "image/tiff"
	case imageFiletypeIFF:
		return "image/iff"
	case imageFiletypeWBMP:
		return "image/vnd.wap.wbmp"
	case imageFiletypeJPC:
		return "application/octet-stream"
	case imageFiletypeJP2:
		return "image/jp2"
	case imageFiletypeXBM:
		return "image/xbm"
	case imageFiletypeICO:
		return "image/vnd.microsoft.icon"
	case imageFiletypeWEBP:
		return "image/webp"
	case imageFiletypeAVIF:
		return "image/avif"
	}
	return "application/octet-stream"
}

// gfxinfo — размеры и параметры изображения (struct gfxinfo).
type gfxinfo struct {
	width    uint32
	height   uint32
	bits     uint32
	channels uint32
}

// imageReader — последовательное чтение байтов файла (аналог php_stream).
type imageReader struct {
	data []byte
	pos  int
}

func (r *imageReader) read(n int) ([]byte, bool) {
	if r.pos < 0 || r.pos+n > len(r.data) || n < 0 {
		return nil, false
	}
	b := r.data[r.pos : r.pos+n]
	r.pos += n
	return b, true
}

func (r *imageReader) getc() int {
	if r.pos < 0 || r.pos >= len(r.data) {
		r.pos++
		return -1
	}
	c := int(r.data[r.pos])
	r.pos++
	return c
}

func (r *imageReader) seekDelta(d int) bool {
	np := r.pos + d
	if np < 0 || np > len(r.data) {
		return true // PHP: seek за EOF не ошибка до следующего чтения
	}
	r.pos = np
	return false
}

func (r *imageReader) seekAbs(p int) bool {
	if p < 0 || p > len(r.data) {
		return true
	}
	r.pos = p
	return false
}

func (r *imageReader) rewind() bool {
	r.pos = 0
	return false
}

// gets читает строку до '\n' включительно.
func (r *imageReader) gets() (string, bool) {
	if r.pos >= len(r.data) {
		return "", false
	}
	i := bytes.IndexByte(r.data[r.pos:], '\n')
	if i < 0 {
		s := string(r.data[r.pos:])
		r.pos = len(r.data)
		return s, true
	}
	s := string(r.data[r.pos : r.pos+i+1])
	r.pos += i + 1
	return s, true
}

func read2(r *imageReader) uint16 {
	b, ok := r.read(2)
	if !ok {
		return 0
	}
	return uint16(b[0])<<8 | uint16(b[1])
}

func read4(r *imageReader) uint32 {
	b, ok := r.read(4)
	if !ok {
		return 0
	}
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

// --- определение типа файла ---

var (
	sigGIF    = []byte{'G', 'I', 'F'}
	sigJPG    = []byte{0xFF, 0xD8, 0xFF}
	sigPNG    = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}
	sigSWF    = []byte{'F', 'W', 'S'}
	sigSWC    = []byte{'C', 'W', 'S'}
	sigPSD    = []byte{'8', 'B', 'P', 'S'}
	sigBMP    = []byte{'B', 'M'}
	sigJPC    = []byte{0xFF, 0x4F, 0xFF}
	sigRIFF   = []byte{'R', 'I', 'F', 'F'}
	sigWEBP   = []byte{'W', 'E', 'B', 'P'}
	sigTIFFII = []byte{'I', 'I', 0x2A, 0x00}
	sigTIFFMM = []byte{'M', 'M', 0x00, 0x2A}
	sigIFF    = []byte{'F', 'O', 'R', 'M'}
	sigICO    = []byte{0x00, 0x00, 0x01, 0x00}
	sigJP2    = []byte{0x00, 0x00, 0x00, 0x0C, 0x6A, 0x50, 0x20, 0x20, 0x0D, 0x0A, 0x87, 0x0A}
)

// phpGetImageType — php_getimagetype: определяет тип и оставляет поток на
// позиции, с которой начинается разбор конкретного формата.
func phpGetImageType(ctx *vm.Context, r *imageReader, name, fnName string) int {
	readErr := func() {
		ctx.VM().Notice(fnName + "(): Error reading from " + name + "!")
	}
	tmp, ok := r.read(3)
	if !ok {
		readErr()
		return imageFiletypeUnknown
	}
	switch {
	case bytes.Equal(tmp, sigGIF):
		return imageFiletypeGIF
	case bytes.Equal(tmp, sigJPG):
		return imageFiletypeJPEG
	case bytes.HasPrefix(sigPNG, tmp):
		rest, ok := r.read(5)
		if !ok {
			readErr()
			return imageFiletypeUnknown
		}
		if bytes.Equal(append(append([]byte{}, tmp...), rest...), sigPNG) {
			return imageFiletypePNG
		}
		return imageFiletypeUnknown
	case bytes.Equal(tmp, sigSWF):
		return imageFiletypeSWF
	case bytes.Equal(tmp, sigSWC):
		return imageFiletypeSWC
	case bytes.HasPrefix(sigPSD, tmp):
		return imageFiletypePSD
	case tmp[0] == sigBMP[0] && tmp[1] == sigBMP[1]:
		return imageFiletypeBMP
	case bytes.Equal(tmp, sigJPC):
		return imageFiletypeJPC
	case bytes.Equal(tmp, sigRIFF[:3]):
		rest, ok := r.read(9)
		if !ok {
			readErr()
			return imageFiletypeUnknown
		}
		all := append(append([]byte{}, tmp...), rest...)
		if len(all) >= 12 && bytes.Equal(all[8:12], sigWEBP) {
			return imageFiletypeWEBP
		}
		return imageFiletypeUnknown
	}
	extra, ok := r.read(1)
	if !ok {
		readErr()
		return imageFiletypeUnknown
	}
	all := append(append([]byte{}, tmp...), extra...)
	switch {
	case bytes.Equal(all, sigTIFFII):
		return imageFiletypeTIFFII
	case bytes.Equal(all, sigTIFFMM):
		return imageFiletypeTIFFMM
	case bytes.Equal(all, sigIFF):
		return imageFiletypeIFF
	case bytes.Equal(all, sigICO):
		return imageFiletypeICO
	}
	rest, twelve := r.read(8)
	twelveOK := twelve
	if twelveOK {
		all12 := append(append([]byte{}, all...), rest...)
		if bytes.Equal(all12, sigJP2) {
			return imageFiletypeJP2
		}
	}
	if r.rewind() == false && phpGetWBMP(r, nil, true) {
		return imageFiletypeWBMP
	}
	if !twelveOK {
		readErr()
		return imageFiletypeUnknown
	}
	if phpGetXBM(r, nil) {
		return imageFiletypeXBM
	}
	return imageFiletypeUnknown
}

// --- обработчики форматов ---

func phpHandleGIF(r *imageReader) *gfxinfo {
	if r.seekDelta(3) {
		return nil
	}
	dim, ok := r.read(5)
	if !ok {
		return nil
	}
	res := &gfxinfo{}
	res.width = uint32(dim[0]) | uint32(dim[1])<<8
	res.height = uint32(dim[2]) | uint32(dim[3])<<8
	if dim[4]&0x80 != 0 {
		res.bits = (uint32(dim[4]) & 0x07) + 1
	}
	res.channels = 3
	return res
}

func phpHandlePSD(r *imageReader) *gfxinfo {
	if r.seekDelta(11) {
		return nil
	}
	dim, ok := r.read(8)
	if !ok {
		return nil
	}
	res := &gfxinfo{}
	res.height = uint32(dim[0])<<24 + uint32(dim[1])<<16 + uint32(dim[2])<<8 + uint32(dim[3])
	res.width = uint32(dim[4])<<24 + uint32(dim[5])<<16 + uint32(dim[6])<<8 + uint32(dim[7])
	return res
}

func phpHandleBMP(r *imageReader) *gfxinfo {
	if r.seekDelta(11) {
		return nil
	}
	dim, ok := r.read(16)
	if !ok {
		return nil
	}
	size := int(int32(uint32(dim[3])<<24 | uint32(dim[2])<<16 | uint32(dim[1])<<8 | uint32(dim[0])))
	switch {
	case size == 12:
		res := &gfxinfo{}
		res.width = uint32(dim[5])<<8 + uint32(dim[4])
		res.height = uint32(dim[7])<<8 + uint32(dim[6])
		res.bits = uint32(dim[11])
		return res
	case size > 12 && (size <= 64 || size == 108 || size == 124):
		res := &gfxinfo{}
		res.width = uint32(dim[7])<<24 + uint32(dim[6])<<16 + uint32(dim[5])<<8 + uint32(dim[4])
		res.height = uint32(dim[11])<<24 + uint32(dim[10])<<16 + uint32(dim[9])<<8 + uint32(dim[8])
		res.height = uint32(absInt32(int32(res.height)))
		res.bits = uint32(dim[15])<<8 + uint32(dim[14])
		return res
	}
	return nil
}

func absInt32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

func phpSWFGetBits(buf []byte, pos, count uint32) uint32 {
	var result uint32
	for loop := pos; loop < pos+count; loop++ {
		b := buf[loop/8]
		bit := (b >> (7 - (loop % 8))) & 0x01
		result += uint32(bit) << (count - (loop - pos) - 1)
	}
	return result
}

func phpHandleSWF(r *imageReader) *gfxinfo {
	if r.seekDelta(5) {
		return nil
	}
	a, ok := r.read(32)
	if !ok {
		return nil
	}
	res := &gfxinfo{}
	bits := phpSWFGetBits(a, 0, 5)
	res.width = (phpSWFGetBits(a, 5+bits, bits) - phpSWFGetBits(a, 5, bits)) / 20
	res.height = (phpSWFGetBits(a, 5+3*bits, bits) - phpSWFGetBits(a, 5+2*bits, bits)) / 20
	return res
}

func phpHandleSWC(r *imageReader) *gfxinfo {
	if r.seekDelta(5) {
		return nil
	}
	a, ok := r.read(64)
	if !ok {
		return nil
	}
	var b []byte
	zr, err := zlib.NewReader(bytes.NewReader(a))
	if err == nil {
		b, _ = io.ReadAll(zr)
		zr.Close()
	}
	if len(b) == 0 {
		// Попытка: прочитать остаток файла и распаковать целиком.
		if r.seekAbs(8) {
			return nil
		}
		rest := r.data[r.pos:]
		for factor := 1; factor < 16; factor++ {
			zr, err := zlib.NewReader(bytes.NewReader(rest))
			if err != nil {
				return nil
			}
			out, _ := io.ReadAll(zr)
			zr.Close()
			if len(out) > 0 {
				b = out
				break
			}
			break
		}
	}
	if len(b) < 1 {
		return nil
	}
	res := &gfxinfo{}
	bits := phpSWFGetBits(b, 0, 5)
	res.width = (phpSWFGetBits(b, 5+bits, bits) - phpSWFGetBits(b, 5, bits)) / 20
	res.height = (phpSWFGetBits(b, 5+3*bits, bits) - phpSWFGetBits(b, 5+2*bits, bits)) / 20
	return res
}

func phpHandlePNG(r *imageReader) *gfxinfo {
	if r.seekDelta(8) {
		return nil
	}
	dim, ok := r.read(9)
	if !ok {
		return nil
	}
	res := &gfxinfo{}
	res.width = uint32(dim[0])<<24 + uint32(dim[1])<<16 + uint32(dim[2])<<8 + uint32(dim[3])
	res.height = uint32(dim[4])<<24 + uint32(dim[5])<<16 + uint32(dim[6])<<8 + uint32(dim[7])
	res.bits = uint32(dim[8])
	return res
}

const (
	mSOF0  = 0xC0
	mSOF1  = 0xC1
	mSOF2  = 0xC2
	mSOF3  = 0xC3
	mSOF5  = 0xC5
	mSOF6  = 0xC6
	mSOF7  = 0xC7
	mSOF9  = 0xC9
	mSOF10 = 0xCA
	mSOF11 = 0xCB
	mSOF13 = 0xCD
	mSOF14 = 0xCE
	mSOF15 = 0xCF
	mEOI   = 0xD9
	mSOS   = 0xDA
	mAPP0  = 0xE0
	mAPP15 = 0xEF
)

func isSOF(marker int) bool {
	switch marker {
	case mSOF0, mSOF1, mSOF2, mSOF3, mSOF5, mSOF6, mSOF7,
		mSOF9, mSOF10, mSOF11, mSOF13, mSOF14, mSOF15:
		return true
	}
	return false
}

// phpNextMarker — php_next_marker.
func phpNextMarker(ctx *vm.Context, r *imageReader, lastMarker int, ffRead bool, fnName string) int {
	marker := 0
	if !ffRead {
		extraneous := 0
		for {
			marker = r.getc()
			if marker != 0xFF {
				if marker == -1 {
					return mEOI
				}
				extraneous++
				continue
			}
			break
		}
		if extraneous > 0 {
			ctx.VM().Warn(fnName + "(): Corrupt JPEG data: " + strconv.Itoa(extraneous) + " extraneous bytes before marker")
		}
	}
	a := 1
	for {
		marker = r.getc()
		if marker == -1 {
			return mEOI
		}
		a++
		if marker != 0xFF {
			break
		}
	}
	if a < 2 {
		return mEOI
	}
	return marker
}

func phpSkipVariable(r *imageReader) bool {
	length := int(read2(r))
	if length < 2 {
		return false
	}
	r.seekDelta(length - 2)
	return true
}

func phpReadApp(ctx *vm.Context, r *imageReader, marker int, info *vm.Array) bool {
	length := int(read2(r))
	if length < 2 {
		return false
	}
	length -= 2
	buf, ok := r.read(length)
	if !ok {
		return false
	}
	name := "APP" + strconv.Itoa(marker-mAPP0)
	if _, exists := info.Get(vm.MakeStrKey(name)); !exists {
		info.Set(vm.MakeStrKey(name), vm.MakeString(string(buf)))
	}
	return true
}

func phpHandleJPEG(ctx *vm.Context, r *imageReader, info *vm.Array, fnName string) *gfxinfo {
	var result *gfxinfo
	marker := 0xFFD8
	ffRead := true
	for {
		marker = phpNextMarker(ctx, r, marker, ffRead, fnName)
		ffRead = false
		switch {
		case isSOF(marker):
			if result == nil {
				result = &gfxinfo{}
				length := int(read2(r))
				result.bits = uint32(r.getc())
				result.height = uint32(read2(r))
				result.width = uint32(read2(r))
				result.channels = uint32(r.getc())
				if info == nil || length < 8 {
					return result
				}
				if r.seekDelta(length - 8) {
					return result
				}
			} else if !phpSkipVariable(r) {
				return result
			}
		case marker >= mAPP0 && marker <= mAPP15:
			if info != nil {
				if !phpReadApp(ctx, r, marker, info) {
					return result
				}
			} else if !phpSkipVariable(r) {
				return result
			}
		case marker == mSOS || marker == mEOI:
			return result
		default:
			if !phpSkipVariable(r) {
				return result
			}
		}
	}
}

func phpHandleJPC(r *imageReader) *gfxinfo {
	first := r.getc()
	if first != 0x51 { // SIZ
		return nil
	}
	res := &gfxinfo{}
	read2(r) // Lsiz
	read2(r) // Rsiz
	res.width = read4(r)
	res.height = read4(r)
	if r.seekDelta(24) {
		return nil
	}
	res.channels = uint32(read2(r))
	if (res.channels == 0 && r.pos >= len(r.data)) || res.channels > 256 {
		return nil
	}
	var highest uint32
	for i := uint32(0); i < res.channels; i++ {
		bitDepth := uint32(r.getc()) + 1
		if bitDepth > highest {
			highest = bitDepth
		}
		r.getc()
		r.getc()
	}
	res.bits = highest
	return res
}

func phpHandleJP2(r *imageReader) *gfxinfo {
	jp2c := []byte{0x6a, 0x70, 0x32, 0x63}
	for {
		boxLength := read4(r)
		t, ok := r.read(4)
		if !ok {
			break
		}
		if boxLength == 1 {
			return nil
		}
		if bytes.Equal(t, jp2c) {
			r.seekDelta(3)
			return phpHandleJPC(r)
		}
		if int32(boxLength) <= 0 {
			break
		}
		if r.seekDelta(int(boxLength) - 8) {
			break
		}
	}
	return nil
}

var tiffBytesPerFormat = []int{0, 1, 1, 2, 4, 8, 1, 1, 2, 4, 8, 4, 8}

func ifdGet16u(b []byte, motorola bool) int {
	if motorola {
		return int(b[0])<<8 | int(b[1])
	}
	return int(b[1])<<8 | int(b[0])
}

func ifdGet16s(b []byte, motorola bool) int16 { return int16(ifdGet16u(b, motorola)) }

func ifdGet32s(b []byte, motorola bool) int32 {
	if motorola {
		return int32(uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3]))
	}
	return int32(uint32(b[3])<<24 | uint32(b[2])<<16 | uint32(b[1])<<8 | uint32(b[0]))
}

func phpHandleTIFF(r *imageReader, motorola bool) *gfxinfo {
	ifdPtr, ok := r.read(4)
	if !ok {
		return nil
	}
	ifdAddr := int(uint32(ifdGet32s(ifdPtr, motorola)))
	if r.seekDelta(ifdAddr - 8) {
		return nil
	}
	head, ok := r.read(2)
	if !ok {
		return nil
	}
	numEntries := ifdGet16u(head, motorola)
	dirSize := 2 + 12*numEntries + 4
	data, ok := r.read(dirSize - 2)
	if !ok {
		return nil
	}
	full := append(append([]byte{}, head...), data...)
	var width, height uint32
	for i := 0; i < numEntries; i++ {
		entry := full[2+i*12:]
		if len(entry) < 12 {
			break
		}
		entryTag := ifdGet16u(entry[0:], motorola)
		entryType := ifdGet16u(entry[2:], motorola)
		var entryValue uint32
		var entryValueSigned int32
		switch entryType {
		case 1, 6: // BYTE / SBYTE
			entryValue = uint32(entry[8])
		case 3: // USHORT
			entryValue = uint32(ifdGet16u(entry[8:], motorola))
		case 8: // SSHORT
			entryValue = uint32(int32(ifdGet16s(entry[8:], motorola)))
		case 4: // ULONG
			entryValue = uint32(ifdGet32s(entry[8:], motorola))
		case 9: // SLONG
			entryValueSigned = ifdGet32s(entry[8:], motorola)
			entryValue = uint32(entryValueSigned)
		default:
			continue
		}
		switch entryTag {
		case 0x0100, 0xA002: // width
			width = entryValue
		case 0x0101, 0xA003: // height
			height = entryValue
		}
	}
	if width != 0 && height != 0 {
		return &gfxinfo{width: width, height: height}
	}
	return nil
}

func phpHandleIFF(r *imageReader) *gfxinfo {
	a, ok := r.read(8)
	if !ok {
		return nil
	}
	if !bytes.Equal(a[4:8], []byte("ILBM")) && !bytes.Equal(a[4:8], []byte("PBM ")) {
		return nil
	}
	for {
		hdr, ok := r.read(8)
		if !ok {
			return nil
		}
		chunkID := int(ifdGet32s(hdr[0:], true))
		size := int(ifdGet32s(hdr[4:], true))
		if size < 0 {
			return nil
		}
		if size&1 == 1 {
			size++
		}
		if chunkID == 0x424d4844 { // BMHD
			if size < 9 {
				return nil
			}
			b, ok := r.read(9)
			if !ok {
				return nil
			}
			width := int32(ifdGet16s(b[0:], true))
			height := int32(ifdGet16s(b[2:], true))
			bits := int(b[8])
			if width > 0 && height > 0 && bits > 0 && bits < 33 {
				return &gfxinfo{width: uint32(width), height: uint32(height), bits: uint32(bits)}
			}
		} else if r.seekDelta(size) {
			return nil
		}
	}
}

func phpGetWBMP(r *imageReader, result **gfxinfo, check bool) bool {
	if r.rewind() {
		return false
	}
	if r.getc() != 0 {
		return false
	}
	for {
		i := r.getc()
		if i < 0 {
			return false
		}
		if i&0x80 == 0 {
			break
		}
	}
	width, height := 0, 0
	for {
		i := r.getc()
		if i < 0 {
			return false
		}
		width = (width << 7) | (i & 0x7f)
		if width > 2048 {
			return false
		}
		if i&0x80 == 0 {
			break
		}
	}
	for {
		i := r.getc()
		if i < 0 {
			return false
		}
		height = (height << 7) | (i & 0x7f)
		if height > 2048 {
			return false
		}
		if i&0x80 == 0 {
			break
		}
	}
	if height == 0 || width == 0 {
		return false
	}
	if !check && result != nil {
		*result = &gfxinfo{width: uint32(width), height: uint32(height)}
	}
	return true
}

func phpHandleWBMP(r *imageReader) *gfxinfo {
	var res *gfxinfo
	if !phpGetWBMP(r, &res, false) {
		return nil
	}
	return res
}

func phpGetXBM(r *imageReader, result **gfxinfo) bool {
	if result != nil {
		*result = nil
	}
	if r.rewind() {
		return false
	}
	width, height := uint32(0), uint32(0)
	for {
		fline, ok := r.gets()
		if !ok {
			break
		}
		fields := strings.Fields(fline)
		if len(fields) >= 3 && fields[0] == "#define" {
			iname := fields[1]
			value, err := strconv.Atoi(fields[2])
			if err != nil {
				continue
			}
			typeName := iname
			if i := strings.LastIndexByte(iname, '_'); i >= 0 {
				typeName = iname[i+1:]
			}
			switch typeName {
			case "width":
				width = uint32(value)
				if height != 0 {
					goto done
				}
			case "height":
				height = uint32(value)
				if width != 0 {
					goto done
				}
			}
		}
	}
done:
	if width != 0 && height != 0 {
		if result != nil {
			*result = &gfxinfo{width: width, height: height}
		}
		return true
	}
	return false
}

func phpHandleXBM(r *imageReader) *gfxinfo {
	var res *gfxinfo
	phpGetXBM(r, &res)
	return res
}

func phpHandleICO(r *imageReader) *gfxinfo {
	dim, ok := r.read(2)
	if !ok {
		return nil
	}
	numIcons := int(dim[1])<<8 + int(dim[0])
	if numIcons < 1 || numIcons > 255 {
		return nil
	}
	res := &gfxinfo{}
	for numIcons > 0 {
		e, ok := r.read(16)
		if !ok {
			break
		}
		if int(e[7])<<8+int(e[6]) >= int(res.bits) {
			res.width = uint32(e[0])
			res.height = uint32(e[1])
			res.bits = uint32(e[7])<<8 + uint32(e[6])
		}
		numIcons--
	}
	if res.width == 0 {
		res.width = 256
	}
	if res.height == 0 {
		res.height = 256
	}
	return res
}

func phpHandleWEBP(r *imageReader) *gfxinfo {
	buf, ok := r.read(18)
	if !ok {
		return nil
	}
	if !bytes.Equal(buf[0:3], []byte{'V', 'P', '8'}) {
		return nil
	}
	format := buf[3]
	if format != ' ' && format != 'L' && format != 'X' {
		return nil
	}
	res := &gfxinfo{}
	switch format {
	case ' ':
		res.width = uint32(buf[14]) + (uint32(buf[15])&0x3F)<<8
		res.height = uint32(buf[16]) + (uint32(buf[17])&0x3F)<<8
	case 'L':
		res.width = uint32(buf[9]) + (uint32(buf[10])&0x3F)<<8 + 1
		res.height = uint32(buf[10]>>6) + uint32(buf[11])<<2 + (uint32(buf[12])&0xF)<<10 + 1
	case 'X':
		res.width = uint32(buf[12]) + uint32(buf[13])<<8 + uint32(buf[14])<<16 + 1
		res.height = uint32(buf[15]) + uint32(buf[16])<<8 + uint32(buf[17])<<16 + 1
	}
	res.bits = 8
	return res
}

// imageSizeFromData разбирает изображение и возвращает (result, itype, info).
func imageSizeFromData(ctx *vm.Context, data []byte, name string, wantInfo bool, fnName string) (*gfxinfo, int, *vm.Array) {
	r := &imageReader{data: data}
	var info *vm.Array
	if wantInfo {
		info = vm.NewArray()
	}
	var handleInfo *vm.Array
	if wantInfo {
		handleInfo = info
	}
	itype := phpGetImageType(ctx, r, name, fnName)
	var result *gfxinfo
	switch itype {
	case imageFiletypeGIF:
		result = phpHandleGIF(r)
	case imageFiletypeJPEG:
		result = phpHandleJPEG(ctx, r, handleInfo, fnName)
	case imageFiletypePNG:
		result = phpHandlePNG(r)
	case imageFiletypeSWF:
		result = phpHandleSWF(r)
	case imageFiletypeSWC:
		result = phpHandleSWC(r)
	case imageFiletypePSD:
		result = phpHandlePSD(r)
	case imageFiletypeBMP:
		result = phpHandleBMP(r)
	case imageFiletypeTIFFII:
		result = phpHandleTIFF(r, false)
	case imageFiletypeTIFFMM:
		result = phpHandleTIFF(r, true)
	case imageFiletypeJPC:
		result = phpHandleJPC(r)
	case imageFiletypeJP2:
		result = phpHandleJP2(r)
	case imageFiletypeIFF:
		result = phpHandleIFF(r)
	case imageFiletypeWBMP:
		result = phpHandleWBMP(r)
	case imageFiletypeXBM:
		result = phpHandleXBM(r)
	case imageFiletypeICO:
		result = phpHandleICO(r)
	case imageFiletypeWEBP:
		result = phpHandleWEBP(r)
	}
	return result, itype, info
}

// buildImageSizeResult собирает массив результата getimagesize.
func buildImageSizeResult(res *gfxinfo, itype int) *vm.Array {
	a := vm.NewArray()
	a.Append(vm.MakeInt(int64(res.width)))
	a.Append(vm.MakeInt(int64(res.height)))
	a.Append(vm.MakeInt(int64(itype)))
	a.Append(vm.MakeString("width=\"" + strconv.FormatUint(uint64(res.width), 10) + "\" height=\"" +
		strconv.FormatUint(uint64(res.height), 10) + "\""))
	if res.bits != 0 {
		a.Set(vm.MakeStrKey("bits"), vm.MakeInt(int64(res.bits)))
	}
	if res.channels != 0 {
		a.Set(vm.MakeStrKey("channels"), vm.MakeInt(int64(res.channels)))
	}
	a.Set(vm.MakeStrKey("mime"), vm.MakeString(imageTypeToMimeType(itype)))
	return a
}

// imageSizeInfoResult готовит массив $image_info (2-й аргумент).
func imageSizeInfoResult(res *gfxinfo, info *vm.Array) vm.Value {
	if info == nil {
		return vm.MakeNull()
	}
	return vm.MakeArray(info)
}

func getimagesizeFromPathFn(ctx *vm.Context) vm.Value {
	name, ok := strParam(ctx, 0, "getimagesize", "filename")
	if !ok {
		return *new(vm.Value)
	}
	if strings.IndexByte(name, 0) >= 0 {
		ctx.Throw(vm.NewThrowable("ValueError", "getimagesize(): Argument #1 ($filename) must not contain any null bytes"))
		return *new(vm.Value)
	}
	wantInfo := refArgIsUsable(ctx, 1)
	if wantInfo {
		netWriteRef(ctx, 1, vm.MakeArray(vm.NewArray()))
	}
	data, err := os.ReadFile(name)
	if err != nil {
		ctx.VM().Warn("getimagesize(" + name + "): Failed to open stream: " + sysErrText(err))
		return vm.Bool(false)
	}
	res, itype, info := imageSizeFromData(ctx, data, name, wantInfo, "getimagesize")
	if res == nil {
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.Bool(false)
	}
	if wantInfo {
		netWriteRef(ctx, 1, imageSizeInfoResult(res, info))
	}
	return vm.MakeArray(buildImageSizeResult(res, itype))
}

func getimagesizefromstringFn(ctx *vm.Context) vm.Value {
	data, ok := strParam(ctx, 0, "getimagesizefromstring", "string")
	if !ok {
		return *new(vm.Value)
	}
	wantInfo := refArgIsUsable(ctx, 1)
	if wantInfo {
		netWriteRef(ctx, 1, vm.MakeArray(vm.NewArray()))
	}
	res, itype, info := imageSizeFromData(ctx, []byte(data), "", wantInfo, "getimagesizefromstring")
	if res == nil {
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.Bool(false)
	}
	if wantInfo {
		netWriteRef(ctx, 1, imageSizeInfoResult(res, info))
	}
	return vm.MakeArray(buildImageSizeResult(res, itype))
}

// refArgIsUsable сообщает, что by-ref аргумент #i пригоден для записи.
// PHP заменяет значение переменной на массив $image_info независимо от её
// прежнего типа (int/string/...), поэтому тип не проверяем.
func refArgIsUsable(ctx *vm.Context, i int) bool {
	args := ctx.Args()
	if i < 0 || i >= len(args) {
		return false
	}
	s := args[i]
	return s.IsRef() && s.Ref() != nil
}

// sysErrText формирует PHP-подобный текст ошибки открытия файла.
func sysErrText(err error) string {
	if os.IsNotExist(err) {
		return "No such file or directory"
	}
	if os.IsPermission(err) {
		return "Permission denied"
	}
	return err.Error()
}

func imageTypeToMimeTypeFn(ctx *vm.Context) vm.Value {
	t := intParam(ctx, 0, "image_type_to_mime_type", "image_type")
	return vm.Str(imageTypeToMimeType(int(t)))
}

// imageTypeToExtensionFn — image_type_to_extension(): расширение по типу.
func imageTypeToExtensionFn(ctx *vm.Context) vm.Value {
	t := intParam(ctx, 0, "image_type_to_extension", "image_type")
	incDot := true
	if len(ctx.Args()) >= 2 {
		incDot = derefSlot(arg(ctx, 1)).ToBool()
	}
	var ext string
	switch int(t) {
	case imageFiletypeGIF:
		ext = ".gif"
	case imageFiletypeJPEG:
		ext = ".jpeg"
	case imageFiletypePNG:
		ext = ".png"
	case imageFiletypeSWF, imageFiletypeSWC:
		ext = ".swf"
	case imageFiletypePSD:
		ext = ".psd"
	case imageFiletypeBMP, imageFiletypeWBMP:
		ext = ".bmp"
	case imageFiletypeTIFFII, imageFiletypeTIFFMM:
		ext = ".tiff"
	case imageFiletypeIFF:
		ext = ".iff"
	case imageFiletypeJPC:
		ext = ".jpc"
	case imageFiletypeJP2:
		ext = ".jp2"
	case imageFiletypeJPX:
		ext = ".jpx"
	case imageFiletypeJB2:
		ext = ".jb2"
	case imageFiletypeXBM:
		ext = ".xbm"
	case imageFiletypeICO:
		ext = ".ico"
	case imageFiletypeWEBP:
		ext = ".webp"
	case imageFiletypeAVIF:
		ext = ".avif"
	default:
		return vm.Bool(false)
	}
	if !incDot {
		ext = ext[1:]
	}
	return vm.Str(ext)
}

// iptcParseFn — iptcparse(): разбор блока IPTC (APP13) в массив.
// Формат ключей: "<dataset>#<recnum:03d>" (порт ext/standard/iptc.c).
func iptcParseFn(ctx *vm.Context) vm.Value {
	str, ok := strParam(ctx, 0, "iptcparse", "iptc_block")
	if !ok {
		return *new(vm.Value)
	}
	buffer := []byte(str)
	inx := 0
	n := len(buffer)
	// ищем первый тег: 0x1C + (0x01|0x02)
	for inx < n {
		if buffer[inx] == 0x1c && inx+1 < n && (buffer[inx+1] == 0x01 || buffer[inx+1] == 0x02) {
			break
		}
		inx++
	}
	out := vm.NewArray()
	tagsfound := 0
	for inx < n {
		if buffer[inx] != 0x1c {
			break
		}
		inx++
		if inx+4 >= n {
			break
		}
		dataset := buffer[inx]
		inx++
		recnum := buffer[inx]
		inx++
		var length int
		if buffer[inx]&0x80 != 0 { // длинный тег
			if inx+6 >= n {
				break
			}
			length = int(buffer[inx+2])<<24 | int(buffer[inx+3])<<16 | int(buffer[inx+4])<<8 | int(buffer[inx+5])
			inx += 6
		} else {
			length = int(buffer[inx])<<8 | int(buffer[inx+1])
			inx += 2
		}
		if length > n || inx+length > n {
			break
		}
		key := strconv.Itoa(int(dataset)) + "#" + pad3(int(recnum))
		element, exists := out.Get(vm.MakeStrKey(key))
		if !exists {
			arr := vm.NewArray()
			out.Set(vm.MakeStrKey(key), vm.MakeArray(arr))
			element = vm.MakeArray(arr)
		}
		element.AsArray().Append(vm.MakeString(string(buffer[inx : inx+length])))
		inx += length
		tagsfound++
	}
	if tagsfound == 0 {
		return vm.Bool(false)
	}
	return vm.MakeArray(out)
}

func pad3(n int) string {
	s := strconv.Itoa(n)
	for len(s) < 3 {
		s = "0" + s
	}
	return s
}

// iptcEmbed собирает JPEG с новым APP13-блоком (порт iptcembed). ok=false —
// данные не JPEG или запись невозможна.
func iptcEmbed(data []byte, iptcdata string) ([]byte, bool) {
	in := &imageReader{data: data}
	out := make([]byte, 0, len(data)+len(iptcdata)+64)
	get1 := func(spool bool) int {
		c := in.getc()
		if c == -1 {
			return -1
		}
		if spool {
			out = append(out, byte(c))
		}
		return c
	}
	put1 := func(c int) { out = append(out, byte(c)) }
	skipVariable := func(spool bool) int {
		c1 := get1(spool)
		if c1 == -1 {
			return mEOI
		}
		c2 := get1(spool)
		if c2 == -1 {
			return mEOI
		}
		length := c1<<8 | c2
		length -= 2
		for length > 0 {
			if get1(spool) == -1 {
				return mEOI
			}
			length--
		}
		return 0
	}
	readRemaining := func(spool bool) int {
		for get1(spool) != -1 {
		}
		return mEOI
	}
	nextMarker := func(spool bool) int {
		c := get1(spool)
		if c == -1 {
			return mEOI
		}
		for c != 0xff {
			c = get1(spool)
			if c == -1 {
				return mEOI
			}
		}
		for {
			c = get1(false)
			if c == -1 {
				return mEOI
			}
			if c == 0xff {
				put1(c)
				continue
			}
			break
		}
		return c
	}

	if get1(false) != 0xFF {
		return nil, false
	}
	if get1(false) != 0xD8 {
		return nil, false
	}
	out = append(out, 0xFF, 0xD8)
	psheader := []byte("\xFF\xED\x00\x00Photoshop 3.0\x008BIM\x04\x04\x00\x00\x00\x00")
	iptcLen := len(iptcdata)
	if iptcLen%2 == 1 {
		iptcLen++
	}
	written := false
	done := false
	for !done {
		marker := nextMarker(true)
		if marker == mEOI {
			break
		}
		if marker != 0xED { // APP13
			put1(marker)
		}
		switch marker {
		case 0xED:
			skipVariable(false)
			readRemaining(true)
			done = true
		case mAPP0, 0xE1: // APP0/APP1
			if written {
				break
			}
			written = true
			skipVariable(true)
			hdr := make([]byte, len(psheader))
			copy(hdr, psheader)
			hdr[2] = byte((iptcLen + 28) >> 8)
			hdr[3] = byte((iptcLen + 28) & 0xff)
			out = append(out, hdr...)
			out = append(out, byte(iptcLen>>8), byte(iptcLen&0xff))
			out = append(out, []byte(iptcdata)...)
			for i := len(iptcdata); i < iptcLen; i++ {
				out = append(out, 0)
			}
		case mSOS:
			readRemaining(true)
			done = true
		default:
			skipVariable(true)
		}
	}
	return out, true
}

// iptcembedFn — iptcembed(): встраивает IPTC-данные в JPEG.
func iptcembedFn(ctx *vm.Context) vm.Value {
	iptcdata, ok := strParam(ctx, 0, "iptcembed", "iptc_data")
	if !ok {
		return *new(vm.Value)
	}
	file, ok := strParam(ctx, 1, "iptcembed", "filename")
	if !ok {
		return *new(vm.Value)
	}
	spool := 0
	if len(ctx.Args()) >= 3 {
		spool = int(derefSlot(arg(ctx, 2)).ToInt())
	}
	data, err := os.ReadFile(file)
	if err != nil {
		ctx.VM().Warn("iptcembed(): Unable to open " + file)
		return vm.Bool(false)
	}
	out, ok := iptcEmbed(data, iptcdata)
	if !ok {
		return vm.Bool(false)
	}
	if spool >= 2 {
		return vm.Bool(true)
	}
	return vm.Str(string(out))
}
