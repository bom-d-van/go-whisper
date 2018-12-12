/*
	Package whisper implements Graphite's Whisper database format
*/
package whisper

import (
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kr/pretty"
)

const (
	IntSize         = 4
	FloatSize       = 4
	Float64Size     = 8
	PointSize       = 12
	MetadataSize    = 16
	ArchiveInfoSize = 12

	CompressedMetadataSize    = 24
	VersionSize               = 1
	CompressedArchiveInfoSize = 10*4 + 3*8 // 64
	avgCompressedPointSize    = 2
	BlockRangeSize            = 8
)

const (
	Seconds = 1
	Minutes = 60
	Hours   = 3600
	Days    = 86400
	Weeks   = 86400 * 7
	Years   = 86400 * 365
)

type AggregationMethod int

const (
	Average AggregationMethod = iota + 1
	Sum
	Last
	Max
	Min
)

var (
	compressedMagicString = []byte("whisper_compressed") // len = 18
)

type Options struct {
	Sparse bool
	FLock  bool

	Compressed     bool
	PointsPerBlock int
}

func unitMultiplier(s string) (int, error) {
	switch {
	case strings.HasPrefix(s, "s"):
		return Seconds, nil
	case strings.HasPrefix(s, "m"):
		return Minutes, nil
	case strings.HasPrefix(s, "h"):
		return Hours, nil
	case strings.HasPrefix(s, "d"):
		return Days, nil
	case strings.HasPrefix(s, "w"):
		return Weeks, nil
	case strings.HasPrefix(s, "y"):
		return Years, nil
	}
	return 0, fmt.Errorf("Invalid unit multiplier [%v]", s)
}

var retentionRegexp *regexp.Regexp = regexp.MustCompile("^(\\d+)([smhdwy]+)$")

func parseRetentionPart(retentionPart string) (int, error) {
	part, err := strconv.ParseInt(retentionPart, 10, 32)
	if err == nil {
		return int(part), nil
	}
	if !retentionRegexp.MatchString(retentionPart) {
		return 0, fmt.Errorf("%v", retentionPart)
	}
	matches := retentionRegexp.FindStringSubmatch(retentionPart)
	value, err := strconv.ParseInt(matches[1], 10, 32)
	if err != nil {
		panic(fmt.Sprintf("Regex on %v is borked, %v cannot be parsed as int", retentionPart, matches[1]))
	}
	multiplier, err := unitMultiplier(matches[2])
	return multiplier * int(value), err
}

/*
  Parse a retention definition as you would find in the storage-schemas.conf of a Carbon install.
  Note that this only parses a single retention definition, if you have multiple definitions (separated by a comma)
  you will have to split them yourself.

  ParseRetentionDef("10s:14d") Retention{10, 120960}

  See: http://graphite.readthedocs.org/en/1.0/config-carbon.html#storage-schemas-conf
*/
func ParseRetentionDef(retentionDef string) (*Retention, error) {
	parts := strings.Split(retentionDef, ":")
	if len(parts) != 2 {
		return nil, fmt.Errorf("Not enough parts in retentionDef [%v]", retentionDef)
	}
	precision, err := parseRetentionPart(parts[0])
	if err != nil {
		return nil, fmt.Errorf("Failed to parse precision: %v", err)
	}

	points, err := parseRetentionPart(parts[1])
	if err != nil {
		return nil, fmt.Errorf("Failed to parse points: %v", err)
	}
	points /= precision

	return &Retention{precision, points}, err
}

func ParseRetentionDefs(retentionDefs string) (Retentions, error) {
	retentions := make(Retentions, 0)
	for _, retentionDef := range strings.Split(retentionDefs, ",") {
		retention, err := ParseRetentionDef(retentionDef)
		if err != nil {
			return nil, err
		}
		retentions = append(retentions, retention)
	}
	return retentions, nil
}

/*
	Represents a Whisper database file.
*/
type Whisper struct {
	file *os.File // TODO: switch bufio.Writer

	// Metadata
	aggregationMethod AggregationMethod
	maxRetention      int
	xFilesFactor      float32
	archives          []*archiveInfo

	compressed     bool
	compVersion    int
	pointsPerBlock int
}

// Wrappers for whisper.file operations
func (whisper *Whisper) fileWriteAt(b []byte, off int64) error {
	_, err := whisper.file.WriteAt(b, off)
	return err
}

// Wrappers for file.ReadAt operations
func (whisper *Whisper) fileReadAt(b []byte, off int64) error {
	_, err := whisper.file.ReadAt(b, off)
	return err
}

/*
	Create a new Whisper database file and write it's header.
*/
func Create(path string, retentions Retentions, aggregationMethod AggregationMethod, xFilesFactor float32) (whisper *Whisper, err error) {
	return CreateWithOptions(path, retentions, aggregationMethod, xFilesFactor, &Options{
		Sparse: false,
		FLock:  false,
	})
}

// CreateWithOptions is more customizable create function
func CreateWithOptions(path string, retentions Retentions, aggregationMethod AggregationMethod, xFilesFactor float32, options *Options) (whisper *Whisper, err error) {
	if options == nil {
		options = &Options{}
	}
	sort.Sort(retentionsByPrecision{retentions})
	if err = validateRetentions(retentions); err != nil {
		return nil, err
	}
	_, err = os.Stat(path)
	if err == nil {
		return nil, os.ErrExist
	}
	file, err := os.Create(path)
	if err != nil {
		return nil, err
	}

	if options.FLock {
		if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
			file.Close()
			return nil, err
		}
	}

	whisper = new(Whisper)

	// Set the metadata
	whisper.file = file
	whisper.aggregationMethod = aggregationMethod
	whisper.xFilesFactor = xFilesFactor

	whisper.compressed = options.Compressed
	whisper.pointsPerBlock = options.PointsPerBlock
	for _, retention := range retentions {
		if retention.MaxRetention() > whisper.maxRetention {
			whisper.maxRetention = retention.MaxRetention()
		}
	}

	// Set the archive info
	// whisper.archives = make([]*archiveInfo, len(retentions))
	// offset := MetadataSize + (ArchiveInfoSize * len(retentions))

	for _, retention := range retentions {
		whisper.archives = append(whisper.archives, &archiveInfo{Retention: *retention})
		// whisper.archives[i].Retention = *retention
	}

	offset := whisper.MetadataSize()
	for i, retention := range retentions {
		whisper.archives[i].offset = offset

		if whisper.compressed {
			whisper.archives[i].cblock.lastByteBitPos = 7
			if retention.numberOfPoints > whisper.pointsPerBlock {
				whisper.archives[i].blockSize = whisper.pointsPerBlock * avgCompressedPointSize
			} else {
				whisper.archives[i].blockSize = retention.numberOfPoints * avgCompressedPointSize
			}
			// whisper.archives[i].blockCount = whisper.blockCount(whisper.archives[i])
			whisper.archives[i].blockRanges = make([]blockRange, whisper.blockCount(whisper.archives[i]))
			// for i := range whisper.archives[i].blockRanges {
			// whisper.archives[i].blockRanges[i].index = i
			// }

			// offset += retention.numberOfPoints * avgCompressedPointSize
			// offset += whisper.archives[i].blockSize * whisper.blockCount(whisper.archives[i])
			offset += whisper.archives[i].blockSize * whisper.archives[i].blockCount()

			if i > 0 {
				log.Printf("buffer = %+v\n", i)
				size := whisper.archives[i].secondsPerPoint / whisper.archives[i-1].secondsPerPoint * PointSize
				whisper.archives[i-1].buffer = make([]byte, size)
			}
		} else {
			offset += retention.Size()
		}
	}

	if whisper.compressed {
		whisper.initMetaInfo()
		err = whisper.writeHeaderCompressed()
	} else {
		err = whisper.writeHeader()
	}
	if err != nil {
		return nil, err
	}

	// pre-allocate file size, fallocate proved slower
	if options.Sparse {
		if _, err = whisper.file.Seek(int64(whisper.Size()-1), 0); err != nil {
			return nil, err
		}
		if _, err = whisper.file.Write([]byte{0}); err != nil {
			return nil, err
		}
	} else {
		if err := allocateDiskSpace(whisper.file, whisper.Size()-whisper.MetadataSize()); err != nil {
			return nil, err
		}
	}
	// whisper.file.Sync()

	return whisper, nil
}

