package main

import (
	"fmt"
	"log"
	"math"
	"os"
	"time"

	whisper "github.com/go-graphite/go-whisper"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/kr/pretty"
)

var _ = pretty.Println

func main() {
	// whisper.Now = func() time.Time {
	// 	return time.Unix(1544478230, 0)
	// }

	file1 := os.Args[1]
	file2 := os.Args[2]

	db1, err := whisper.OpenWithOptions(file1, &whisper.Options{})
	if err != nil {
		panic(err)
	}
	db2, err := whisper.OpenWithOptions(file2, &whisper.Options{})
	if err != nil {
		panic(err)
	}

	for _, ret := range db1.Retentions() {
		now := int(time.Now().Unix())
		from := now - ret.MaxRetention()
		until := now

		fmt.Println(time.Second*time.Duration(ret.MaxRetention()), ret.SecondsPerPoint())
		log.Printf("from = %+v\n", from)
		log.Printf("until = %+v\n", until)

		dps1, err := db1.Fetch(from, until)
		if err != nil {
			panic(err)
		}
		dps2, err := db2.Fetch(from, until)
		if err != nil {
			panic(err)
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
			// fmt.Println(diff)
			// pretty.Println(dps1.Points()[23517])
			// pretty.Println(dps2.Points()[23517])
			fmt.Printf("error: does not match\n")
		}
		// return
	}
}
