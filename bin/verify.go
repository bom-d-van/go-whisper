package main

import (
	"fmt"
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
		dps1, err := db1.Fetch(from, until)
		if err != nil {
			panic(err)
		}
		dps2, err := db2.Fetch(from, until)
		if err != nil {
			panic(err)
		}

		fmt.Println(time.Duration(ret.MaxRetention()))
		if diff := cmp.Diff(dps1, dps2, cmp.AllowUnexported(whisper.TimeSeries{}), cmpopts.EquateNaNs()); diff != "" {
			// pretty.Println(dps1)
			// pretty.Println(dps2)
			fmt.Println(diff)
		}
		// return
	}
}
