package binary

import (
	"encoding/binary"
)

type littleEndian struct {
	binary.ByteOrder
	binary.AppendByteOrder
}

func (littleEndian) ConvertUint16(b []byte, v []uint16) { convert(b, v) }
func (littleEndian) ConvertUint32(b []byte, v []uint32) { convert(b, v) }
func (littleEndian) ConvertUint64(b []byte, v []uint64) { convert(b, v) }

var LittleEndian = littleEndian{
	binary.LittleEndian,
	binary.LittleEndian,
}
