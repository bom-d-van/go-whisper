package main

import (
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	whisper "github.com/go-graphite/go-whisper"
)

func init() {
	log.SetFlags(log.Lshortfile)
}

func main() {
	now := flag.Int("now", 0, "specify the current time")
	ignoreBuffer := flag.Bool("ignore-buffer", false, "ignore points in buffer that haven't been propagated")
	quarantinesRaw := flag.String("quarantines", "", "ignore data started from this point. e.g. 2019-02-21,2019-02-22")
	verbose := flag.Bool("verbose", false, "be overly and nicely talkive")
	flag.BoolVar(verbose, "v", false, "be overly and nicely talkive")
	flag.Parse()

	var quarantines [][2]int
	if *quarantinesRaw != "" {
		for _, q := range strings.Split(*quarantinesRaw, ";") {
			var quarantine [2]int
			for i, t := range strings.Split(q, ",") {
				tim, err := time.Parse("2006-01-02", t)
				if err != nil {
					panic(err)
				}
				quarantine[i] = int(tim.Unix())
			}
			quarantines = append(quarantines, quarantine)
		}
	}

	if *now > 0 {
		whisper.Now = func() time.Time {
			return time.Unix(int64(*now), 0)
		}
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
	// var sums []string
	for index, ret := range db1.Retentions() {
		// now := int(whisper.Now().Unix())
		// from := now - ret.MaxRetention()
		// until := now
		from := int(whisper.Now().Unix()) - ret.MaxRetention() + ret.SecondsPerPoint()*60
		until := int(whisper.Now().Unix()) - 3600*8

		if *verbose {
			fmt.Printf("%d %s: from = %+v until = %+v\n", index, ret, from, until)
		}

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

		if *ignoreBuffer && index < len(db1.Retentions())-1 {
			{
				vals := dps1.Values()
				vals[len(vals)-1] = math.NaN()
				vals[len(vals)-2] = math.NaN()
			}
			{
				vals := dps2.Values()
				vals[len(vals)-1] = math.NaN()
				vals[len(vals)-2] = math.NaN()
			}
		}

		for _, quarantine := range quarantines {
			qfrom := quarantine[0]
			quntil := quarantine[1]
			if from <= qfrom && qfrom <= until {
				qfromIndex := (qfrom - from) / ret.SecondsPerPoint()
				quntilIndex := (quntil - from) / ret.SecondsPerPoint()
				{
					vals := dps1.Values()
					for i := qfromIndex; i <= quntilIndex && i < len(vals); i++ {
						vals[i] = math.NaN()
					}
				}
				{
					vals := dps2.Values()
					for i := qfromIndex; i <= quntilIndex && i < len(vals); i++ {
						vals[i] = math.NaN()
					}
				}
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

		fmt.Printf("  len1 = %d len2 = %d vals1 = %d vals2 = %d\n", len(dps1.Values()), len(dps2.Values()), vals1, vals2)

		if len(dps1.Values()) != len(dps2.Values()) {
			bad = true
			fmt.Printf("  size doesn't match: %d != %d\n", len(dps1.Values()), len(dps2.Values()))
		}
		if vals1 != vals2 {
			bad = true
			fmt.Printf("  values doesn't match: %d != %d\n", vals1, vals2)
		}
		var ptDiff int
		for i, p1 := range dps1.Values() {
			if len(dps2.Values()) < i {
				break
			}
			p2 := dps2.Values()[i]
			if !((math.IsNaN(p1) && math.IsNaN(p2)) || p1 == p2) {
				bad = true
				ptDiff++
				if *verbose {
					fmt.Printf("    %d: %v != %v\n", i, p1, p2)
				}
			}
		}
		fmt.Printf("  point mismatches: %d\n", ptDiff)

		// if diff := cmp.Diff(dps1.Points(), dps2.Points(), cmp.AllowUnexported(whisper.TimeSeries{}), cmpopts.EquateNaNs()); diff != "" {
		// 	fmt.Println(diff)
		// 	fmt.Printf("error: does not match for %s\n", file1)
		// 	bad = true
		// }
	}
	if bad {
		os.Exit(1)
	}
}
