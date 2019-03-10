package main

import (
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"sync"
	"time"

	whisper "github.com/go-graphite/go-whisper"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func init() {
	log.SetFlags(log.Lshortfile)
}

func main() {
	now := flag.Int64("now", time.Now().Unix(), "specify the current time")
	ignoreBuffer := flag.Bool("ignore-buffer", false, "ignore points in buffer that haven't been propagated")
	flag.Parse()

	whisper.Now = func() time.Time {
		return time.Unix(*now, 0)
	}

	file1 := flag.Args()[0]
	file2 := flag.Args()[1]

	db1, err := whisper.OpenWithOptions(file1, &whisper.Options{})
	if err != nil {
		panic(err)
	}
	db2, err := whisper.OpenWithOptions(file2, &whisper.Options{})
	if err != nil {
		panic(err)
	}

	var bad bool
	for index, ret := range db1.Retentions() {
		now := int(whisper.Now().Unix())
		from := now - ret.MaxRetention()
		until := now

		fmt.Println(time.Second*time.Duration(ret.MaxRetention()), ret.SecondsPerPoint())
		log.Printf("from = %+v\n", from)
		log.Printf("until = %+v\n", until)

		var dps1, dps2 *whisper.TimeSeries
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()

			var err error
			dps1, err = db1.Fetch(from, until)
			if err != nil {
				panic(err)
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()

			var err error
			dps2, err = db2.Fetch(from, until)
			if err != nil {
				panic(err)
			}
		}()

		wg.Wait()

		if *ignoreBuffer && index > 0 {
			if !db1.IsCompressed() {
				vals := dps1.Values()
				vals[len(vals)-1] = math.NaN()
				vals[len(vals)-2] = math.NaN()
			}
			if !db2.IsCompressed() {
				vals := dps2.Values()
				vals[len(vals)-1] = math.NaN()
				vals[len(vals)-2] = math.NaN()
			}
		}

		var vals1, vals2 int
		for _, p := range dps1.Values() {
			if !math.IsNaN(p) {
				vals1++
			}
		}
		for _, p := range dps2.Values() {
			if !math.IsNaN(p) {
				vals2++
			}
		}

		fmt.Printf("len1 = %d len2 = %d vals1 = %d vals2 = %d\n", len(dps1.Values()), len(dps2.Values()), vals1, vals2)

		if diff := cmp.Diff(dps1.Points(), dps2.Points(), cmp.AllowUnexported(whisper.TimeSeries{}), cmpopts.EquateNaNs()); diff != "" {
			fmt.Println(diff)
			fmt.Printf("error: does not match for %s\n", file1)
			bad = true
		}
	}
	if bad {
		os.Exit(1)
	}
}
