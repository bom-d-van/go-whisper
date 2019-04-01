package main

import (
	"log"
	"os"

	whisper "github.com/go-graphite/go-whisper"
)

func init() {
	log.SetFlags(log.Lshortfile)
}

func main() {
	file1 := os.Args[1]

	db1, err := whisper.OpenWithOptions(file1, &whisper.Options{})
	if err != nil {
		panic(err)
	}

	db1.CheckIntegrity()
}