func (archive *archiveInfo) blockCount() int {
	return len(archive.blockRanges)
}

func (whisper *Whisper) blockCount(archive *archiveInfo) int {
	// retention.numberOfPoints / whisper.pointsPerBlock
	return int(math.Ceil(float64(archive.numberOfPoints) / float64(whisper.pointsPerBlock)))
}

func allocateDiskSpace(file *os.File, remaining int) error {
	chunkSize := 16384
	zeros := make([]byte, chunkSize)
	for remaining > chunkSize {
		if _, err := file.Write(zeros); err != nil {
			return err
		}
		remaining -= chunkSize
	}
	if _, err := file.Write(zeros[:remaining]); err != nil {
		return err
	}
	return nil
}

func validateRetentions(retentions Retentions) error {
	if len(retentions) == 0 {
		return fmt.Errorf("No retentions")
	}
	for i, retention := range retentions {
		if i == len(retentions)-1 {
			break
		}

		nextRetention := retentions[i+1]
		if !(retention.secondsPerPoint < nextRetention.secondsPerPoint) {
			return fmt.Errorf("A Whisper database may not be configured having two archives with the same precision (archive%v: %v, archive%v: %v)", i, retention, i+1, nextRetention)
		}

		if mod(nextRetention.secondsPerPoint, retention.secondsPerPoint) != 0 {
			return fmt.Errorf("Higher precision archives' precision must evenly divide all lower precision archives' precision (archive%v: %v, archive%v: %v)", i, retention.secondsPerPoint, i+1, nextRetention.secondsPerPoint)
		}

		if retention.MaxRetention() >= nextRetention.MaxRetention() {
			return fmt.Errorf("Lower precision archives must cover larger time intervals than higher precision archives (archive%v: %v seconds, archive%v: %v seconds)", i, retention.MaxRetention(), i+1, nextRetention.MaxRetention())
		}

		if retention.numberOfPoints < (nextRetention.secondsPerPoint / retention.secondsPerPoint) {
			return fmt.Errorf("Each archive must have at least enough points to consolidate to the next archive (archive%v consolidates %v of archive%v's points but it has only %v total points)", i+1, nextRetention.secondsPerPoint/retention.secondsPerPoint, i, retention.numberOfPoints)
		}
	}
	return nil
}

/*
  Open an existing Whisper database and read it's header
*/
func Open(path string) (whisper *Whisper, err error) {
	return OpenWithOptions(path, &Options{
		FLock: false,
	})
}

func OpenWithOptions(path string, options *Options) (whisper *Whisper, err error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0666)
	if err != nil {
		return
	}

	defer func() {
		if err != nil {
			whisper = nil
			file.Close()
		}
	}()

	if options.FLock {
		if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
			return
		}
	}

	whisper = new(Whisper)
	whisper.file = file

	b := make([]byte, len(compressedMagicString))

	if _, err := whisper.file.Read(b); err != nil {
		return nil, fmt.Errorf("Unable to read magic string: %s", err)
	} else if string(b) == string(compressedMagicString) {
		whisper.compressed = true
	} else if _, err := whisper.file.Seek(0, 0); err != nil {
		return nil, fmt.Errorf("Unable to reset file offset: %s", err)
	}

	offset := 0

	// read the metadata
	if whisper.compressed {
		b = make([]byte, CompressedMetadataSize)
		err = whisper.readHeaderCompressed()

		return whisper, err
	}

	b = make([]byte, MetadataSize)
	readed, err := file.Read(b)

	if err != nil {
		err = fmt.Errorf("Unable to read header: %s", err.Error())
		return
	}
	if readed != MetadataSize {
		err = fmt.Errorf("Unable to read header: EOF")
		return
	}

	a := unpackInt(b[offset : offset+IntSize])
	if a > 1024 { // support very old format. File starts with lastUpdate and has only average aggregation method
		whisper.aggregationMethod = Average
	} else {
		whisper.aggregationMethod = AggregationMethod(a)
	}
	offset += IntSize
	whisper.maxRetention = unpackInt(b[offset : offset+IntSize])
	offset += IntSize
	whisper.xFilesFactor = unpackFloat32(b[offset : offset+FloatSize])
	offset += FloatSize
	archiveCount := unpackInt(b[offset : offset+IntSize])
	offset += IntSize

	// read the archive info
	b = make([]byte, ArchiveInfoSize)

	whisper.archives = make([]*archiveInfo, 0)
	for i := 0; i < archiveCount; i++ {
		readed, err = file.Read(b)
		if err != nil || readed != ArchiveInfoSize {
			err = fmt.Errorf("Unable to read archive %d metadata", i)
			return
		}
		whisper.archives = append(whisper.archives, unpackArchiveInfo(b))
	}

	return whisper, nil
}

func (whisper *Whisper) initMetaInfo() {
	metaOffset := whisper.MetadataSize()
	bufOffset := metaOffset - whisper.bufferSize()
	// whisper.archives[0].blockOffset = metaOffset
	// whisper.archives[0].blockSize = whisper.pointsPerBlock * avgCompressedPointSize

	for i, arc := range whisper.archives {
		// arc.blockSize = whisper.pointsPerBlock * avgCompressedPointSize
		if arc.cblock.lastByteOffset == 0 {
			arc.cblock.lastByteOffset = arc.blockOffset(0)
		}
		arc.whisper = whisper
		// arc.blockBuffer = make([]byte, arc.blockSize)

		arc.initBlockRanges()
		if i == 0 {
			continue
		}

		prevArc := whisper.archives[i-1]
		prevArc.next = arc
		prevArc.bufferSize = arc.secondsPerPoint / prevArc.secondsPerPoint * PointSize
		bufOffset += prevArc.bufferSize
		prevArc.bufferOffset = bufOffset

		// metaOffset += prevArc.blockSize * whisper.blockCount(prevArc)
		// arc.blockOffset = metaOffset
	}
}

func (archive *archiveInfo) hasBuffer() bool {
	return archive.bufferSize > 0
}

func (arc *archiveInfo) initBlockRanges() {
	for i := range arc.blockRanges {
		arc.blockRanges[i].index = i
	}
	sort.Slice(arc.blockRanges, func(i, j int) bool {
		return arc.blockRanges[i].start > 0 && arc.blockRanges[j].start > 0 && arc.blockRanges[i].start < arc.blockRanges[j].start
	})
}

