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
//
// 7 6 5 4 3 2 1 0
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

		// a.blockRanges[a.cblock.index].start = a.cblock.p0.interval
		// a.blockRanges[a.cblock.index].end = a.cblock.pn1.interval

		for i, block := range a.blockRanges {
			if a.cblock.index != block.index {
				continue
			}
			a.blockRanges[i].start = a.cblock.p0.interval
			a.blockRanges[i].end = a.cblock.pn1.interval
			break
		}

		// if ps[0].interval > 1544295600 {
		// 	log.Printf("p = %+v\n", ps[0])
		// 	log.Printf("a.secondsPerPoint = %+v\n", a.secondsPerPoint)
		// 	// log.Printf("size = %+v\n", size)
		// 	// log.Printf("left = %+v\n", left)
		// }

		// write end-of-block marker if there is enough space
		bw.WriteUint(4, 0x0f)
		bw.WriteUint(32, 0)
	}()

	if debug {
		fmt.Println(bw.index)
		fmt.Println(a.cblock.lastByteOffset)
		fmt.Println(a.blockSize)
	}

	// TODO: return error if interval is not monotonically increasing

	// clean possible end-of-block maker
	bw.buf[0] &= 0xFF ^ (1<<uint(a.cblock.lastByteBitPos+1) - 1)
	bw.buf[1] = 0

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

			// log.Printf("a.cblock = %+v\n", a.cblock)

			// var buf [8]byte
			// binary.BigEndian.PutUint32(buf[:], uint64(p.interval))
			// bw.Write(32, buf[:4]...)

			// binary.BigEndian.PutUint64(buf[:], uint64(p.value))

			// bw.Write(PointSize*8, p.Bytes()...)

			// log.Printf("bw.index = %+v\n", bw.index)
			copy(buf, p.Bytes())
			bw.index += PointSize

			if debug {
				fmt.Printf("begin\n")
				fmt.Printf("%d: %f\n", p.interval, p.value)
			}

			// log.Printf("buf = %x\n", buf)

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

		if debug {
			fmt.Printf("%d %d: %v\n", i, p.interval, p.value)
		}
		// fmt.Printf("%d %d: %v\n", i, p.interval, p.value)

		// TODO: use two's complement instead to extend delta range?
		if delta == 0 {
			bw.Write(1, 0)

			if debug {
				fmt.Printf("\tbuf.index = %d/%d delta = %d: %0s\n", bw.bitPos, bw.index, delta, dumpBits(1, 0))
			}

			a.stats.interval.len1++
		} else if -63 < delta && delta < 64 {
			bw.Write(2, 2)
			if delta < 0 {
				delta *= -1
				delta |= 64
			}
			bw.WriteUint(7, uint64(delta))

			if debug {
				fmt.Printf("\tbuf.index = %d/%d delta = %d: %0s\n", bw.bitPos, bw.index, delta, dumpBits(2, 2, 7, uint64(delta)))
			}

			a.stats.interval.len9++
		} else if -255 < delta && delta < 256 {
			bw.Write(3, 6)
			if delta < 0 {
				delta *= -1
				delta |= 256
			}
			bw.WriteUint(9, uint64(delta))

			if debug {
				fmt.Printf("\tbuf.index = %d/%d delta = %d: %0s\n", bw.bitPos, bw.index, delta, dumpBits(3, 6, 9, uint64(delta)))
			}

			a.stats.interval.len12++
		} else if -2047 < delta && delta < 2048 {
			bw.Write(4, 14)
			if delta < 0 {
				delta *= -1
				delta |= 2048
			}
			bw.WriteUint(12, uint64(delta))

			if debug {
				fmt.Printf("\tbuf.index = %d/%d delta = %d: %0s\n", bw.bitPos, bw.index, delta, dumpBits(4, 14, 12, uint64(delta)))
			}

			a.stats.interval.len16++
		} else {
			bw.Write(4, 15)
			bw.WriteUint(32, uint64(p.interval))

			if debug {
				fmt.Printf("\tbuf.index = %d/%d delta = %d: %0s\n", bw.bitPos, bw.index, delta, dumpBits(4, 15, 32, uint64(delta)))
			}

			a.stats.interval.len36++
		}

		// pval := float64ToUint64(ps[len(ps)-1].value)
		pn1val := math.Float64bits(a.cblock.pn1.value)
		pn2val := math.Float64bits(a.cblock.pn2.value)
		val := math.Float64bits(p.value)
		pxor := pn1val ^ pn2val
		xor := pn1val ^ val
		// if pval == val {

		if debug {
			fmt.Printf("  %v %016x\n", a.cblock.pn2.value, pn2val)
			fmt.Printf("  %v %016x\n", a.cblock.pn1.value, pn1val)
			fmt.Printf("  %v %016x\n", p.value, val)
			fmt.Printf("  pxor: %016x (%064b)\n  xor:  %016x (%064b)\n", pxor, pxor, xor, xor)
		}

		if xor == 0 {
			bw.Write(1, 0)
			if debug {
				fmt.Printf("\tsame, write 0\n")
			}

			a.stats.value.same++
		} else {
			plz := bits.LeadingZeros64(pxor)
			lz := bits.LeadingZeros64(xor)
			ptz := bits.TrailingZeros64(pxor)
			tz := bits.TrailingZeros64(xor)
			if plz <= lz && ptz <= tz {
				mlen := 64 - plz - ptz // meaningful block size
				bw.Write(2, 2)
				bw.WriteUint(mlen, xor>>uint64(ptz))
				if debug {
					// fmt.Printf("mlen = %d %b\n", mlen, xor>>uint64(ptz))
					fmt.Printf("\tsame-length meaningful block: %0s\n", dumpBits(2, 2, uint64(mlen), xor>>uint(ptz)))
				}

				a.stats.value.sameLen++
			} else {
				// TODO: handle if lz >= 1<<5
				if lz >= 1<<5 {
					lz = 31 // 11111
				}
				mlen := 64 - lz - tz // meaningful block size
				wmlen := mlen

				if mlen == 64 {
					mlen = 63
				} else if mlen == 63 {
					wmlen = 64
				} else {
					xor >>= uint64(tz)
				}

				if debug {
					log.Printf("lz = %+v\n", lz)
					log.Printf("mlen = %+v\n", mlen)
					log.Printf("xor mblock = %08b\n", xor)
				}

				bw.Write(2, 3)
				bw.WriteUint(5, uint64(lz))
				bw.WriteUint(6, uint64(mlen))
				bw.WriteUint(wmlen, xor)
				if debug {
					fmt.Printf("\tvaried-length meaningful block: %0s\n", dumpBits(2, 3, 5, uint64(lz), 6, uint64(mlen), uint64(wmlen), xor))
				}

				a.stats.value.variedLen++
			}
		}

		// TODO: fix it
		if bw.isFull() || bw.index+a.cblock.lastByteOffset+1 >= a.blockOffset(a.cblock.index)+a.blockSize {
			// fmt.Println("")
			// fmt.Println("")
			// log.Printf("a.secondsPerPoint = %+v\n", a.secondsPerPoint)
			// log.Printf("bw.isFull() = %+v\n", bw.isFull())
			// log.Printf("bw.index+a.cblock.lastByteOffset+1 = %+v\n", bw.index+a.cblock.lastByteOffset+1)
			// log.Printf("a.blockOffset(a.cblock.index)+a.blockSize = %+v\n", a.blockOffset(a.cblock.index)+a.blockSize)

			// log.Printf("bw.index = %+v\n", bw.index)
			// log.Printf("a.cblock.lastByteOffset = %+v\n", a.cblock.lastByteOffset)
			// log.Printf("a.cblock.index = %+v\n", a.cblock.index)
			// log.Printf("a.blockSize = %+v\n", a.blockSize)

			// log.Printf("p = %+v\n", p)
			bw.index = oldBwIndex
			bw.bitPos = oldBwBitPos
			left = ps[i:]
			break
		}

		// if p.interval > 1517858880 {
		// 	log.Printf("%d dps = %+v\n", a.secondsPerPoint, p)
		// }

		// log.Printf("a.cblock = %+v\n", a.cblock)
		a.cblock.pn2 = a.cblock.pn1
		a.cblock.pn1 = p

		if debug {
			start := bw.index - 8
			end := bw.index + 8
			if start < 0 {
				start = 0
			}
			if end > len(bw.buf) {
				end = len(bw.buf) - 1
			}
			fmt.Printf("%d/%d/%d: %08b\n", bw.index, start, end, bw.buf[start:end])
		}
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
		// fmt.Printf("-- %08b\n", byte(data))
	case lenb <= 16:
		binary.LittleEndian.PutUint16(buf, uint16(data))
	case lenb <= 32:
		binary.LittleEndian.PutUint32(buf, uint32(data))
	case lenb <= 64:
		binary.LittleEndian.PutUint64(buf, data)
	default:
		panic(fmt.Sprintf("invalid int size: %d", lenb))
	}

	// if debug {
	// 	fmt.Printf("== %08b\n", buf)
	// 	log.Printf("lenb = %+v\n", lenb)
	// 	fmt.Printf("%064b\n", data)
	// }

	// fmt.Printf("\ndata = %064b\n", data)

	// if lenb <= 8 {
	// 	buf[0] = byte(data)
	// } else {
	// 	data = bits.Reverse64(data)
	// 	binary.BigEndian.PutUint64(buf, data)
	// }

	// fmt.Printf("== %08b\n", buf)
	// log.Printf("lenb = %+v\n", lenb)
	// fmt.Printf("rdata = %064b\n", data)

	bw.Write(lenb, buf...)
}

