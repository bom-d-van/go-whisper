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
)

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

		var nan1, nan2 int
		for _, p := range dps1.Values() {
			if !math.IsNaN(p) {
				nan1++
			}
		}
		for _, p := range dps2.Values() {
			if !math.IsNaN(p) {
				nan2++
			}
		}

		fmt.Printf("len1 = %d len2 = %d nan1 = %d nan2 = %d\n", len(dps1.Values()), len(dps2.Values()), nan1, nan2)

		if diff := cmp.Diff(dps1, dps2, cmp.AllowUnexported(whisper.TimeSeries{}), cmpopts.EquateNaNs()); diff != "" {
			// pretty.Println(dps2.Points())
			// pretty.Println(dps1.Points()[12324-10 : 12324+10])
			// fmt.Println(diff)
			fmt.Printf("error: not matched\n")
		}
		// return
	}
}
