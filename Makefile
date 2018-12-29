
SHELL := '/bin/bash'

convert:
	go test -c -o cwhisper
	for f in `find data -iname '*.wsp'`; do echo $$f; ./cwhisper -test.run TestCompressedWhisperReadWrite4 -file $$f; done

test: convert
	go build -o verify bin/verify.go
	for f in `find data -iname '*.wsp'`; do echo $$f; ./verify $$f $$f.cwsp; done 2>&1 | grep -B 100 error:

clean:
	rm cwhisper icheck verify
