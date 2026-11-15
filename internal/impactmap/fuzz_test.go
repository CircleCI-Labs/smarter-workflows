package impactmap

import (
	"testing"
)

func FuzzParseV2Indices(f *testing.F) {
	f.Add([]byte{0x01, 0x02, 0x80, 0x03})
	f.Add([]byte(nil))
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 64*1024 {
			return
		}
		_, _ = readIndexDeltas(raw, 10_000, MaxIndicesPerJob)
	})
}

func FuzzParseMap(f *testing.F) {
	f.Add([]byte(`{"version":2,"revision":"x","created_at":"t","method":"atime","files":[],"dirs":[],"jobs":{}}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1024*1024 {
			return
		}
		_, _ = Parse(data)
	})
}
