package whisper

import (
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"math/bits"
)

// TODO: drop ...dataPoint
func (a *archiveInfo) appendPointsToBlock(buf []byte, ps ...dataPoint) (written int, left []dataPoint) {
	// if len(e.points) == 0 {
	// 	e.header = ps[0]
	// 	ps = ps[1:]
	// }

	var bw BitsWriter
	// bw.buf = make([]byte, 7200*2)
	bw.buf = buf
	// bw.index = a.cblock.size - 1
	bw.buf[0] = a.cblock.lastByte
	bw.bitPos = a.cblock.lastByteBitPos
	defer func() {
		// a.cblock.size = bw.index + 1
		a.cblock.lastByte = bw.buf[bw.index]
		a.cblock.lastByteBitPos = int(bw.bitPos)
		a.cblock.lastByteOffset += bw.index
		written = bw.index + 1

		// write end-of-block marker if there is enough space
		bw.WriteUint(4, 0x0f)
		bw.WriteUint(32, 0)
	}()

	fmt.Println(bw.index)
	fmt.Println(a.cblock.lastByteOffset)
	fmt.Println(a.blockSize)

	// TODO: clean end-of-block maker

	for i, p := range ps {
		if p.interval == 0 {
			continue
		}

		oldBwIndex := bw.index
		oldBwBitPos := bw.bitPos

		var delta1, delta2 int
		if a.cblock.p0.interval == 0 {
			// p.points = append(p.points, p)
			// p.header = p
			a.cblock.p0 = p
			a.cblock.pn1 = p
			a.cblock.pn2 = p

			// var buf [8]byte
			// binary.BigEndian.PutUint32(buf[:], uint64(p.interval))
			// bw.Write(32, buf[:4]...)

			// binary.BigEndian.PutUint64(buf[:], uint64(p.value))
			bw.Write(PointSize*8, p.Bytes()...)
			log.Printf("bw.index = %+v\n", bw.index)
			// bw.index += PointSize
			continue
		}

		// delta1 = p.interval - a.cblock.p0.interval
		// if a.cblock.p0.interval == 0 {
		// p1 := ps[len(ps)-1]
		delta1 = p.interval - a.cblock.pn1.interval
		// }

		// if len(ps) > 2 {
		// p1 := ps[len(ps)-1]
		// p2 := ps[len(ps)-2]
		// delta2 = p1.interval - p2.interval
		// }
		delta2 = a.cblock.pn1.interval - a.cblock.pn2.interval

		delta := (delta1 - delta2) / a.secondsPerPoint
		if delta == 0 {
			bw.Write(1, 0)
		} else if -63 <= delta && delta <= 64 {
			bw.Write(2, 2)
			if delta < 0 {
				delta *= -1
				delta |= 64
			}
			bw.WriteUint(7, uint64(delta))
		} else if -255 <= delta && delta <= 256 {
			bw.Write(3, 6)
			if delta < 0 {
				delta *= -1
				delta |= 256
			}
			bw.WriteUint(9, uint64(delta))
		} else if -2047 <= delta && delta <= 2048 {
			bw.Write(4, 14)
			if delta < 0 {
				delta *= -1
				delta |= 2048
			}
			bw.WriteUint(12, uint64(delta))
		} else {
			bw.Write(4, 15)
			bw.WriteUint(32, uint64(p.interval))
		}

		// pval := float64ToUint64(ps[len(ps)-1].value)
		pval := math.Float64bits(a.cblock.pn1.value)
		val := math.Float64bits(p.value)
		if pval == val {
			bw.Write(1, 0)
		} else {
			plz := bits.LeadingZeros64(pval)
			lz := bits.LeadingZeros64(val)
			ptz := bits.TrailingZeros64(pval)
			tz := bits.TrailingZeros64(val)
			bits := 64 - lz - tz
			if plz == lz && ptz == tz {
				bw.Write(2, 2)
				bw.WriteUint(bits, val>>uint64(tz))
			} else {
				bw.Write(2, 3)
				bw.WriteUint(5, uint64(lz))
				bw.WriteUint(6, uint64(bits))
				bw.WriteUint(bits, val>>uint64(tz))
			}
		}

		// TODO: fix it
		if bw.index+a.cblock.lastByteOffset+1 > a.blockSize {
			bw.index = oldBwIndex
			bw.bitPos = oldBwBitPos
			left = ps[i:]
			break
		}

		a.cblock.pn2 = a.cblock.pn1
		a.cblock.pn1 = p
	}

	// data = bw.buf[:bw.index+1]

	return
}

