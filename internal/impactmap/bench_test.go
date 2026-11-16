package impactmap

import (
	"testing"
	"time"
)

func benchMap(jobCount, totalFiles, filesPerJob int) (*Map, error) {
	return SyntheticMap(jobCount, totalFiles, filesPerJob)
}

func BenchmarkMapV2Marshal2000x10k(b *testing.B) {
	m, err := benchMap(2000, 10_000, 10_000)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := m.Marshal(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMapV2Parse2000x10k(b *testing.B) {
	m, err := benchMap(2000, 10_000, 10_000)
	if err != nil {
		b.Fatal(err)
	}
	data, err := m.Marshal()
	if err != nil {
		b.Fatal(err)
	}
	b.Logf("compressed %d bytes", len(data))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Parse(data); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMapV1Parse2000x10k(b *testing.B) {
	m, err := benchMap(2000, 10_000, 10_000)
	if err != nil {
		b.Fatal(err)
	}
	data, err := m.MarshalV1()
	if err != nil {
		b.Fatal(err)
	}
	b.Logf("v1 JSON %d bytes", len(data))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := parseV1(data); err != nil {
			b.Fatal(err)
		}
	}
}

func TestBenchMapV2FitsTarget(t *testing.T) {
	if testing.Short() {
		t.Skip("slow bench check")
	}
	m, err := benchMap(2000, 10_000, 10_000)
	if err != nil {
		t.Fatal(err)
	}
	data, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("v2 gzip size: %d bytes", len(data))
	if len(data) > 20<<20 {
		t.Fatalf("compressed map is %d bytes, want under 20 MiB", len(data))
	}
	const runs = 3
	best := time.Duration(1<<63 - 1)
	for i := 0; i < runs; i++ {
		start := time.Now()
		if _, err := Parse(data); err != nil {
			t.Fatal(err)
		}
		elapsed := time.Since(start)
		if elapsed < best {
			best = elapsed
		}
	}
	t.Logf("v2 decode (best of %d): %v", runs, best)
	if best > 500*time.Millisecond {
		t.Fatalf("decode took %v, want under 0.5s", best)
	}
}
