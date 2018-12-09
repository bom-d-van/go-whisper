package whisper

import (
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
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
	// br.buf = []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x07, 0x08}
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

func init() {
	log.SetFlags(log.Lshortfile)
}

func TestBlockReadWrite(t *testing.T) {
	// debug = true

	for i := 0; i < 1; i++ {
		var acv archiveInfo
		acv.secondsPerPoint = 1
		acv.numberOfPoints = 64
		acv.cblock.lastByteBitPos = 7
		acv.blockSize = 64 * PointSize

		ts := 1543689630
		var delta int
		next := func(incs ...int) int {
			for _, i := range incs {
				delta += i
			}
			return ts + delta
		}

		// input := []dataPoint{
		// 	{interval: next(0), value: 12},
		// 	{interval: next(1), value: 24},
		// 	{interval: next(1), value: 15},

		// 	// // {interval: next(1), value: 1},
		// 	// // {interval: ts + 3, value: 1},

		// 	{interval: next(10), value: 1},
		// 	{interval: next(10), value: 2},
		// 	{interval: next(10), value: 3},
		// 	{interval: next(10), value: 4},

		// 	{interval: next(10), value: 15.5},
		// 	{interval: next(11), value: 14.0625},
		// 	{interval: next(11), value: 3.25},
		// 	{interval: next(11), value: 8.625},
		// 	{interval: next(11), value: 13.1},
		// }

		var input []dataPoint
		{
			rand.Seed(time.Now().Unix())
			input = append(input, dataPoint{interval: next(1), value: 1})
			input = append(input, dataPoint{interval: next(1), value: 1})
			input = append(input, dataPoint{interval: next(1), value: 1})
			for i := 0; i < 200; i++ {
				// input = append(input, dataPoint{interval: next(rand.Intn(60 * 60)), value: float64(rand.Intn(30))})
				input = append(input, dataPoint{interval: next(rand.Intn(10)), value: rand.NormFloat64()})
			}
		}

		buf := make([]byte, acv.blockSize)
		written, left := acv.appendPointsToBlock(buf, input...)
		// fmt.Printf("%08b\n", buf[:8])
		// fmt.Printf("%08b\n", buf[8:16])
		// fmt.Printf("%08b\n", buf[16:24])

		// fmt.Printf("%08b\n", input[0].Bytes())

		// log.Printf("written = %+v\n", written)
		// pretty.Println(left)

		if true {
			for i := 0; i < written; i += 8 {
				fmt.Printf("%08b\n", buf[i:i+8])
			}

			acv.dumpInfo()
			fmt.Printf("compressd pctl: %.2f%%\n", (float64(acv.cblock.lastByteOffset)/float64(len(input)*PointSize))*100)
		}

		points := make([]dataPoint, 0, 200)

		if true {
			log.Printf("acv.cblock.lastByteOffset = %+v\n", acv.cblock.lastByteOffset)
			fmt.Println("read test ---")
		}

		points, err := acv.readFromBlock(buf, points, ts, ts+60*60*60)
		if err != nil {
			t.Error(err)
		}

		if !reflect.DeepEqual(input, append(points, left...)) {
			// pretty.Printf("%# v\n", input)
			// pretty.Printf("%# v\n", points)

			if diff := cmp.Diff(input, points, cmp.AllowUnexported(dataPoint{})); diff != "" {
				t.Error(diff)
			}

			t.FailNow()
		}
		// pretty.Printf("%# v\n", input)}
	}
}

func TestCompressedWhisperReadWrite(t *testing.T) {
	fpath := "comp.whisper"
	os.Remove(fpath)
	whisper, err := CreateWithOptions(
		fpath,
		[]*Retention{
			{secondsPerPoint: 1, numberOfPoints: 100},
			// {secondsPerPoint: 20, numberOfPoints: 100},
		},
		Sum,
		0.7,
		&Options{Compressed: false, PointsPerBlock: 7200},
	)
	if err != nil {
		panic(err)
	}

	ts := int(time.Now().Add(time.Second * -60).Unix())
	var delta int
	next := func(incs ...int) int {
		for _, i := range incs {
			delta += i
		}
		return ts + delta
	}
	input := []*TimeSeriesPoint{
		{Time: next(0), Value: 12},
		{Time: next(1), Value: 24},
		{Time: next(1), Value: 15},

		// // {Time: nexV(1), value: 1},
		// // {Time: ts V 3, value: 1},

		{Time: next(1), Value: 1},
		{Time: next(1), Value: 2},
		{Time: next(10), Value: 3},
		{Time: next(1), Value: 4},

		{Time: next(1), Value: 15.5},
		{Time: next(1), Value: 14.0625},
		{Time: next(1), Value: 3.25},
		{Time: next(1), Value: 8.625},
		{Time: next(1), Value: 13.1},
	}

	if err := whisper.UpdateMany(input); err != nil {
		t.Error(err)
	}
	whisper.Close()

	whisper, err = OpenWithOptions(fpath, &Options{Compressed: true, PointsPerBlock: 7200})
	if err != nil {
		t.Fatal(err)
	}

	// pretty.Println(whisper)

	if ts, err := whisper.Fetch(ts, ts+30); err != nil {
		t.Error(err)
	} else {
		pretty.Println(ts)
	}
}
