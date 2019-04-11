package fuzzy_whisper

import (
	"fmt"
	"math"

	whisper "github.com/go-graphite/go-whisper"
)

func Fuzz(data []byte) int {
	archive := whisper.GenTestArchive(data, whisper.NewRetention(1, 172800))
	dps, _, err := archive.ReadFromBlock(data, whisper.GenDataPointSlice(), -1, math.MaxInt64)
	if err != nil {
		panic(err)
	}
	if len(dps) == 0 {
		panic("failed to read")
	}
	buf := make([]byte, len(data)+5)
	_, left, _ := archive.AppendPointsToBlock(buf, dps)
	if len(left) > 0 {
		panic(fmt.Sprintf("len(left) = %d", len(left)))
	}
	for i, b := range buf {
		if b != data[i] {
			panic("failed to write")
		}
	}
	return 0
}