// [whisper header]
// [archive_0 header]
// ...
// [archive_n header]
// [buffer_0]
// ...
// [buffer_n-1]
// [archive0 blocks0]
// ...
// [archive0 blocksn]
func (whisper *Whisper) readHeaderCompressed() (err error) {
	offset := 0
	log.Printf("whisper.MetadataSize() = %+v\n", whisper.MetadataSize())
	hlen := whisper.MetadataSize() - len(compressedMagicString)
	b := make([]byte, hlen)
	readed, err := whisper.file.Read(b)
	if err != nil {
		err = fmt.Errorf("Unable to read header: %s", err.Error())
		return
	}
	if readed != hlen {
		err = fmt.Errorf("Unable to read header: EOF")
		return
	}

	whisper.compVersion = int(b[offset])
	offset += 1

	log.Printf("b[offset : offset+IntSize] = %08b\n", b[offset:offset+IntSize])
	whisper.aggregationMethod = AggregationMethod(unpackInt(b[offset : offset+IntSize]))
	offset += IntSize
	whisper.maxRetention = unpackInt(b[offset : offset+IntSize])
	offset += IntSize
	whisper.xFilesFactor = unpackFloat32(b[offset : offset+FloatSize])
	offset += FloatSize
	whisper.pointsPerBlock = unpackInt(b[offset : offset+IntSize])
	offset += IntSize
	whisper.compVersion = unpackInt(b[offset : offset+IntSize])
	offset += IntSize
	archiveCount := unpackInt(b[offset : offset+IntSize])
	offset += IntSize

	log.Printf("offset = %+v\n", offset)

	// a := unpackInt(b[offset : offset+IntSize])
	// if a > 1024 { // support very old format. File starts with lastUpdate and has only average aggregation method
	// 	whisper.aggregationMethod = Average
	// } else {
	// 	whisper.aggregationMethod = AggregationMethod(a)
	// }
	// offset += IntSize
	// whisper.maxRetention = unpackInt(b[offset : offset+IntSize])
	// offset += IntSize
	// whisper.xFilesFactor = unpackFloat32(b[offset : offset+FloatSize])
	// offset += FloatSize
	// archiveCount := unpackInt(b[offset : offset+IntSize])
	// offset += IntSize

	// read the archive info
	// b = make([]byte, CompressedArchiveInfoSize)

	whisper.archives = make([]*archiveInfo, archiveCount)
	for i := 0; i < archiveCount; i++ {
		b := make([]byte, CompressedArchiveInfoSize)
		readed, err = whisper.file.Read(b)
		if err != nil || readed != CompressedArchiveInfoSize {
			err = fmt.Errorf("Unable to read archive %d metadata: %s", i, err)
			return
		}
		offset = 0
		var arc archiveInfo

		// arc.startTimestamp = unpackInt(b[offset : offset+IntSize])
		// offset += IntSize
		// arc.startIndex = unpackInt(b[offset : offset+IntSize])
		// offset += IntSize
		arc.offset = unpackInt(b[offset : offset+IntSize])
		offset += IntSize
		arc.secondsPerPoint = unpackInt(b[offset : offset+IntSize])
		offset += IntSize
		arc.numberOfPoints = unpackInt(b[offset : offset+IntSize])
		offset += IntSize
		arc.blockSize = unpackInt(b[offset : offset+IntSize])
		offset += IntSize

		arc.cblock.index = unpackInt(b[offset : offset+IntSize])
		offset += IntSize
		// arc.cblock.offset = unpackInt(b[offset : offset+IntSize])
		// offset += IntSize
		// arc.cblock.size = unpackInt(b[offset : offset+IntSize])
		// offset += IntSize
		// arc.cblock.crc32 = unpackInt(b[offset : offset+IntSize])
		// offset += IntSize
		arc.cblock.p0.interval = unpackInt(b[offset : offset+IntSize])
		offset += IntSize
		arc.cblock.p0.value = unpackFloat64(b[offset : offset+Float64Size])
		offset += Float64Size
		arc.cblock.pn1.interval = unpackInt(b[offset : offset+IntSize])
		offset += IntSize
		arc.cblock.pn1.value = unpackFloat64(b[offset : offset+Float64Size])
		offset += Float64Size
		arc.cblock.pn2.interval = unpackInt(b[offset : offset+IntSize])
		offset += IntSize
		arc.cblock.pn2.value = unpackFloat64(b[offset : offset+Float64Size])
		offset += Float64Size
		arc.cblock.lastByte = byte(unpackInt(b[offset : offset+IntSize]))
		offset += IntSize
		arc.cblock.lastByteBitPos = unpackInt(b[offset : offset+IntSize])
		offset += IntSize

		whisper.archives[i] = &arc
	}

	whisper.initMetaInfo()

	for i, arc := range whisper.archives {
		b := make([]byte, BlockRangeSize*whisper.blockCount(arc))
		readed, err = whisper.file.Read(b)
		if err != nil || readed != BlockRangeSize*whisper.blockCount(arc) {
			err = fmt.Errorf("Unable to read archive %d block ranges: %s", i, err)
			return
		}
		offset := 0

		arc.blockRanges = make([]blockRange, whisper.blockCount(arc))
		for i := range arc.blockRanges {
			arc.blockRanges[i].start = unpackInt(b[offset : offset+IntSize])
			offset += IntSize
			arc.blockRanges[i].end = unpackInt(b[offset : offset+IntSize])
			offset += IntSize
		}

		arc.initBlockRanges()

		if !arc.hasBuffer() {
			continue
		}
		arc.buffer = make([]byte, arc.bufferSize)
		// copy(arc.buffer, b[offset:])
		// offset += arc.bufferSize

		readed, err = whisper.file.Read(arc.buffer)
		if err != nil || readed != arc.bufferSize {
			err = fmt.Errorf("Unable to read archive %d buffer: %s", i, err)
			return
		}
	}

	// for i, arc := range whisper.archives {
	// 	if !arc.hasBuffer() {
	// 		break
	// 	}
	// 	arc.buffer = make([]byte, arc.bufferSize)
	// 	// copy(arc.buffer, b[offset:])
	// 	// offset += arc.bufferSize

	// 	readed, err = whisper.file.Read(arc.buffer)
	// 	if err != nil || readed != arc.bufferSize {
	// 		err = fmt.Errorf("Unable to read archive %d buffer: %s", i, err)
	// 		return
	// 	}
	// }

	// pretty.Println(whisper)

	return nil
}

func (whisper *Whisper) writeHeader() (err error) {
	b := make([]byte, whisper.MetadataSize())
	i := 0
	i += packInt(b, int(whisper.aggregationMethod), i)
	i += packInt(b, whisper.maxRetention, i)
	i += packFloat32(b, whisper.xFilesFactor, i)
	i += packInt(b, len(whisper.archives), i)
	for _, archive := range whisper.archives {
		i += packInt(b, archive.offset, i)
		i += packInt(b, archive.secondsPerPoint, i)
		i += packInt(b, archive.numberOfPoints, i)
	}
	_, err = whisper.file.Write(b)

	return err
}

func (whisper *Whisper) writeHeaderCompressed() (err error) {
	b := make([]byte, whisper.MetadataSize())
	i := 0

	// magic string
	i += len(compressedMagicString)
	copy(b, compressedMagicString)

	// version
	b[i] = 1
	i += 1

	// pretty.Println(whisper)

	i += packInt(b, int(whisper.aggregationMethod), i)
	i += packInt(b, whisper.maxRetention, i)
	i += packFloat32(b, whisper.xFilesFactor, i)
	i += packInt(b, whisper.pointsPerBlock, i)
	i += packInt(b, whisper.compVersion, i)
	i += packInt(b, len(whisper.archives), i)

	for _, archive := range whisper.archives {
		// i += packInt(b, archive.startTimestamp, i)
		// i += packInt(b, archive.startIndex, i)
		i += packInt(b, archive.offset, i)
		i += packInt(b, archive.secondsPerPoint, i)
		i += packInt(b, archive.numberOfPoints, i)
		i += packInt(b, archive.blockSize, i)
		// for _, bran := range archive.blockRanges {
		// 	i += packInt(b, bran.start, i)
		// 	i += packInt(b, bran.end, i)
		// }

		i += packInt(b, archive.cblock.index, i)
		// i += packInt(b, archive.cblock.offset, i)
		// i += packInt(b, archive.cblock.size, i)
		// i += packInt(b, archive.cblock.crc32, i)
		i += packInt(b, archive.cblock.p0.interval, i)
		i += packFloat64(b, archive.cblock.p0.value, i)
		i += packInt(b, archive.cblock.pn1.interval, i)
		i += packFloat64(b, archive.cblock.pn1.value, i)
		i += packInt(b, archive.cblock.pn2.interval, i)
		i += packFloat64(b, archive.cblock.pn2.value, i)
		i += packInt(b, int(archive.cblock.lastByte), i)
		i += packInt(b, archive.cblock.lastByteBitPos, i)
	}

	// write block_range_info and buffer
	for _, archive := range whisper.archives {
		sort.Slice(archive.blockRanges, func(i, j int) bool {
			return archive.blockRanges[i].index < archive.blockRanges[j].index
		})

		for _, bran := range archive.blockRanges {
			i += packInt(b, bran.start, i)
			i += packInt(b, bran.end, i)
		}

		if archive.hasBuffer() {
			// log.Printf("archive.buffer = %x\n", archive.buffer)
			// log.Printf("i = %+v\n", i)
			i += copy(b[i:], archive.buffer)
		}
	}

	// // write buffer
	// for _, archive := range whisper.archives {
	// 	// size := archive.secondsPerPoint / whisper.archives[i].secondsPerPoint * PointSize
	// 	// if err := allocateDiskSpace(whisper.file, size); err != nil {
	// 	// 	return err
	// 	// }
	// }

	if err := whisper.fileWriteAt(b, 0); err != nil {
		return err
	}
	if _, err := whisper.file.Seek(int64(len(b)), 0); err != nil {
		return err
	}

	return nil
}

