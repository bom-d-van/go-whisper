package whisper

import (
	"fmt"
	"log"
	"math"
	"testing"

	"github.com/kr/pretty"
)

func TestBitWriter(t *testing.T) {
	var bw BitsWriter
	bw.buf = make([]byte, 8)
	bw.bitPos = 7

	bw.Write(1, 1)
	bw.Write(2, 1)
	bw.Write(3, 1)
	// bw.Write(2, 1)
	// for i := 0; i < 16; i++ {
	// 	bw.Write(1, 1)
	// }

	// fmt.Printf("-- %08b\n", bw.buf)

	bw.WriteUint(8, 0xaa)
	bw.WriteUint(12, 0x01aa)

	// 1010 01 0000 0000 1010 1010
	fmt.Printf("-- %08b\n", bw.buf)
	fmt.Printf("-- %08b\n", 12)
}

func TestBitReader(t *testing.T) {
	var br BitsReader
	br.buf = []byte{0xB3, 0x02, 0xFF, 0xFF, 0xFF}
	br.bitPos = 7

	fmt.Printf("%08b\n", br.buf)
	fmt.Printf("%08b\n", br.Read(1))
	fmt.Printf("%08b\n", br.Read(2))
	fmt.Printf("%08b\n", br.Read(3))
	fmt.Printf("%08b\n", br.Read(4))
	fmt.Printf("%08b\n", br.Read(16))
}

func TestBitsReadWrite(t *testing.T) {
	buf := make([]byte, 32)

	var bw BitsWriter
	bw.buf = buf
	bw.bitPos = 7

	var br BitsReader
	br.buf = buf
	br.bitPos = 7

	// fmt.Printf("%08b\n", 1)
	// fmt.Printf("%08b\n", 5)
	// fmt.Printf("%016b\n", 97)

	// // 1 0000010 1 01100001 00000000
	// // 1 0000010 1 01100001 0000000 00000000

	// bw.Write(1, 1)
	// bw.WriteUint(8, 5)
	// bw.WriteUint(16, 97)
	// bw.WriteUint(32, 123)
	// bw.WriteUint(64, math.Float64bits(95.1))
	// bw.WriteUint(64, 0xfffffffff1ffffff)

	// // br.Read(1)
	// fmt.Printf("br.Read(1) = %v\n", br.Read(1))
	// fmt.Printf("br.Read(8) = %v\n", br.Read(8))
	// fmt.Printf("br.Read(16) = %v\n", br.Read(16))
	// fmt.Printf("br.Read(32) = %v\n", br.Read(32))
	// fmt.Printf("br.Read(64) = %v\n", math.Float64frombits(br.Read(64)))
	// fmt.Printf("br.Read(64) = %x\n", br.Read(64))

	input := []struct {
		val uint64
		len int
	}{
		{len: 1, val: 1},
		{len: 8, val: 5},
		{len: 16, val: 97},
		{len: 32, val: 123},
		{len: 64, val: math.Float64bits(95.1)},
		{len: 64, val: 0xfffffffff1ffffff},
	}
	for _, d := range input {
		bw.WriteUint(d.len, d.val)
	}
	for i, d := range input {
		if got, want := br.Read(d.len), d.val; got != want {
			t.Errorf("%d: br.Read(%d) = %d; want %d", i, d.len, got, want)
		}
	}
}

// // +build ignore

// package main

// import "fmt"

// func main() {
// 	data := 900719925475131
// 	oldc := 52
// 	ndata := ((data & (1<<uint(oldc-48) - 1)) << 48) |
// 		(((data >> uint(oldc-40)) & 0xFF) << 40) |
// 		(((data >> uint(oldc-32)) & 0xFF) << 32) |
// 		(((data >> uint(oldc-24)) & 0xFF) << 24) |
// 		(((data >> uint(oldc-16)) & 0xFF) << 16) |
// 		(((data >> uint(oldc-8)) & 0xFF) << 8) |
// 		((data >> uint(oldc-48)) & 0xFF)

// 	fmt.Printf(" data = %064b\n", data)
// 	fmt.Printf("ndata = %064b\n", ndata)
// }

// // input:  00110011 00110011 00110011 00110011 00110011 01110011 1011

// // output: 1011 00110011 00110011 00110011 00110011 00110011 01110011

func TestBlockReadWrite(t *testing.T) {
	debug = true

	log.SetFlags(log.Lshortfile)
	var acv archiveInfo
	acv.secondsPerPoint = 1
	acv.numberOfPoints = 64
	acv.cblock.lastByteBitPos = 7
	acv.blockSize = acv.numberOfPoints * PointSize
	buf := make([]byte, acv.numberOfPoints*PointSize)
	ts := 1543689630
	var delta int
	next := func(incs ...int) int {
		for _, i := range incs {
			delta += i
		}
		return ts + delta
	}
	input := []dataPoint{
		// {interval: next(0), value: 12},
		// {interval: next(1), value: 24},
		// {interval: next(1), value: 15},

		// {interval: next(1), value: 1},
		// {interval: ts + 3, value: 1},
		// {interval: next(10), value: 1},
		// {interval: next(10), value: 1},
		// {interval: next(10), value: 1},
		// {interval: next(10), value: 1},

		{interval: next(0), value: 15.5},
		{interval: next(1), value: 14.0625},
		{interval: next(1), value: 3.25},
		{interval: next(1), value: 8.625},
		{interval: next(1), value: 13.1},
	}

	written, _ := acv.appendPointsToBlock(buf, input...)
	// fmt.Printf("%08b\n", buf[:8])
	// fmt.Printf("%08b\n", buf[8:16])
	// fmt.Printf("%08b\n", buf[16:24])

	for i := 0; i < written; i += 8 {
		fmt.Printf("%08b\n", buf[i:i+8])
	}

	// fmt.Printf("%08b\n", input[0].Bytes())

	acv.dumpInfo()

	points := make([]dataPoint, 0, 10)
	log.Printf("acv.cblock.lastByteOffset = %+v\n", acv.cblock.lastByteOffset)

	fmt.Println("read test ---")
	points, err := acv.readFromBlock(buf, points, ts, ts+60)
	if err != nil {
		t.Error(err)
	}
	pretty.Printf("%# v\n", points)
}
