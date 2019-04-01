// +build debug

package whisper

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"sort"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/kr/pretty"
)

// not really tests here, just for easy debugging/development.

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

	bw.Write(8, 0xaa)
	bw.Write(12, 0x01aa)

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

var whisperFile = flag.String("file", "", "whipser filepath")

func TestCompressedWhisperInplaceConvert(t *testing.T) {
	data := []*TimeSeriesPoint{{Time: int(time.Now().Add(-time.Minute).Unix()), Value: 1024.4096}}
	from, until := int(time.Now().Add(-time.Hour).Unix()), int(time.Now().Unix())

	if _, err := os.Stat(*whisperFile + ".original"); err != nil && os.IsNotExist(err) {
		exec.Command("cp", *whisperFile, *whisperFile+".original").CombinedOutput()
	}

	cwsp, err := OpenWithOptions(*whisperFile, &Options{Compressed: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := cwsp.UpdateMany(data); err != nil {
		t.Fatal(err)
	}
	nps, err := cwsp.Fetch(from, until)
	if err != nil {
		t.Fatal(err)
	}

	wsp, err := OpenWithOptions(*whisperFile+".original", &Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := wsp.UpdateMany(data); err != nil {
		t.Fatal(err)
	}
	ops, err := wsp.Fetch(from, until)
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(nps, ops, cmp.AllowUnexported(TimeSeries{}), cmpopts.EquateNaNs()); diff != "" {
		t.Errorf("inplace convert failed\n%s\n", diff)
		pretty.Println(nps)
		pretty.Println(ops)
	}

	cwsp.Close()
	wsp.Close()
}

func TestBrokenWhisperFile(t *testing.T) {
	wsp, err := OpenWithOptions("test/var/lib/carbon/whisper/loadbalancers/group/external_102/externallb-108_ams4_prod_booking_com/haproxy/backend/chat_booking_com_https_ams4/server/intercom-1003_ams4_prod_booking_com/stot.wsp", &Options{})
	if err != nil {
		t.Fatal(err)
	}
	// ps, err := wsp.Fetch(1552764920, 1552854180)
	// if err != nil {
	// 	t.Fatal(err)
	// }
	start := 1552764920
	end := 1552854180

	var points []dataPoint
	{
		archive := wsp.archives[0]
		b := make([]byte, archive.Size())
		err := wsp.fileReadAt(b, archive.Offset())
		if err != nil {
			t.Fatal(err)
		}
		points = unpackDataPoints(b)
		sort.Slice(points, func(i, j int) bool {
			return points[i].interval < points[j].interval
		})

		// filter null data points
		var index int
		for i := 0; i < len(points); i++ {
			if start <= points[i].interval && points[i].interval <= end {
				points[index] = points[i]
				index++
			}
		}
		points = points[:index]
	}

	log.Printf("len(points) = %+v\n", len(points))
	log.Printf("points[0] = %+v\n", points[0])
	log.Printf("points[len(points)-1] = %+v\n", points[len(points)-1])

	cwsp, err := OpenWithOptions("tmp_stot.cwsp", &Options{})

	archive := cwsp.archives[0]
	var nblock blockInfo
	nblock.index = 3
	nblock.lastByteBitPos = 7
	nblock.lastByteOffset = archive.blockOffset(nblock.index)
	archive.cblock = nblock
	archive.blockRanges = make([]blockRange, 4)
	archive.blockRanges[nblock.index].start = 0
	archive.blockRanges[nblock.index].end = 0

	log.Printf("nblock.lastByteOffset = %+v\n", nblock.lastByteOffset)
	log.Printf("archive.blockSize = %+v\n", archive.blockSize)

	const extraPointSize = 2
	blockBuffer := make([]byte, len(points)*(PointSize+extraPointSize)+endOfBlockSize)

	// debugCompress = true
	size, left, rotate := archive.appendPointsToBlock(blockBuffer, points)
	log.Printf("size = %+v\n", size)
	log.Printf("len(left) = %+v\n", len(left))
	log.Printf("rotate = %+v\n", rotate)

	// blockBuffer2 := blockBuffer[6510:]
	// for i := 0; i < len(blockBuffer2); i += 16 {
	// 	for j := i; j < i+16; j += 2 {
	// 		fmt.Printf("%04x ", blockBuffer2[j:j+2])
	// 	}
	// 	fmt.Println("")
	// }

	var dst []dataPoint
	dst2, _, err := archive.readFromBlock(blockBuffer, dst, start, end)
	if err != nil {
		t.Fatal(err)
	}
	log.Printf("len(dst) = %+v\n", len(dst2))
	log.Printf("archive.blockRanges[3].crc32 = %x\n", archive.blockRanges[3].crc32)
	for i, p := range dst2 {
		// continue
		fmt.Printf("  % 4d %d: %f\n", i, p.interval, p.value)
	}
}