/*
  Close the whisper file
*/
func (whisper *Whisper) Close() {
	whisper.file.Close()
}

/*
  Calculate the total number of bytes the Whisper file should be according to the metadata.
*/
func (whisper *Whisper) Size() int {
	size := whisper.MetadataSize()
	for _, archive := range whisper.archives {
		if whisper.compressed {
			size += archive.blockSize * archive.blockCount()
		} else {
			size += archive.Size()
		}
	}
	return size
}

/*
  Calculate the number of bytes the metadata section will be.
*/
func (whisper *Whisper) MetadataSize() int {
	if whisper.compressed {
		return len(compressedMagicString) + VersionSize + CompressedMetadataSize + (CompressedArchiveInfoSize * len(whisper.archives)) + whisper.blockRangesSize() + whisper.bufferSize()
	}

	return MetadataSize + (ArchiveInfoSize * len(whisper.archives))
}

func (whisper *Whisper) blockRangesSize() int {
	var blockRangesSize int
	for _, arc := range whisper.archives {
		blockRangesSize += BlockRangeSize * whisper.blockCount(arc)
	}
	return blockRangesSize
}

func (whisper *Whisper) bufferSize() int {
	if len(whisper.archives) == 0 {
		return 0
	}
	var bufSize int
	for i, arc := range whisper.archives[1:] {
		bufSize += arc.secondsPerPoint / whisper.archives[i].secondsPerPoint * 12
	}
	return bufSize
}

/* Return aggregation method */
func (whisper *Whisper) AggregationMethod() string {
	aggr := "unknown"
	switch whisper.aggregationMethod {
	case Average:
		aggr = "Average"
	case Sum:
		aggr = "Sum"
	case Last:
		aggr = "Last"
	case Max:
		aggr = "Max"
	case Min:
		aggr = "Min"
	}
	return aggr
}

/* Return max retention in seconds */
func (whisper *Whisper) MaxRetention() int {
	return whisper.maxRetention
}

/* Return xFilesFactor */
func (whisper *Whisper) XFilesFactor() float32 {
	return whisper.xFilesFactor
}

/* Return retentions */
func (whisper *Whisper) Retentions() []Retention {
	ret := make([]Retention, 0, 4)
	for _, archive := range whisper.archives {
		ret = append(ret, archive.Retention)
	}

	return ret
}

/*
  Update a value in the database.

  If the timestamp is in the future or outside of the maximum retention it will
  fail immediately.
*/
func (whisper *Whisper) Update(value float64, timestamp int) (err error) {
	// recover panics and return as error
	defer func() {
		if e := recover(); e != nil {
			err = errors.New(e.(string))
		}
	}()

	diff := int(time.Now().Unix()) - timestamp
	if !(diff < whisper.maxRetention && diff >= 0) {
		return fmt.Errorf("Timestamp not covered by any archives in this database")
	}
	var archive *archiveInfo
	var lowerArchives []*archiveInfo
	var i int
	for i, archive = range whisper.archives {
		if archive.MaxRetention() < diff {
			continue
		}
		lowerArchives = whisper.archives[i+1:] // TODO: investigate just returning the positions
		break
	}

	myInterval := timestamp - mod(timestamp, archive.secondsPerPoint)
	point := dataPoint{myInterval, value}

	_, err = whisper.file.WriteAt(point.Bytes(), whisper.getPointOffset(myInterval, archive))
	if err != nil {
		return err
	}

	higher := archive
	for _, lower := range lowerArchives {
		propagated, err := whisper.propagate(myInterval, higher, lower)
		if err != nil {
			return err
		} else if !propagated {
			break
		}
		higher = lower
	}

	return nil
}

func reversePoints(points []*TimeSeriesPoint) {
	size := len(points)
	end := size / 2

	for i := 0; i < end; i++ {
		points[i], points[size-i-1] = points[size-i-1], points[i]
	}
}

var Now = func() time.Time { return time.Now() }

func (whisper *Whisper) UpdateMany(points []*TimeSeriesPoint) (err error) {
	// recover panics and return as error

	// defer func() {
	// 	if e := recover(); e != nil {
	// 		err = fmt.Errorf("%s", e)
	// 	}
	// }()

	// sort the points, newest first
	reversePoints(points)
	sort.Stable(timeSeriesPointsNewestFirst{points})

	now := int(Now().Unix()) // TODO: danger of 2030 something overflow

	var currentPoints []*TimeSeriesPoint
	for _, archive := range whisper.archives {
		currentPoints, points = extractPoints(points, now, archive.MaxRetention())

		// log.Printf("archive.secondsPerPoint = %+v\n", archive.secondsPerPoint)
		// pretty.Println(currentPoints)

		if len(currentPoints) == 0 {
			continue
		}
		// reverse currentPoints
		reversePoints(currentPoints)
		if whisper.compressed {
			err = whisper.archiveUpdateManyCompressed(archive, currentPoints)
		} else {
			err = whisper.archiveUpdateMany(archive, currentPoints)
		}
		if err != nil {
			return
		}

		if len(points) == 0 { // nothing left to do
			break
		}
	}

	if whisper.compressed {
		if err := whisper.writeHeaderCompressed(); err != nil {
			return err
		}
	}

	return
}

func (whisper *Whisper) archiveUpdateMany(archive *archiveInfo, points []*TimeSeriesPoint) error {
	alignedPoints := alignPoints(archive, points)
	intervals, packedBlocks := packSequences(archive, alignedPoints)

	baseInterval := whisper.getBaseInterval(archive)
	if baseInterval == 0 {
		baseInterval = intervals[0]
	}

	for i := range intervals {
		myOffset := archive.PointOffset(baseInterval, intervals[i])
		bytesBeyond := int(myOffset-archive.End()) + len(packedBlocks[i])
		if bytesBeyond > 0 {
			pos := len(packedBlocks[i]) - bytesBeyond
			err := whisper.fileWriteAt(packedBlocks[i][:pos], myOffset)
			if err != nil {
				return err
			}
			err = whisper.fileWriteAt(packedBlocks[i][pos:], archive.Offset())
			if err != nil {
				return err
			}
		} else {
			err := whisper.fileWriteAt(packedBlocks[i], myOffset)
			if err != nil {
				return err
			}
		}
	}

	higher := archive
	lowerArchives := whisper.lowerArchives(archive)

	for _, lower := range lowerArchives {
		seen := make(map[int]bool)
		propagateFurther := false
		for _, point := range alignedPoints {
			interval := point.interval - mod(point.interval, lower.secondsPerPoint)
			if !seen[interval] {
				if propagated, err := whisper.propagate(interval, higher, lower); err != nil {
					panic("Failed to propagate")
				} else if propagated {
					propagateFurther = true
				}
			}
		}
		if !propagateFurther {
			break
		}
		higher = lower
	}
	return nil
}

