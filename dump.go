// +build ignore

package main

import (
	"fmt"
	"os"
	"time"

	whisper "github.com/go-graphite/go-whisper"
	"github.com/kr/pretty"
)

func main() {
	whisper.Now = func() time.Time {
		return time.Unix(1544478230-3600, 0)
	}

	db, err := whisper.OpenWithOptions(os.Args[1], &whisper.Options{Compressed: true, PointsPerBlock: 7200})
	if err != nil {
		panic(err)
	}

	data, err := db.Fetch(
		// int(time.Unix(1544478230, 0).Add(time.Hour*-24*365*2).Add(time.Hour*-72).Unix()),
		// int(time.Unix(1544478230, 0).Add(time.Hour*-24*365*2).Add(time.Hour*17520).Unix()),

		// 1481403600,
		// 1544472000,

		int(time.Unix(1544478230, 0).Add(time.Hour*-24*27).Unix()),
		int(time.Unix(1544478230, 0).Add(time.Hour*-24*28).Add(time.Minute*40320).Unix()),
	)
	if err != nil {
		panic(err)
	}

	// return

	pretty.Println(data)
	pretty.Println(db)

	fmt.Println(int(time.Unix(1544478230, 0).Add(time.Hour * -24 * 365 * 2).Add(time.Hour * -72).Unix()))
	fmt.Println(int(time.Unix(1544478230, 0).Add(time.Hour * -24 * 365 * 2).Add(time.Hour * 17520).Unix()))
	fmt.Println(1481403600)
	fmt.Println(1544472000)
}
