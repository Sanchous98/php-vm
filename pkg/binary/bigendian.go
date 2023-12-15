package binary

import (
	"encoding/binary"
)

type bigEndian struct {
	binary.ByteOrder
	binary.AppendByteOrder
}

func (bigEndian) ConvertUint16(b []byte, v []uint16) { convert(swap[uint16](b), v) }
func (bigEndian) ConvertUint32(b []byte, v []uint32) { convert(swap[uint32](b), v) }
func (bigEndian) ConvertUint64(b []byte, v []uint64) { convert(swap[uint64](b), v) }

var BigEndian = bigEndian{
	binary.BigEndian,
	binary.BigEndian,
}
