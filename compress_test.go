package whisper

import (
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/kr/pretty"
)

func TestBitsReadWrite(t *testing.T) {
	buf := make([]byte, 256)

	var bw BitsWriter
	bw.buf = buf
	bw.bitPos = 7

	var br BitsReader
	br.buf = buf
	br.bitPos = 7

	input := []struct {
		val uint64
		len int
	}{
		{len: 8, val: 5},
		{len: 16, val: 97},
		{len: 32, val: 123},
		{len: 64, val: math.Float64bits(95.1)},
		{len: 64, val: 0xf1f2f3f4f5f6f7f8},

		{len: 1, val: 1},
		{len: 15, val: 0x7f7f},
		{len: 55, val: 0x7f2f3f4f5f6f7f},
	}
	for _, d := range input {
		bw.Write(d.len, d.val)
	}
	for i, d := range input {
		if got, want := br.Read(d.len), d.val; got != want {
			t.Errorf("%d: br.Read(%d) = %x; want %b", i, d.len, got, want)
		}
	}
}

func init() {
	log.SetFlags(log.Lshortfile)
}

func TestBlockReadWrite1(t *testing.T) {
	for i := 0; i < 1; i++ {
		var acv archiveInfo
		acv.secondsPerPoint = 1
		acv.numberOfPoints = 64
		acv.cblock.lastByteBitPos = 7
		acv.blockSize = 64 * PointSize
		acv.blockRanges = make([]blockRange, 1)

		ts := 1543689630
		var delta int
		next := func(incs ...int) int {
			for _, i := range incs {
				delta += i
			}
			return ts + delta
		}

		var input []dataPoint
		{
			rand.Seed(time.Now().Unix())
			input = append(input, dataPoint{interval: next(1), value: 1})
			input = append(input, dataPoint{interval: next(1), value: 1})
			input = append(input, dataPoint{interval: next(1), value: 1})
			for i := 0; i < 200; i++ {
				input = append(input, dataPoint{interval: next(rand.Intn(10)), value: rand.NormFloat64()})
			}
		}

		buf := make([]byte, acv.blockSize)
		_, left, _ := acv.appendPointsToBlock(buf, input)

		points := make([]dataPoint, 0, 200)
		points, _, err := acv.readFromBlock(buf, points, ts, ts+60*60*60)
		if err != nil {
			t.Error(err)
		}

		if !reflect.DeepEqual(input, append(points, left...)) {
			if diff := cmp.Diff(input, points, cmp.AllowUnexported(dataPoint{})); diff != "" {
				t.Error(diff)
			}

			t.FailNow()
		}
	}
}

func TestBlockReadWrite2(t *testing.T) {
	for i := 0; i < 1; i++ {
		var acv archiveInfo
		acv.secondsPerPoint = 1
		acv.numberOfPoints = 100
		acv.cblock.lastByteBitPos = 7
		acv.blockSize = int(float64(acv.numberOfPoints) * avgCompressedPointSize)
		acv.blockRanges = make([]blockRange, 1)

		ts := 1544456874
		var input []dataPoint = []dataPoint{
			0: {interval: 1544456874, value: 12},
			1: {interval: 1544456875, value: 24},
			2: {interval: 1544456876, value: 15},
			3: {interval: 1544456877, value: 1},
			4: {interval: 1544456878, value: 2},
			5: {interval: 1544456888, value: 3},
			6: {interval: 1544456889, value: 4},
		}

		buf := make([]byte, acv.blockSize)
		var size int
		{
			written, _, _ := acv.appendPointsToBlock(buf, input[:1])
			size += written
		}
		{
			written, _, _ := acv.appendPointsToBlock(buf[size-1:], input[1:5])
			size += written - 1
		}
		{
			written, _, _ := acv.appendPointsToBlock(buf[size-1:], input[5:])
			size += written - 1
		}

		points, _, err := acv.readFromBlock(buf, make([]dataPoint, 0, 200), ts, ts+30)
		if err != nil {
			t.Error(err)
		}

		if !reflect.DeepEqual(input, points) {
			pretty.Printf("%# v\n", input)
			pretty.Printf("%# v\n", points)
			if diff := cmp.Diff(input, points, cmp.AllowUnexported(dataPoint{})); diff != "" {
				t.Error(diff)
			}

			t.FailNow()
		}
	}
}

