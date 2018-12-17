package whisper

import (
	"fmt"
	"time"
)

func (whisper *Whisper) Dump(full bool) {
	if !whisper.compressed {
		panic("Dump works only for compressed whisper files.")
	}

	fmt.Printf("aggregation_method:        %d\n", whisper.aggregationMethod)
	fmt.Printf("max_retention:             %d\n", whisper.maxRetention)
	fmt.Printf("x_files_factor:            %f\n", whisper.xFilesFactor)
	fmt.Printf("compressed:                %t\n", whisper.compressed)
	fmt.Printf("comp_version:              %d\n", whisper.compVersion)
	fmt.Printf("points_per_block:          %d\n", whisper.pointsPerBlock)
	fmt.Printf("avg_compressed_point_size: %f\n", whisper.avgCompressedPointSize)

	for _, arc := range whisper.archives {
		arc.dumpInfo()
	}

	if !full {
		return
	}

	for _, arc := range whisper.archives {
		fmt.Println("")

		if arc.hasBuffer() {
			fmt.Printf("archive %s buffer[%d]:\n", time.Duration(int(time.Second)*arc.secondsPerPoint*arc.numberOfPoints), len(arc.buffer)/PointSize)
			dps := unpackDataPoints(arc.buffer)
			for i, p := range dps {
				fmt.Printf("  % 4d %d: %f\n", i, p.interval, p.value)
			}
		}

		for _, block := range arc.blockRanges {
			buf := make([]byte, arc.blockSize)
			fmt.Printf("archive %s block %d\n", time.Duration(int(time.Second)*arc.secondsPerPoint*arc.numberOfPoints), block.index)
			if err := whisper.fileReadAt(buf, int64(arc.blockOffset(block.index))); err != nil {
				panic(err)
			}
			dps, err := arc.readFromBlock(buf, []dataPoint{}, block.start, block.end)
			if err != nil {
				panic(err)
			}
			for i, p := range dps {
				fmt.Printf("  % 4d %d: %f\n", i, p.interval, p.value)
			}
		}
	}
}

func (archive *archiveInfo) dumpInfo() {
	fmt.Println("")
	fmt.Printf("number_of_points:  %d %s\n", archive.numberOfPoints, time.Duration(int(time.Second)*archive.secondsPerPoint*archive.numberOfPoints))
	fmt.Printf("seconds_per_point: %d %s\n", archive.secondsPerPoint, time.Duration(int(time.Second)*archive.secondsPerPoint))
	fmt.Printf("block_size:        %d\n", archive.blockSize)
	fmt.Printf("buffer_size:       %d\n", archive.bufferSize)
	fmt.Printf("point_size:        %f\n", archive.avgCompressedPointSize)
	fmt.Printf("cblock\n")
	fmt.Printf("  index:     %d\n", archive.cblock.index)
	fmt.Printf("  p[0].interval:     %d\n", archive.cblock.p0.interval)
	fmt.Printf("  p[n-2].interval:   %d\n", archive.cblock.pn2.interval)
	fmt.Printf("  p[n-1].interval:   %d\n", archive.cblock.pn1.interval)
	fmt.Printf("  last_byte:         %08b\n", archive.cblock.lastByte)
	fmt.Printf("  last_byte_offset:  %d\n", archive.cblock.lastByteOffset)
	fmt.Printf("  last_byte_bit_pos: %d\n", archive.cblock.lastByteBitPos)

	archive.sortBlockRanges()

	for _, block := range archive.blockRanges {
		fmt.Printf("%d: %d-%d %d\n", block.index, block.start, block.end, (block.end-block.start)/archive.secondsPerPoint+1)
	}
}