func (bw *BitsWriter) isFull() bool {
	return bw.index+1 >= len(bw.buf)
}

func mask(l int) uint {
	return (1 << uint(l)) - 1
}

func (bw *BitsWriter) Write(lenb int, data ...byte) {
	index := bw.index
	end := bw.index + 5
	if debug && false {
		if end >= len(bw.buf) {
			end = len(bw.buf) - 1
		}
		log.Printf("bw.bitPos = %+v\n", bw.bitPos)
		log.Printf("bw.buf = %08b\n", bw.buf[bw.index:end])
	}

	for _, b := range data {
		// if lenb <= 0 || bw.index > len(bw.buf) || (bw.index == len(bw.buf)-1 && lenb > 8) {
		if lenb <= 0 || bw.isFull() {
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
	if debug && false {
		log.Printf("bw.buf = %08b\n", bw.buf[index:end])
	}
}

var debugprint bool

func Debug(b bool) {
	debug = b
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
	// br.Read(PointSize * 8)
	br.current = PointSize

	p := unpackDataPoint(buf)
	// ps[0] = &p

	// log.Printf("start <= p.interval && p.interval <= end = %+v\n", start <= p.interval && p.interval <= end)
	// log.Printf("p.interval = %+v\n", p.interval)
	// fmt.Println("start =", start)
	// fmt.Println("end =", end)
	// log.Printf("start <= p.interval = %+v\n", start <= p.interval)
	// log.Printf("p.interval <= end = %+v\n", p.interval <= end)

	if start <= p.interval && p.interval <= end {
		dst = append(dst, p)
	}

	// log.Printf("p = %+v\n", p)

	// log.Printf("dst = %+v\n", dst)

	var pn1, pn2 *dataPoint = &p, &p

readloop:
	for {
		if br.current >= len(br.buf) {
			// log.Printf("0 = %+v\n", 1)
			break
		}

		var p dataPoint

		if debug {
			end := br.current + 8
			if end >= len(br.buf) {
				end = len(br.buf) - 1
			}
			fmt.Printf("new point %d:\n  br.index = %d/%d br.bitPos = %d byte = %08b peek(1) = %08b peek(2) = %08b peek(3) = %08b peek(4) = %08b buf[%d:%d] = %08b\n", len(dst), br.current, len(br.buf), br.bitPos, br.buf[br.current], br.Peek(1), br.Peek(2), br.Peek(3), br.Peek(4), br.current, end, br.buf[br.current:end])
		}

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
			// log.Printf("br.Peek(1) = %+v\n", br.Peek(1))
			return dst, fmt.Errorf("unknown timestamp prefix: %04b at %d, context[%d-%d] = %08b", br.Peek(4), br.current, start, end, data)
		}

		br.Read(skip)
		delta := int(br.Read(toRead))

		if debug {
			// dumpBits(uint64(skip), uint64(skipdata), uint64(toRead), uint64(delta)),
			fmt.Printf("\tskip = %d toRead = %d delta = %d\n", skip, toRead, delta)
		}

		switch toRead {
		case 0:
			// p.interval = ps[len(ps)-1].interval
			if debug {
				fmt.Println("\tended by 0 bits to read")
			}
			// log.Printf("2 = %+v\n", 3)
			break readloop
		case 32:
			if delta == 0 {
				// log.Printf("4 = %+v\n", 5)
				break readloop
			}
			p.interval = delta

			if debug {
				fmt.Printf("\tfull interval read: %d\n", delta)
			}
		default:
			// TODO: incorrect
			if skip > 0 && delta&(1<<uint(toRead-1)) > 0 { // POC: toRead-1
				delta &= (1 << uint(toRead-1)) - 1
				delta *= -1
			}
			delta *= a.secondsPerPoint
			// p.interval = ps[len(ps)-1].interval + delta
			p.interval = 2*pn1.interval + delta - pn2.interval

			if debug {
				fmt.Printf("\tp.interval = 2*%d + %d - %d = %d\n", pn1.interval, delta, pn2.interval, p.interval)
			}
		}

		if debug {
			fmt.Printf("  br.index = %d/%d br.bitPos = %d byte = %08b peek(1) = %08b peek(2) = %08b\n", br.current, len(br.buf), br.bitPos, br.buf[br.current], br.Peek(1), br.Peek(2))
		}

		switch {
		case br.Peek(1) == 0: // 0x
			br.Read(1)
			p.value = pn1.value

			if debug {
				fmt.Printf("\tsame as previous value %016x (%f)\n", math.Float64bits(pn1.value), p.value)
			}
		case br.Peek(2) == 2: // 10
			br.Read(2)
			xor := math.Float64bits(pn1.value) ^ math.Float64bits(pn2.value)
			lz := bits.LeadingZeros64(xor)
			tz := bits.TrailingZeros64(xor)
			val := br.Read(64 - lz - tz)
			p.value = math.Float64frombits(math.Float64bits(pn1.value) ^ (val << uint(tz)))

			if debug {
				fmt.Printf("\tsame-length meaningful block\n")
				fmt.Printf("\txor: %016x val: %016x (%f)\n", val<<uint(tz), math.Float64bits(p.value), p.value)
			}
		case br.Peek(2) == 3: // 11
			br.Read(2)
			lz := br.Read(5)
			mlen := br.Read(6)
			rmlen := mlen
			if mlen == 63 {
				rmlen = 64
			}
			xor := br.Read(int(rmlen))
			// log.Printf("lz = %08b\n", lz)
			// log.Printf("mlen = %08b %d\n", mlen, mlen)
			// log.Printf("xor = %08b\n", xor)
			if mlen < 63 {
				xor <<= uint(64 - lz - mlen)
			}
			p.value = math.Float64frombits(math.Float64bits(pn1.value) ^ xor)

			if debug {
				fmt.Printf("\tvaried-length meaningful block\n")
				fmt.Printf("\txor: %016x mlen: %d val: %016x (%f)\n", xor, mlen, math.Float64bits(p.value), p.value)
			}
		}

		// if debug {
		// 	fmt.Println(a)
		// }

		// if debugprint {
		// 	log.Printf("xxp = %+v\n", p)
		// }

		if br.badRead {
			// log.Printf("6 = %+v\n", 7)
			break
		}

		// log.Printf("p = %+v\n", p)

		pn2 = pn1
		pn1 = &p

		// ps = append(ps, &p)
		if start <= p.interval && p.interval <= end {
			dst = append(dst, p)
		}
		if p.interval >= end {
			// log.Printf("8 = %+v\n", 9)
			break
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
	if end >= len(br.buf) {
		end = len(br.buf)
	}
	data = br.buf[start:end]
	return
}

// 7 6 5 4 3 2 1 0
//
// 7 6 5 4 3 2 1 0 7 6 5 4 3 2 1 0

func (br *BitsReader) Peek(c int) byte {
	if br.current >= len(br.buf) {
		return 0
	}
	if br.bitPos+1 >= c {
		return (br.buf[br.current] & (1<<uint(br.bitPos+1) - 1)) >> uint(br.bitPos+1-c)
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
	if c > 64 {
		panic("BitsReader can't read more than 64 bits")
	}
	// var data [8]byte
	var data uint64
	// buf := br.buf[br.current:]
	// var read uint
	oldc := c
	// var written uint
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

	// // log.Printf("c = %d data = %064b\n", oldc, data)
	// // 7 6 5 4 3 2 1 0 7 6 5 4 3 2 1 0 7 6 5 4 3 2 1 0
	// // data = data
	switch {
	case oldc <= 8:
	case oldc <= 16:
		// // data = uint64(bits.ReverseBytes16(uint16(data)))
		// return ((data & (1<<uint(oldc-8) - 1)) << 8) |
		// 	((data >> uint(oldc-8)) & 0xFF)
		diff := oldc - 8
		return ((data >> uint(diff)) & 0xFF) |
			(data&(1<<uint(diff)-1))<<8
	case oldc <= 24:
		diff := oldc - 16
		return ((data >> uint(diff+8)) & 0xFF) |
			(((data >> uint(diff)) & 0xFF) << 8) |
			(data&(1<<uint(diff)-1))<<16
	case oldc <= 32:
		// data = uint64(bits.ReverseBytes32(uint32(data)))
		diff := oldc - 24
		return ((data >> uint(diff+16)) & 0xFF) |
			(((data >> uint(diff+8)) & 0xFF) << 8) |
			(((data >> uint(diff)) & 0xFF) << 16) |
			(data&(1<<uint(diff)-1))<<24
	case oldc <= 40:
		diff := oldc - 32
		return ((data >> uint(diff+24)) & 0xFF) |
			(((data >> uint(diff+16)) & 0xFF) << 8) |
			(((data >> uint(diff+8)) & 0xFF) << 16) |
			(((data >> uint(diff)) & 0xFF) << 24) |
			(data&(1<<uint(diff)-1))<<32
	case oldc <= 48:
		diff := oldc - 40
		return ((data >> uint(diff+32)) & 0xFF) |
			(((data >> uint(diff+24)) & 0xFF) << 8) |
			(((data >> uint(diff+16)) & 0xFF) << 16) |
			(((data >> uint(diff+8)) & 0xFF) << 24) |
			(((data >> uint(diff)) & 0xFF) << 32) |
			(data&(1<<uint(diff)-1))<<40
	case oldc <= 56:
		diff := oldc - 48
		return ((data >> uint(diff+40)) & 0xFF) |
			(((data >> uint(diff+32)) & 0xFF) << 8) |
			(((data >> uint(diff+24)) & 0xFF) << 16) |
			(((data >> uint(diff+16)) & 0xFF) << 24) |
			(((data >> uint(diff+8)) & 0xFF) << 32) |
			(((data >> uint(diff)) & 0xFF) << 40) |
			(data&(1<<uint(diff)-1))<<48
	case oldc <= 64:
		diff := oldc - 56
		return ((data >> uint(diff+48)) & 0xFF) |
			(((data >> uint(diff+40)) & 0xFF) << 8) |
			(((data >> uint(diff+32)) & 0xFF) << 16) |
			(((data >> uint(diff+24)) & 0xFF) << 24) |
			(((data >> uint(diff+16)) & 0xFF) << 32) |
			(((data >> uint(diff+8)) & 0xFF) << 40) |
			(((data >> uint(diff)) & 0xFF) << 48) |
			(data&(1<<uint(diff)-1))<<56
	default:
		panic("read count > 64")
	}
	return data

	// log.Printf("-- data %d = %064b\n", oldc, data)
	// log.Printf("-- rdata %d = %064b\n", oldc, bits.Reverse64(data))

	// return bits.Reverse64(data)

	// var mask uint64 = 0xff
	// // var ndata uint64
	// // 7 6 5 4 3 2 1 0 7 6 5 4 3 2 1 0 7 6 5 4 3 2 1 0
	// var buf [8]byte
	// var index uint
	// for i := oldc; i > 0; i -= 8 {
	// 	shift := i - 8
	// 	if shift < 0 {
	// 		shift = 0
	// 	}
	// 	buf[index] = byte(data & (mask << uint(i)) >> uint(shift))
	// 	index++
	// }
	// switch {
	// case oldc <= 8:
	// case oldc <= 16:
	// 	data = uint64(binary.LittleEndian.Uint16(buf[:]))
	// case oldc <= 32:
	// 	data = uint64(binary.LittleEndian.Uint32(buf[:]))
	// case oldc <= 64:
	// 	data = uint64(binary.LittleEndian.Uint64(buf[:]))
	// default:
	// 	panic(fmt.Sprintf("invalid int size: %d", oldc))
	// }
	// return data
}

func dumpBits(data ...uint64) string {
	var bw BitsWriter
	bw.buf = make([]byte, 16)
	bw.bitPos = 7
	var l uint64
	for i := 0; i < len(data); i += 2 {
		// reflect.ValueOf(data[i]).Uint()
		bw.WriteUint(int(data[i]), data[i+1])
		l += data[i]
	}
	return fmt.Sprintf("%08b len(%d) bit_pos(%d)", bw.buf[:bw.index+1], l, bw.bitPos)
}
