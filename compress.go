package whisper

import (
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"math/bits"
)

var debug bool

// Timestamp:
// 1. The block header stores the starting time stamp, t−1,
// which is aligned to a two hour window; the first time
// stamp, t0, in the block is stored as a delta from t−1 in
// 14 bits. 1
// 2. For subsequent time stamps, tn:
// (a) Calculate the delta of delta:
// D = (tn − tn−1) − (tn−1 − tn−2)
// (b) If D is zero, then store a single ‘0’ bit
// (c) If D is between [-63, 64], store ‘10’ followed by
// the value (7 bits)
// (d) If D is between [-255, 256], store ‘110’ followed by
// the value (9 bits)
// (e) if D is between [-2047, 2048], store ‘1110’ followed
// by the value (12 bits)
// (f) Otherwise store ‘1111’ followed by D using 32 bits
//
// Value:
// 1. The first value is stored with no compression
// 2. If XOR with the previous is zero (same value), store
// single ‘0’ bit
// 3. When XOR is non-zero, calculate the number of leading
// and trailing zeros in the XOR, store bit ‘1’ followed
// by either a) or b):
// 	(a) (Control bit ‘0’) If the block of meaningful bits
// 	    falls within the block of previous meaningful bits,
// 	    i.e., there are at least as many leading zeros and
// 	    as many trailing zeros as with the previous value,
// 	    use that information for the block position and
// 	    just store the meaningful XORed value.
// 	(b) (Control bit ‘1’) Store the length of the number
// 	    of leading zeros in the next 5 bits, then store the
// 	    length of the meaningful XORed value in the next
// 	    6 bits. Finally store the meaningful bits of the
// 	    XORed value.

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
		pn1val := math.Float64bits(a.cblock.pn1.value)
		pn2val := math.Float64bits(a.cblock.pn2.value)
		val := math.Float64bits(p.value)
		pxor := pn1val ^ pn2val
		xor := pn1val ^ val
		// if pval == val {
		if debug {
			fmt.Printf("value\n")
		}
		if xor == 0 {
			bw.Write(1, 0)
			if debug {
				fmt.Printf("\tsame, write 0\n")
			}
		} else {
			plz := bits.LeadingZeros64(pxor)
			lz := bits.LeadingZeros64(xor)
			ptz := bits.TrailingZeros64(pxor)
			tz := bits.TrailingZeros64(xor)
			mlen := 64 - lz - tz // meaningful block size
			if plz == lz && ptz == tz {
				bw.Write(2, 2)
				bw.WriteUint(mlen, xor>>uint64(tz))
				if debug {
					fmt.Printf("\tsame-length meaningful block: %0s\n", dumpBits(2, 2, uint64(mlen), xor>>uint(tz)))
				}
			} else {
				bw.Write(2, 3)
				bw.WriteUint(5, uint64(lz))
				bw.WriteUint(6, uint64(mlen))
				bw.WriteUint(mlen, xor>>uint64(tz))
				if debug {
					fmt.Printf("\tvaried-length meaningful block: %0s\n", dumpBits(2, 3, 5, uint64(lz), 6, uint64(mlen), uint64(mlen), xor>>uint64(tz)))
				}
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

	log.Printf("dst = %+v\n", dst)

	var pn1, pn2 *dataPoint = &p, &p

readloop:
	for {
		if br.current >= len(br.buf) {
			break
		}

		var p dataPoint

		var skip, toRead int
		switch {
		case br.Peek(1) == 0: //  0xxx
			skip = 0
			toRead = 1
		case br.Peek(2) == 2: //  10xx
			skip = 2
			toRead = 7
		case br.Peek(3) == 6: //  110x
			skip = 3
			toRead = 9
		case br.Peek(4) == 14: // 1110
			skip = 4
			toRead = 12
		case br.Peek(4) == 15: // 1111
			skip = 4
			toRead = 32
		default:
			start, end, data := br.trailingDebug()
			log.Printf("br.Peek(1) = %+v\n", br.Peek(1))
			return dst, fmt.Errorf("unknown timestamp prefix: %04b at %d, context[%d-%d] = %08b", br.Peek(4), br.current, start, end, data)
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
		case br.Peek(1) == 0: // 0x
			br.Read(1)
			p.value = pval
		case br.Peek(2) == 2: // 10
			br.Read(2)
			pn2val := pn2.value
			xor := math.Float64bits(pval) ^ math.Float64bits(pn2val)
			lz := bits.LeadingZeros64(xor)
			tz := bits.TrailingZeros64(xor)
			val := br.Read(64 - lz - tz)
			p.value = math.Float64frombits(math.Float64bits(pval) ^ (val << uint(tz)))
		case br.Peek(2) == 3: // 11
			br.Read(2)
			lz := br.Read(5)
			mlen := br.Read(6)
			p.value = math.Float64frombits(math.Float64bits(pval) ^ (br.Read(int(mlen)) << uint(64-lz-mlen)))
		}

		if br.badRead {
			break
		}

		// log.Printf("p = %+v\n", p)

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
	end = br.current + 1
	if br.current == len(br.buf) {
		end = br.current
	}
	data = br.buf[start : end+1]
	return
}

// 7 6 5 4 3 2 1 0
//
// 7 6 5 4 3 2 1 0 7 6 5 4 3 2 1 0

func (br *BitsReader) Peek(c int) byte {
	if br.current >= len(br.buf) {
		return 0
	}
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

func dumpBits(data ...uint64) string {
	var bw BitsWriter
	bw.buf = make([]byte, 16)
	bw.bitPos = 7
	// to
	for i := 0; i < len(data); i += 2 {
		// reflect.ValueOf(data[i]).Uint()
		bw.WriteUint(int(data[i]), data[i+1])
	}
	return fmt.Sprintf("%08b bit_pos(%d)", bw.buf[:bw.index+1], bw.bitPos)
}