func TestCompressedWhisperReadWrite1(t *testing.T) {
	fpath := "comp.whisper"
	os.Remove(fpath)
	whisper, err := CreateWithOptions(
		fpath,
		[]*Retention{
			{secondsPerPoint: 1, numberOfPoints: 100},
			{secondsPerPoint: 5, numberOfPoints: 100},
		},
		Sum,
		0.7,
		&Options{Compressed: true, PointsPerBlock: 7200},
	)
	if err != nil {
		panic(err)
	}

	ts := int(Now().Add(time.Second * -60).Unix())
	var delta int
	next := func(incs int) int {
		delta += incs
		return ts + delta
	}
	input := []*TimeSeriesPoint{
		{Time: next(1), Value: 12},
		{Time: next(1), Value: 24},
		{Time: next(1), Value: 15},
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

	expectVals := make([]float64, 60)
	for i := 0; i < 60; i++ {
		expectVals[i] = math.NaN()
	}
	for _, p := range input {
		expectVals[p.Time-ts-1] = p.Value
	}
	expect := &TimeSeries{
		fromTime:  ts + 1,
		untilTime: ts + 61,
		step:      1,
		values:    expectVals,
	}
	if ts, err := whisper.Fetch(ts, ts+300); err != nil {
		t.Error(err)
	} else if diff := cmp.Diff(ts, expect, cmp.AllowUnexported(TimeSeries{}), cmpopts.EquateNaNs()); diff != "" {
		t.Error(diff)
	}
}

func TestCompressedWhisperReadWrite2(t *testing.T) {
	fpath := "comp.whisper"
	os.Remove(fpath)
	whisper, err := CreateWithOptions(
		fpath,
		[]*Retention{
			{secondsPerPoint: 1, numberOfPoints: 100},
			{secondsPerPoint: 5, numberOfPoints: 100},
		},
		Sum,
		0.7,
		&Options{Compressed: true, PointsPerBlock: 7200},
	)
	if err != nil {
		panic(err)
	}

	nowTs := 1544478230
	Now = func() time.Time {
		return time.Unix(int64(nowTs), 0)
	}

	input := []*TimeSeriesPoint{
		{Time: nowTs - 300, Value: 666},

		{Time: 1544478201, Value: 12},

		{Time: 1544478211, Value: 24},
		{Time: 1544478212, Value: 15},
		{Time: 1544478213, Value: 1},
		{Time: 1544478214, Value: 2},

		{Time: 1544478224, Value: 3},
		{Time: 1544478225, Value: 4},
		{Time: 1544478226, Value: 15.5},
		{Time: 1544478227, Value: 14.0625},
		{Time: 1544478228, Value: 3.25},
		{Time: 1544478229, Value: 8.625},
		{Time: nowTs, Value: 13.1},
	}

	for _, p := range input {
		if err := whisper.UpdateMany([]*TimeSeriesPoint{p}); err != nil {
			t.Error(err)
		}
	}
	whisper.Close()

	whisper, err = OpenWithOptions(fpath, &Options{Compressed: true, PointsPerBlock: 7200})
	if err != nil {
		t.Fatal(err)
	}

	{
		expectVals := make([]float64, 4)
		for i := 0; i < 4; i++ {
			expectVals[i] = math.NaN()
		}
		expectVals[1] = input[0].Value
		expect := &TimeSeries{
			fromTime:  1544477925,
			untilTime: 1544477945,
			step:      5,
			values:    expectVals,
		}
		if ts, err := whisper.Fetch(nowTs-310, nowTs-290); err != nil {
			t.Error(err)
		} else if diff := cmp.Diff(ts, expect, cmp.AllowUnexported(TimeSeries{}), cmpopts.EquateNaNs()); diff != "" {
			t.Error(diff)
		}
	}

	{
		expectVals := make([]float64, 30)
		for i := 0; i < 30; i++ {
			expectVals[i] = math.NaN()
		}
		for _, p := range input[1:] {
			expectVals[29-(nowTs-p.Time)] = p.Value
		}
		expect := &TimeSeries{
			fromTime:  1544478201,
			untilTime: 1544478231,
			step:      1,
			values:    expectVals,
		}
		if ts, err := whisper.Fetch(nowTs-30, nowTs); err != nil {
			t.Error(err)
		} else if diff := cmp.Diff(ts, expect, cmp.AllowUnexported(TimeSeries{}), cmpopts.EquateNaNs()); diff != "" {
			t.Error(diff)
		}
	}
}

func TestCompressedWhisperReadWrite3(t *testing.T) {
	fpath := "test3.wsp"
	os.Remove(fpath)
	os.Remove(fpath + ".cwsp")

	cwhisper, err := CreateWithOptions(
		fpath+".cwsp",
		[]*Retention{
			{secondsPerPoint: 1, numberOfPoints: 172800},   // 1s:2d
			{secondsPerPoint: 60, numberOfPoints: 40320},   // 1m:28d
			{secondsPerPoint: 3600, numberOfPoints: 17520}, // 1h:2y
		},
		Sum,
		0,
		&Options{Compressed: true, PointsPerBlock: 7200},
	)
	if err != nil {
		panic(err)
	}
	ncwhisper, err := CreateWithOptions(
		fpath,
		[]*Retention{
			{secondsPerPoint: 1, numberOfPoints: 172800},   // 1s:2d
			{secondsPerPoint: 60, numberOfPoints: 40320},   // 1m:28d
			{secondsPerPoint: 3600, numberOfPoints: 17520}, // 1h:2y
		},
		Sum,
		0,
		&Options{Compressed: false, PointsPerBlock: 7200},
	)
	if err != nil {
		panic(err)
	}
	cwhisper.Close()
	ncwhisper.Close()

	var now int64 = 1544478230
	Now = func() time.Time {
		return time.Unix(now, 0)
	}

	start := Now().Add(time.Hour * -24 * 1)
	var ps []*TimeSeriesPoint
	for i := 0; i < 1*24*60*60; i++ {
		ps = append(ps, &TimeSeriesPoint{
			Time:  int(start.Add(time.Duration(i) * time.Second).Unix()),
			Value: float64(i),
			// Value: 2000.0 + float64(rand.Intn(100000))/100.0,
			// Value: rand.NormFloat64(),
			// Value: float64(rand.Intn(100000)),
		})

		if len(ps) >= 300 {
			cwhisper, err = OpenWithOptions(fpath+".cwsp", &Options{})
			if err != nil {
				t.Fatal(err)
			}
			ncwhisper, err = OpenWithOptions(fpath, &Options{})
			if err != nil {
				t.Fatal(err)
			}

			if err := cwhisper.UpdateMany(ps); err != nil {
				t.Fatal(err)
			}
			if err := ncwhisper.UpdateMany(ps); err != nil {
				t.Fatal(err)
			}
			ps = ps[:0]

			if err := cwhisper.Close(); err != nil {
				t.Fatal(err)
			}
			if err := ncwhisper.Close(); err != nil {
				t.Fatal(err)
			}
		}

	}

	output, err := exec.Command("go", "run", "cmd/verify.go", "-now", fmt.Sprintf("%d", now), "-ignore-buffer", fpath, fpath+".cwsp").CombinedOutput()
	if err != nil {
		fmt.Println("go", "run", "cmd/verify.go", "-now", fmt.Sprintf("%d", now), "-ignore-buffer", fpath, fpath+".cwsp")
		t.Fatal(err)
		fmt.Fprint(os.Stdout, string(output))
	}
}