func (whisper *Whisper) archiveUpdateManyCompressed(archive *archiveInfo, points []*TimeSeriesPoint) error {
	alignedPoints := alignPoints(archive, points)
	// intervals, packedBlocks := packSequences(archive, alignedPoints)

	// should write to buffer
	// yes
	// 	is buffer full
	// 	yes
	// 		archive buffer
	// 	write to buffer
	// no
	// 	write to archive
	//
	// propagate

	// 1542444689
	// baseInterval := whisper.getBaseInterval(archive)
	// baseInterval := archive.cblock.pn1.interval
	// if baseInterval == 0 {
	// 	baseInterval = alignedPoints[0].interval
	// }
	if !archive.hasBuffer() {
		return archive.appendToBlockAndRotate(alignedPoints)
	}

	var baseInterval int
	// if interval := unpackInt(archive.buffer); interval != 0 {
	// 	baseInterval = interval
	// } else if archive.cblock.pn1.interval > 0 {
	// 	baseInterval = archive.cblock.pn1.interval + archive.secondsPerPoint
	// }
	// log.Printf("archive.cblock.pn1.interval = %+v\n", archive.cblock.pn1.interval)
	if archive.cblock.pn1.interval != 0 {
		baseInterval = archive.next.Interval(archive.cblock.pn1.interval) - archive.next.secondsPerPoint
	} else if dps := unpackDataPointsStrict(archive.buffer); len(dps) > 0 {
		baseInterval = archive.next.Interval(dps[0].interval) - archive.next.secondsPerPoint
	}

	// log.Println("archiveUpdateManyCompressed")
	// pretty.Println(alignedPoints)

	bufferPointsCount := archive.next.secondsPerPoint / archive.secondsPerPoint
	for aindex := 0; aindex < len(alignedPoints); {
		dp := alignedPoints[aindex]
		if baseInterval == 0 {
			baseInterval = archive.next.Interval(dp.interval)
		}
		offset := bufferPointsCount - (baseInterval-dp.interval)/archive.secondsPerPoint
		not_overflow := 0 <= offset && offset < bufferPointsCount

		// log.Printf("baseInterval = %+v\n", baseInterval)
		// log.Printf("dp.interval = %+v\n", dp.interval)
		// log.Printf("offset = %+v\n", offset)
		// log.Printf("not_overflow = %+v\n", not_overflow)

		if not_overflow {
			aindex++
			copy(archive.buffer[offset*PointSize:], dp.Bytes())
			continue
		}

		// log.Printf(" ======= \n")

		// reset base interval
		baseInterval = 0

		// flush buffer
		dps := unpackDataPointsStrict(archive.buffer)

		// reset buffer
		for i := range archive.buffer {
			archive.buffer[i] = 0
		}
		// copy(archive.buffer[offset:], dp.Bytes())

		// buffer empty
		if len(dps) <= 0 {
			// baseInterval = dp.interval
			continue
		}

		// for _, dp := range dps {
		// 	// archive.cblock.p0.interval
		// }
		// if block is full
		// 		need to rotate/resize?
		if err := archive.appendToBlockAndRotate(dps); err != nil {
			return err
		}

		// offset := whisper.MetadataSize() + archive.offset + archive.cblock.index*archive.blockSize
		// continue

		// propagate
		if lower := whisper.getNextArchive(archive); lower != nil {
			// TODO: floor or ceil
			lowerIntervalStart := dps[0].interval - mod(dps[0].interval, lower.secondsPerPoint)

			// pretty.Println(dps)

			var knownValues []float64
			// currentInterval := dps[0].interval
			for _, dPoint := range dps {
				knownValues = append(knownValues, dPoint.value)
				// if dPoint.interval == currentInterval {
				// }
				// currentInterval += archive.secondsPerPoint
			}

			knownPercent := float32(len(knownValues)) / float32(lower.secondsPerPoint/archive.secondsPerPoint)
			// check we have enough data points to propagate a value
			if knownPercent >= whisper.xFilesFactor {
				aggregateValue := aggregate(whisper.aggregationMethod, knownValues)
				point := dataPoint{lowerIntervalStart, aggregateValue}
				if err := lower.appendToBlockAndRotate([]dataPoint{point}); err != nil {
					return err
				}
			}
		}
	}

	// log.Printf("archive.blockBuffer = %X\n", archive.blockBuffer)
	return nil
}

func (whisper *Whisper) getNextArchive(higher *archiveInfo) *archiveInfo {
	for i, archive := range whisper.archives {
		if archive == higher && i < len(whisper.archives) {
			return whisper.archives[i+1]
		}
	}
	return nil
}

func (archive *archiveInfo) appendToBlockAndRotate(dps []dataPoint) error {
	whisper := archive.whisper

	blockBuffer := make([]byte, archive.blockSize)

	// log.Println("appendToBlockAndRotate")
	// log.Printf("archive = %+v\n", archive.secondsPerPoint)
	// log.Println("data mark")
	// pretty.Println(dps)
	// log.Printf("archive.blockSize = %+v\n", archive.blockSize)

	for {
		// bw.index+a.cblock.lastByteOffset+1 >= a.blockOffset+a.blockSize

		size, left := archive.appendPointsToBlock(blockBuffer, dps...)
		// archive.blockDirty = true
		// archive.blockBufferIndex += written - 1

		// log.Printf("int64(archive.cblock.lastByteOffset-size+1) = %+v\n", int64(archive.cblock.lastByteOffset-size+1))
		// log.Printf("size = %+v\n", size)
		// log.Printf("blockBuffer[:size] = %08x\n", blockBuffer[:size])
		// log.Printf("left = %+v\n", left)

		if dps[0].interval > 1544295600 {
			// log.Printf("dps = %+v\n", dps)
			// log.Printf("size = %+v\n", size)
			// log.Printf("left = %+v\n", left)
		}

		// flush block
		end := size + 5
		if end >= len(blockBuffer) {
			end = len(blockBuffer) - 1
		}
		if err := whisper.fileWriteAt(blockBuffer[:end], int64(archive.cblock.lastByteOffset-size+1)); err != nil {
			return err
		}

		// // log.Printf("archive.cblock.index = %+v\n", archive.secondsPerPoint)
		b := make([]byte, 200)
		if _, err := whisper.file.ReadAt(b, int64(archive.blockOffset(archive.cblock.index))); err != nil {
			return err
		}

		// for i := 8; i < 200; i += 8 {
		// 	log.Printf("%08b\n", b[i-8:i])
		// }

		// {
		// 	var dst []dataPoint
		// 	dst, err := archive.readFromBlock(b, dst, dps[0].interval-100, dps[len(dps)-1].interval+100)
		// 	if err != nil {
		// 		panic(err)
		// 	}
		// 	// pretty.Println(archive.cblock)
		// 	// log.Printf("archive.last_byte = %+v\n", archive.cblock.lastByteOffset-archive.blockOffset(0))
		// 	// log.Println("---------")
		// 	// log.Println("data mark")
		// 	// pretty.Println(dst)
		// }

		if len(left) == 0 {
			// // flush block
			// if err := whisper.fileWriteAt(data, archive.cblock.lastByteOffset); err != nil {
			// 	return err
			// }
			// archive.cblock.lastByteOffset += written

			break
		}

		// archive.blockBufferIndex = 0

		log.Println("(archive.cblock.index + 1) % len(archive.blockRanges):", (archive.cblock.index+1)%len(archive.blockRanges))
		log.Println("(archive.cblock.index + 1):", (archive.cblock.index + 1))
		log.Println("len(archive.blockRanges):", len(archive.blockRanges))

		archive.blockRanges[archive.cblock.index].end = archive.cblock.pn1.interval
		var nblock blockInfo
		nblock.index = (archive.cblock.index + 1) % len(archive.blockRanges)
		nblock.lastByteBitPos = 7
		nblock.lastByteOffset = archive.blockOffset(nblock.index)

		// archive.blockRanges[nblock.index].start = left[0].interval
		// archive.blockRanges[nblock.index].end = 0
		archive.cblock = nblock

		// archive.initBlockRanges()

		dps = left
	}

	// // TODO: save header
	// for _, arc := range whisper.archives {
	// 	if arc.blockDirty {
	// 		if err := whisper.fileWriteAt(arc.blockBuffer[:arc.blockBufferIndex+1], int64(arc.cblock.lastByteOffset)); err != nil {
	// 			return err
	// 		}
	// 	}
	// 	arc.blockDirty = false
	// 	arc.blockBufferIndex = 0
	// }

	return nil
}

func extractPoints(points []*TimeSeriesPoint, now int, maxRetention int) (currentPoints []*TimeSeriesPoint, remainingPoints []*TimeSeriesPoint) {
	maxAge := now - maxRetention
	for i, point := range points {
		if point.Time < maxAge {
			if i > 0 {
				return points[:i-1], points[i-1:]
			} else {
				return []*TimeSeriesPoint{}, points
			}
		}
	}
	return points, remainingPoints
}

func alignPoints(archive *archiveInfo, points []*TimeSeriesPoint) []dataPoint {
	alignedPoints := make([]dataPoint, 0, len(points))
	positions := make(map[int]int)
	for _, point := range points {
		dPoint := dataPoint{point.Time - mod(point.Time, archive.secondsPerPoint), point.Value}
		if p, ok := positions[dPoint.interval]; ok {
			alignedPoints[p] = dPoint
		} else {
			alignedPoints = append(alignedPoints, dPoint)
			positions[dPoint.interval] = len(alignedPoints) - 1
		}
	}
	return alignedPoints
}

