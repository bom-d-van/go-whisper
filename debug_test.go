// +build debug

package whisper

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
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
