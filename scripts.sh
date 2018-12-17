
for f in `find data/interfaces -iname '*.wsp'`; do echo $(( `wc -c $f.cwsp | sed 's/^ *//' | cut -d ' ' -f 1` * 1.0/ `wc -c $f | sed 's/^ *//' | cut -d ' ' -f 1`)) $f; done | sort -n

for f in `find data/interfaces -iname '*.wsp'`; do go test -run TestCompressedWhisperReadWrite4 -file $f; done

for f in `find data/interfaces -iname '*.wsp'`; do echo $f; go run bin/verify.go $f $f.cwsp; done