func packSequences(archive *archiveInfo, points []dataPoint) (intervals []int, packedBlocks [][]byte) {
	intervals = make([]int, 0)
	packedBlocks = make([][]byte, 0)
	for i, point := range points {
		if i == 0 || point.interval != intervals[len(intervals)-1]+archive.secondsPerPoint {
			intervals = append(intervals, point.interval)
			packedBlocks = append(packedBlocks, point.Bytes())
		} else {
			packedBlocks[len(packedBlocks)-1] = append(packedBlocks[len(packedBlocks)-1], point.Bytes()...)
		}
	}
	return
}

/*
	Calculate the offset for a given interval in an archive

	This method retrieves the baseInterval and the
*/
func (whisper *Whisper) getPointOffset(start int, archive *archiveInfo) int64 {
	baseInterval := whisper.getBaseInterval(archive)
	if baseInterval == 0 {
		return archive.Offset()
	}
	return archive.PointOffset(baseInterval, start)
}

func (whisper *Whisper) getBaseInterval(archive *archiveInfo) int {
	if whisper.compressed {
		return unpackInt(archive.buffer)
	}

	baseInterval, err := whisper.readInt(archive.Offset())
	if err != nil {
		panic("Failed to read baseInterval")
	}
	return baseInterval
}

func (whisper *Whisper) lowerArchives(archive *archiveInfo) (lowerArchives []*archiveInfo) {
	for i, lower := range whisper.archives {
		if lower.secondsPerPoint > archive.secondsPerPoint {
			return whisper.archives[i:]
		}
	}
	return
}

func (whisper *Whisper) propagate(timestamp int, higher, lower *archiveInfo) (bool, error) {
	lowerIntervalStart := timestamp - mod(timestamp, lower.secondsPerPoint)

	higherFirstOffset := whisper.getPointOffset(lowerIntervalStart, higher)

	// TODO: extract all this series extraction stuff
	higherPoints := lower.secondsPerPoint / higher.secondsPerPoint
	higherSize := higherPoints * PointSize
	relativeFirstOffset := higherFirstOffset - higher.Offset()
	relativeLastOffset := int64(mod(int(relativeFirstOffset+int64(higherSize)), higher.Size()))
	higherLastOffset := relativeLastOffset + higher.Offset()

	series, err := whisper.readSeries(higherFirstOffset, higherLastOffset, higher)
	if err != nil {
		return false, err
	}

	// and finally we construct a list of values
	knownValues := make([]float64, 0, len(series))
	currentInterval := lowerIntervalStart

	for _, dPoint := range series {
		if dPoint.interval == currentInterval {
			knownValues = append(knownValues, dPoint.value)
		}
		currentInterval += higher.secondsPerPoint
	}

	// propagate aggregateValue to propagate from neighborValues if we have enough known points
	if len(knownValues) == 0 {
		return false, nil
	}
	knownPercent := float32(len(knownValues)) / float32(len(series))
	if knownPercent < whisper.xFilesFactor { // check we have enough data points to propagate a value
		return false, nil
	} else {
		aggregateValue := aggregate(whisper.aggregationMethod, knownValues)
		point := dataPoint{lowerIntervalStart, aggregateValue}
		if _, err := whisper.file.WriteAt(point.Bytes(), whisper.getPointOffset(lowerIntervalStart, lower)); err != nil {
			return false, err
		}
		// if err := lower.appendToBlockAndRotate(&point); err != nil {
		// 	return false, err
		// }
	}
	return true, nil
}

func (whisper *Whisper) readSeries(start, end int64, archive *archiveInfo) ([]dataPoint, error) {
	var b []byte
	if start < end {
		b = make([]byte, end-start)
		err := whisper.fileReadAt(b, start)
		if err != nil {
			return nil, err
		}
	} else {
		b = make([]byte, archive.End()-start)
		err := whisper.fileReadAt(b, start)
		if err != nil {
			return nil, err
		}
		b2 := make([]byte, end-archive.Offset())
		err = whisper.fileReadAt(b2, archive.Offset())
		if err != nil {
			return nil, err
		}
		b = append(b, b2...)
	}
	return unpackDataPoints(b), nil
}

func (whisper *Whisper) fetchCompressed(start, end int64, archive *archiveInfo) ([]dataPoint, error) {
	log.Println("-------- fetchCompressed")
	log.Printf("archive.secondsPerPoint = %+v\n", archive.secondsPerPoint)
	pretty.Println(archive.blockRanges)
	var dst []dataPoint
	archive.dumpInfo()
	for _, block := range archive.blockRanges {
		log.Printf("block = %+v\n", block)
		if block.end >= int(start) && int(end) >= block.start {
			// matchedBlocks = append(matchedBlocks, block)
			buf := make([]byte, archive.blockSize)
			// log.Printf("whisper.blockOffset(archive, block.index) = %+v\n", archive.blockOffset(block.index))
			if err := whisper.fileReadAt(buf, int64(archive.blockOffset(block.index))); err != nil {
				return nil, fmt.Errorf("fetchCompressed: %s", err)
			}

			// for i := 8; i < archive.blockSize; i += 8 {
			// 	fmt.Printf("%08b\n", buf[i-8:i])
			// }

			// dps = append(dps, ...)
			var err error
			dst, err = archive.readFromBlock(buf, dst, int(start), int(end))
			if err != nil {
				return dst, err
			}
		}
	}
	// log.Printf("----\n")
	// pretty.Println(dst)
	if archive.hasBuffer() {
		// bstart, bend := archive.getBufferRange()
		// log.Printf("bstart = %+v\n", bstart)
		// log.Printf("bend = %+v\n", bend)
		// if bend >= int(start) && int(end) >= bstart {
		// 	dps := unpackDataPoints(archive.buffer)
		// 	for _, p := range dps {
		// 		if int(start) <= p.interval && p.interval <= int(end) {
		// 			dst = append(dst, p)
		// 		}
		// 	}
		// }
		dps := unpackDataPoints(archive.buffer)
		// log.Printf("archive.buffer = %x\n", archive.buffer)
		// pretty.Println(dps)
		for _, p := range dps {
			if p.interval != 0 && int(start) <= p.interval && p.interval <= int(end) {
				dst = append(dst, p)
			}
		}
	}
	return dst, nil
}

func (archive *archiveInfo) getBufferRange() (start, end int) {
	start = unpackInt(archive.buffer)
	points := archive.bufferSize / PointSize
	return start, start + points*archive.secondsPerPoint
}

func (whisper *Whisper) checkSeriesEmpty(start, end int64, archive *archiveInfo, fromTime, untilTime int) (bool, error) {
	if start < end {
		len := end - start
		return whisper.checkSeriesEmptyAt(start, len, fromTime, untilTime)
	}
	len := archive.End() - start
	empty, err := whisper.checkSeriesEmptyAt(start, len, fromTime, untilTime)
	if err != nil || !empty {
		return empty, err
	}
	return whisper.checkSeriesEmptyAt(archive.Offset(), end-archive.Offset(), fromTime, untilTime)

}

func (whisper *Whisper) checkSeriesEmptyAt(start, len int64, fromTime, untilTime int) (bool, error) {
	b1 := make([]byte, 4)
	// Read first point
	err := whisper.fileReadAt(b1, start)
	if err != nil {
		return false, err
	}
	pointTime := unpackInt(b1)
	if pointTime > fromTime && pointTime < untilTime {
		return false, nil
	}

	b2 := make([]byte, 4)
	// Read last point
	err = whisper.fileReadAt(b2, len-12)
	if err != nil {
		return false, err
	}
	pointTime = unpackInt(b1)
	if pointTime > fromTime && pointTime < untilTime {
		return false, nil
	}
	return true, nil
}

/*
  Calculate the starting time for a whisper db.
*/
func (whisper *Whisper) StartTime() int {
	now := int(Now().Unix()) // TODO: danger of 2030 something overflow
	return now - whisper.maxRetention
}