type BitsWriter struct {
	buf    []byte
	index  int // index
	bitPos int // 0 indexed
}

// 7 6 5 4 3 2 1 0
//
// 7 6 5 4 3 2 1 0

// 0 0 0 0 4 3 2 1
func (bw *BitsWriter) WriteUint(lenb int, data uint64) {
	buf := make([]byte, 8)
	switch {
	case lenb <= 8:
		buf[0] = byte(data)
		fmt.Printf("-- %08b\n", byte(data))
	case lenb <= 16:
		binary.LittleEndian.PutUint16(buf, uint16(data))
	case lenb <= 32:
		binary.LittleEndian.PutUint32(buf, uint32(data))
	case lenb <= 64:
		binary.LittleEndian.PutUint64(buf, data)
	default:
		panic(fmt.Sprintf("invalid int size: %d", lenb))
	}
	// fmt.Printf("== %08b\n", buf)
	bw.Write(lenb, buf...)
}

func mask(l int) uint {
	return (1 << uint(l)) - 1
}

func (bw *BitsWriter) Write(lenb int, data ...byte) {
	for _, b := range data {
		// if lenb <= 0 || bw.index > len(bw.buf) || (bw.index == len(bw.buf)-1 && lenb > 8) {
		if lenb <= 0 || bw.index > len(bw.buf) {
			break
		}

		if bw.bitPos+1 > lenb {
			bw.buf[bw.index] |= b << uint(bw.bitPos+1-lenb)
			bw.bitPos -= lenb
			lenb = 0
			// } else if bw.bitPos+1 == lenb {
			// 	bw.buf[bw.index] |= b
			// 	bw.bitPos = 7
			// 	bw.index++
			// 	lenb = 0
		} else {
			var left int
			if lenb < 8 {
				left = lenb - 1 - bw.bitPos
				lenb = 0
			} else {
				left = 7 - bw.bitPos
				lenb -= 8
			}
			bw.buf[bw.index] |= b >> uint(left)

			if bw.index == len(bw.buf)-1 {
				break
			}
			bw.index++
			// log.Printf("mask(left) = %0x\n", mask(left))
			bw.buf[bw.index] |= (b & byte(mask(left))) << uint(8-left)
			bw.bitPos = 7 - left
		}
	}
}

func (a *archiveInfo) readFromBlock(buf []byte, dst []dataPoint, start, end int) ([]dataPoint, error) {
	// var ps []dataPoint
	// var p dataPoint
	// p.interval = int(binary.BigEndian.Uint32(buf))
	// buf = buf[4:]
	// p.value = math.Float64frombits(binary.BigEndian.Uint64(buf))
	// buf = buf[8:]

	var br BitsReader
	br.buf = buf // [PointSize:]
	br.bitPos = 7
	br.Read(PointSize * 8)

	p := unpackDataPoint(buf)
	// ps[0] = &p
	if start <= p.interval && p.interval <= end {
		dst = append(dst, p)
	}

	var pn1, pn2 *dataPoint = &p, &p

readloop:
	for {
		if br.current >= len(br.buf) {
			break
		}

		var p dataPoint

		var skip, toRead int
		switch {
		case br.Peek(1) == 0:
			skip = 0
			toRead = 1
		case br.Peek(2) == 2:
			skip = 2
			toRead = 7
		case br.Peek(3) == 6:
			skip = 3
			toRead = 9
		case br.Peek(4) == 14:
			skip = 4
			toRead = 12
		case br.Peek(4) == 15:
			skip = 4
			toRead = 32
		default:
			start, end, data := br.trailingDebug()
			return dst, fmt.Errorf("unknown timestamp prefix: %04b around buf[%d-%d] = %08b", br.Peek(4), start, end, data)
		}

		br.Read(skip)
		delta := int(br.Read(toRead))
		switch toRead {
		case 0:
			// p.interval = ps[len(ps)-1].interval
			break readloop
		case 32:
			if delta == 0 {
				break readloop
			}
			p.interval = delta
		default:
			// TODO: incorrect
			if skip > 0 && delta&(1<<uint(toRead-1)) > 0 { // POC: toRead-1
				delta &= (1 << uint(toRead-1)) - 1
				delta *= -1
			}
			delta *= a.secondsPerPoint
			// p.interval = ps[len(ps)-1].interval + delta
			p.interval = 2*pn1.interval + delta - pn2.interval
		}

		// pval := ps[len(ps)-1].value
		pval := pn1.value
		switch {
		case br.Peek(1) == 0:
			br.Read(1)
			p.value = pval
		case br.Peek(2) == 1:
			br.Read(2)
			lz := bits.LeadingZeros64(math.Float64bits(pval))
			tz := bits.TrailingZeros64(math.Float64bits(pval))
			val := br.Read(64 - lz - tz)
			p.value = math.Float64frombits(val << uint(tz))
		case br.Peek(2) == 3:
			br.Read(2)
			lz := br.Read(5)
			mlen := br.Read(6)
			p.value = math.Float64frombits(br.Read(int(mlen)) << uint(64-lz-mlen))
		}

		if br.badRead {
			break
		}

		pn2 = pn1
		pn1 = &p

		// ps = append(ps, &p)
		if start <= p.interval && p.interval <= end {
			dst = append(dst, p)
		}
	}

	return dst, nil
}