/*
  Fetch a TimeSeries for a given time span from the file.
*/
func (whisper *Whisper) Fetch(fromTime, untilTime int) (timeSeries *TimeSeries, err error) {
	now := int(Now().Unix()) // TODO: danger of 2030 something overflow
	if fromTime > untilTime {
		return nil, fmt.Errorf("Invalid time interval: from time '%d' is after until time '%d'", fromTime, untilTime)
	}
	oldestTime := whisper.StartTime()
	// range is in the future
	log.Println("fromTime:", fromTime)
	log.Println("untilTime:", untilTime)
	log.Println("now:", now)
	log.Println("oldestTime:", oldestTime)
	log.Printf("fromTime > now = %+v\n", fromTime > now)
	if fromTime > now {
		return nil, nil
	}
	// range is beyond retention
	log.Printf("untilTime < oldestTime = %+v\n", untilTime < oldestTime)
	if untilTime < oldestTime {
		return nil, nil
	}
	// if fromTime < oldestTime {
	// 	fromTime = oldestTime
	// }
	// if untilTime > now {
	// 	untilTime = now
	// }

	// TODO: improve this algorithm it's ugly
	diff := now - fromTime
	var archive *archiveInfo
	for _, archive = range whisper.archives {
		if archive.MaxRetention() >= diff {
			break
		}
	}

	fromInterval := archive.Interval(fromTime)
	untilInterval := archive.Interval(untilTime)

	var series []dataPoint
	// var err error
	log.Printf("whisper.compressed = %+v\n", whisper.compressed)
	log.Println("untilInterval:", untilInterval)
	log.Println("fromInterval:", fromInterval)
	if whisper.compressed {
		series, err = whisper.fetchCompressed(int64(fromInterval), int64(untilInterval), archive)

		if err != nil {
			return nil, err
		}

		irange := untilInterval - fromInterval
		values := make([]float64, irange/archive.secondsPerPoint)
		log.Printf("irange/archive.secondsPerPoint = %+v\n", irange/archive.secondsPerPoint)
		for i := range values {
			values[i] = math.NaN()
		}
		// currentInterval := fromInterval
		step := archive.secondsPerPoint
		// var index int
		for _, dPoint := range series {
			// if dPoint.interval > currentInterval {
			// }
			// log.Printf("(dPoint.interval-fromInterval)/archive.secondsPerPoint = %+v\n", (dPoint.interval-fromInterval)/archive.secondsPerPoint)
			// log.Printf("dPoint.interval = %+v\n", dPoint.interval)
			// log.Printf("fromInterval = %+v\n", fromInterval)
			// log.Printf("archive.secondsPerPoint = %+v\n", archive.secondsPerPoint)
			values[(dPoint.interval-fromInterval)/archive.secondsPerPoint] = dPoint.value
			// currentInterval = dPoint.interval + step
		}
		return &TimeSeries{fromInterval, untilInterval, step, values}, nil
	} else {
		baseInterval := whisper.getBaseInterval(archive)

		if baseInterval == 0 {
			step := archive.secondsPerPoint
			points := (untilInterval - fromInterval) / step
			values := make([]float64, points)
			for i := range values {
				values[i] = math.NaN()
			}
			return &TimeSeries{fromInterval, untilInterval, step, values}, nil
		}

		// Zero-length time range: always include the next point
		if fromInterval == untilInterval {
			untilInterval += archive.SecondsPerPoint()
		}

		fromOffset := archive.PointOffset(baseInterval, fromInterval)
		untilOffset := archive.PointOffset(baseInterval, untilInterval)

		series, err = whisper.readSeries(fromOffset, untilOffset, archive)
		if err != nil {
			return nil, err
		}

		values := make([]float64, len(series))
		for i := range values {
			values[i] = math.NaN()
		}
		currentInterval := fromInterval
		step := archive.secondsPerPoint

		for i, dPoint := range series {
			if dPoint.interval == currentInterval {
				values[i] = dPoint.value
			}
			currentInterval += step
		}
		return &TimeSeries{fromInterval, untilInterval, step, values}, nil
	}
}

/*
  Check a TimeSeries has a points for a given time span from the file.
*/
func (whisper *Whisper) CheckEmpty(fromTime, untilTime int) (exist bool, err error) {
	now := int(time.Now().Unix()) // TODO: danger of 2030 something overflow
	if fromTime > untilTime {
		return true, fmt.Errorf("Invalid time interval: from time '%d' is after until time '%d'", fromTime, untilTime)
	}
	oldestTime := whisper.StartTime()
	// range is in the future
	if fromTime > now {
		return true, nil
	}
	// range is beyond retention
	if untilTime < oldestTime {
		return true, nil
	}
	if fromTime < oldestTime {
		fromTime = oldestTime
	}
	if untilTime > now {
		untilTime = now
	}

	// TODO: improve this algorithm it's ugly
	diff := now - fromTime
	var archive *archiveInfo
	for _, archive = range whisper.archives {
		fromInterval := archive.Interval(fromTime)
		untilInterval := archive.Interval(untilTime)
		baseInterval := whisper.getBaseInterval(archive)

		if baseInterval == 0 {
			return true, nil
		}

		// Zero-length time range: always include the next point
		if fromInterval == untilInterval {
			untilInterval += archive.SecondsPerPoint()
		}

		fromOffset := archive.PointOffset(baseInterval, fromInterval)
		untilOffset := archive.PointOffset(baseInterval, untilInterval)

		empty, err := whisper.checkSeriesEmpty(fromOffset, untilOffset, archive, fromTime, untilTime)
		if err != nil || !empty {
			return empty, err
		}
		if archive.MaxRetention() >= diff {
			break
		}
	}
	return true, nil
}

func (whisper *Whisper) readInt(offset int64) (int, error) {
	// TODO: make errors better
	b := make([]byte, IntSize)
	_, err := whisper.file.ReadAt(b, offset)
	if err != nil {
		return 0, err
	}

	return unpackInt(b), nil
}

/*
  A retention level.

  Retention levels describe a given archive in the database. How detailed it is and how far back
  it records.
*/
type Retention struct {
	secondsPerPoint int
	numberOfPoints  int
}

func (retention *Retention) MaxRetention() int {
	return retention.secondsPerPoint * retention.numberOfPoints
}

func (retention *Retention) Size() int {
	return retention.numberOfPoints * PointSize
}

func (retention *Retention) SecondsPerPoint() int {
	return retention.secondsPerPoint
}

func (retention *Retention) NumberOfPoints() int {
	return retention.numberOfPoints
}

func NewRetention(secondsPerPoint, numberOfPoints int) Retention {
	return Retention{
		secondsPerPoint,
		numberOfPoints,
	}
}

type Retentions []*Retention

func (r Retentions) Len() int {
	return len(r)
}

func (r Retentions) Swap(i, j int) {
	r[i], r[j] = r[j], r[i]
}

type retentionsByPrecision struct{ Retentions }

func (r retentionsByPrecision) Less(i, j int) bool {
	return r.Retentions[i].secondsPerPoint < r.Retentions[j].secondsPerPoint
}

/*
  Describes a time series in a file.

  The only addition this type has over a Retention is the offset at which it exists within the
  whisper file.
*/
type archiveInfo struct {
	next *archiveInfo

	Retention `meta:"size:8"`
	offset    int `meta:"size:4"`

	whisper *Whisper

	bufferOffset int
	// blockOffset  int

	buffer []byte
	// bufferPoints []dataPoint
	bufferSize int

	blockRanges []blockRange // sorted by start

	// startTimestamp int
	// startIndex     int

	blockSize int `meta:"size:4"`
	// blockCount int // TODO: save
	cblock blockInfo // mostly for quick block write

	// blockBuffer      []byte
	// blockDirty       bool
	// blockBufferIndex int
}

type blockInfo struct {
	index int `meta:"size:4"`
	// crc32          int
	p0, pn1, pn2   dataPoint `meta:"size:12"` // pn1: point at len(block_points) - 1
	lastByte       byte      `meta:"size:4"`  // TODO: make it 1
	lastByteOffset int       `meta:"size:4"`
	lastByteBitPos int       `meta:"size:4"` // TODO: make it 1
}