type BitsReader struct {
	buf     []byte
	current int
	bitPos  int // 0 indexed
	badRead bool
}

func (br *BitsReader) trailingDebug() (start, end int, data []byte) {
	start = br.current - 1
	if br.current == 0 {
		start = 0
	}
	end = br.current + 2
	if br.current == len(br.buf) {
		end = br.current + 1
	}
	data = br.buf[start:end]
	return
}

// 7 6 5 4 3 2 1 0
//
// 7 6 5 4 3 2 1 0 7 6 5 4 3 2 1 0

func (br *BitsReader) Peek(c int) byte {
	if br.bitPos >= c {
		return br.buf[br.current] & (1<<uint(c) - 1)
	}
	if br.current+1 >= len(br.buf) {
		return 0
	}
	var b byte
	left := c - br.bitPos - 1
	b = (br.buf[br.current] & (1<<uint(br.bitPos+1) - 1)) << uint(left)
	b |= br.buf[br.current+1] >> uint(8-left)
	return b
}

// func (br *BitsReader) ReadUint(c int) []byte {
// 	var buf []byte
// 	//
// }

func (br *BitsReader) Read(c int) uint64 {
	// var data [8]byte
	var data uint64
	// buf := br.buf[br.current:]
	// var read uint
	oldc := c
	for {
		if br.badRead = br.current >= len(br.buf); br.badRead || c <= 0 {
			// data = 0
			// data = [8]byte{}
			break
		}

		// if br.bitPos < 7 {
		if c < br.bitPos+1 {
			data <<= uint(c)
			data |= (uint64(br.buf[br.current]>>uint(br.bitPos+1-c)) & ((1 << uint(c)) - 1))
			br.bitPos -= c
			// read += uint(c)
			break
		}

		data <<= uint(br.bitPos + 1)
		data |= (uint64(br.buf[br.current] & ((1 << uint(br.bitPos+1)) - 1)))
		// read += uint(br.bitPos + 1)
		c -= br.bitPos + 1
		br.current++
		br.bitPos = 7
		continue
		// }

		// if c < 8 {
		// 	data <<= uint(c)
		// 	data |= uint64((br.buf[br.current] >> uint(br.bitPos+1-c)) & ((1 << uint(c)) - 1))
		// 	br.bitPos -= c
		// 	break
		// }

		// c -= 8
		// data <<= 8
		// data |= uint64(br.buf[br.current])
		// br.current++
	}

	if oldc > 8 {
		// data = data
		switch {
		case oldc <= 16:
			data = uint64(bits.ReverseBytes16(uint16(data)))
		case oldc <= 32:
			data = uint64(bits.ReverseBytes32(uint32(data)))
		case oldc <= 64:
			data = uint64(bits.ReverseBytes64(uint64(data)))
		}
	}
	return data
}