type blockRange struct {
	index      int
	start, end int
}

func (a *archiveInfo) blockOffset(blockIndex int) int {
	// whisper.
	return a.offset + blockIndex*a.blockSize
}

func (archive *archiveInfo) dumpInfo() {
	fmt.Printf("number_of_points:  %d\n", archive.numberOfPoints)
	fmt.Printf("seconds_per_point: %d\n", archive.secondsPerPoint)
	fmt.Printf("block_size:        %d\n", archive.blockSize)
	fmt.Printf("buffer_size:       %d\n", archive.bufferSize)
	fmt.Printf("cblock\n")
	fmt.Printf("  index:     %d\n", archive.cblock.index)
	fmt.Printf("  p[0].interval:     %d\n", archive.cblock.p0.interval)
	fmt.Printf("  p[n-2].interval:   %d\n", archive.cblock.pn2.interval)
	fmt.Printf("  p[n-1].interval:   %d\n", archive.cblock.pn1.interval)
	fmt.Printf("  last_byte:         %08b\n", archive.cblock.lastByte)
	fmt.Printf("  last_byte_offset:  %d\n", archive.cblock.lastByteOffset)
	fmt.Printf("  last_byte_bit_pos: %d\n", archive.cblock.lastByteBitPos)
}

// func (a *archiveInfo) nextWritableBlockOffset() int {
// 	if a.cblock.offset {

// 	}
// }

// func (whisper *Whisper) writeToBlock(a *archiveInfo, dps []dataPoint) error {
// 	if len(dps) == 0 {
// 		return nil
// 	}

// 	var start int
// 	for i, dp := range dps {
// 		// shadowInterval := dp.interval / a.secondsPerPoint
// 		// 1541811660 -- 1541811660 + second_per_point
// 		// 1541811660 60 -> 25696861
// 		// 1541811720 60 -> 25696862 - 1
// 		// 1541815200 60 -> 25696920 - 58
// 		if dp.interval <= a.cblock.p0.interval+whisper.pointsPerBlock*a.secondsPerPoint && i < len(dps)-1 {
// 			continue
// 		}

// 		// seal cblock
// 		// open new block
// 		if start < i {
// 			data := a.compressPoints(dps[start:i])
// 			if err := whisper.fileWriteAt(data, a.cblock.lastByteOffset); err != nil {
// 				return err
// 			}
// 			a.cblock = blockInfo{
// 				lastByteBitPos: 7,
// 			}
// 		}
// 		start = i

// 		// if a.cblock.p0.lastByteOffset == 0 {
// 		// 	a.cblock.p0 = dps[0]
// 		// 	if len(dps) > 2 {
// 		// 		a.cblock.pn2 = dps[1]
// 		// 		a.cblock.pn1 = dps[2]
// 		// 	} else {
// 		// 		a.cblock.pn1 = dps[1]
// 		// 		a.cblock.pn2 = dps[0]
// 		// 	}
// 		// }
// 	}
// }

func (archive *archiveInfo) Offset() int64 {
	return int64(archive.offset)
}

func (archive *archiveInfo) PointOffset(baseInterval, interval int) int64 {
	timeDistance := interval - baseInterval
	pointDistance := timeDistance / archive.secondsPerPoint
	byteDistance := pointDistance * PointSize
	myOffset := archive.Offset() + int64(mod(byteDistance, archive.Size()))

	return myOffset
}

func (archive *archiveInfo) End() int64 {
	return archive.Offset() + int64(archive.Size())
}

func (archive *archiveInfo) Interval(time int) int {
	return time - mod(time, archive.secondsPerPoint) + archive.secondsPerPoint
}

type TimeSeries struct {
	fromTime  int
	untilTime int
	step      int
	values    []float64
}

func (ts *TimeSeries) FromTime() int {
	return ts.fromTime
}

func (ts *TimeSeries) UntilTime() int {
	return ts.untilTime
}

func (ts *TimeSeries) Step() int {
	return ts.step
}

func (ts *TimeSeries) Values() []float64 {
	return ts.values
}

func (ts *TimeSeries) Points() []TimeSeriesPoint {
	points := make([]TimeSeriesPoint, len(ts.values))
	for i, value := range ts.values {
		points[i] = TimeSeriesPoint{Time: ts.fromTime + ts.step*i, Value: value}
	}
	return points
}

func (ts *TimeSeries) String() string {
	return fmt.Sprintf("TimeSeries{'%v' '%-v' %v %v}", time.Unix(int64(ts.fromTime), 0), time.Unix(int64(ts.untilTime), 0), ts.step, ts.values)
}

type TimeSeriesPoint struct {
	Time  int
	Value float64
}

type timeSeriesPoints []*TimeSeriesPoint

func (p timeSeriesPoints) Len() int {
	return len(p)
}

func (p timeSeriesPoints) Swap(i, j int) {
	p[i], p[j] = p[j], p[i]
}

type timeSeriesPointsNewestFirst struct {
	timeSeriesPoints
}

func (p timeSeriesPointsNewestFirst) Less(i, j int) bool {
	return p.timeSeriesPoints[i].Time > p.timeSeriesPoints[j].Time
}

type dataPoint struct {
	interval int
	value    float64
}

func (point *dataPoint) Bytes() []byte {
	b := make([]byte, PointSize)
	packInt(b, point.interval, 0)
	packFloat64(b, point.value, IntSize)
	return b
}

func sum(values []float64) float64 {
	result := 0.0
	for _, value := range values {
		result += value
	}
	return result
}

func aggregate(method AggregationMethod, knownValues []float64) float64 {
	switch method {
	case Average:
		return sum(knownValues) / float64(len(knownValues))
	case Sum:
		return sum(knownValues)
	case Last:
		return knownValues[len(knownValues)-1]
	case Max:
		max := knownValues[0]
		for _, value := range knownValues {
			if value > max {
				max = value
			}
		}
		return max
	case Min:
		min := knownValues[0]
		for _, value := range knownValues {
			if value < min {
				min = value
			}
		}
		return min
	}
	panic("Invalid aggregation method")
}

func packInt(b []byte, v, i int) int {
	binary.BigEndian.PutUint32(b[i:i+IntSize], uint32(v))
	return IntSize
}

func packFloat32(b []byte, v float32, i int) int {
	binary.BigEndian.PutUint32(b[i:i+FloatSize], math.Float32bits(v))
	return FloatSize
}

func packFloat64(b []byte, v float64, i int) int {
	binary.BigEndian.PutUint64(b[i:i+Float64Size], math.Float64bits(v))
	return Float64Size
}

func unpackInt(b []byte) int {
	return int(binary.BigEndian.Uint32(b))
}

func unpackFloat32(b []byte) float32 {
	return math.Float32frombits(binary.BigEndian.Uint32(b))
}

func unpackFloat64(b []byte) float64 {
	return math.Float64frombits(binary.BigEndian.Uint64(b))
}

func unpackArchiveInfo(b []byte) *archiveInfo {
	return &archiveInfo{
		Retention: Retention{unpackInt(b[IntSize : IntSize*2]), unpackInt(b[IntSize*2 : IntSize*3])},
		offset:    unpackInt(b[:IntSize]),
	}
}

func unpackDataPoint(b []byte) dataPoint {
	return dataPoint{unpackInt(b[0:IntSize]), unpackFloat64(b[IntSize:PointSize])}
}

func unpackDataPoints(b []byte) (series []dataPoint) {
	series = make([]dataPoint, 0, len(b)/PointSize)
	for i := 0; i < len(b); i += PointSize {
		series = append(series, unpackDataPoint(b[i:i+PointSize]))
	}
	return
}

func unpackDataPointsStrict(b []byte) (series []dataPoint) {
	series = make([]dataPoint, 0, len(b)/PointSize)
	for i := 0; i < len(b); i += PointSize {
		dp := unpackDataPoint(b[i : i+PointSize])
		if dp.interval == 0 {
			continue
		}
		series = append(series, dp)
	}
	return
}

/*
	Implementation of modulo that works like Python
	Thanks @timmow for this
*/
func mod(a, b int) int {
	return a - (b * int(math.Floor(float64(a)/float64(b))))
}
