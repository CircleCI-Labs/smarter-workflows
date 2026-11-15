package impactmap

import (
	"encoding/base64"
	"fmt"
	"sort"
)

// encodeIndexDeltas turns sorted unique indices into base64-encoded delta varints.
func encodeIndexDeltas(indices []int) (string, error) {
	if len(indices) == 0 {
		return "", nil
	}
	sorted := append([]int(nil), indices...)
	sort.Ints(sorted)
	for i, idx := range sorted {
		if idx < 0 {
			return "", fmt.Errorf("impact map: negative index %d", idx)
		}
		if i > 0 && sorted[i-1] == idx {
			return "", fmt.Errorf("impact map: duplicate index %d", idx)
		}
	}
	var buf []byte
	prev := -1
	for _, idx := range sorted {
		var delta uint64
		if prev < 0 {
			delta = uint64(idx)
		} else {
			if idx <= prev {
				return "", fmt.Errorf("impact map: indices must be strictly increasing")
			}
			delta = uint64(idx - prev)
		}
		buf = appendUvarint(buf, delta)
		prev = idx
	}
	return base64.StdEncoding.EncodeToString(buf), nil
}

// decodeIndexDeltas reverses encodeIndexDeltas. tableLen is the path table size.
func decodeIndexDeltas(enc string, tableLen int, max int) ([]int, error) {
	if enc == "" {
		return nil, nil
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return nil, fmt.Errorf("impact map: index blob: %w", err)
	}
	indices, err := readIndexDeltas(raw, tableLen, max)
	if err != nil {
		return nil, fmt.Errorf("impact map: index blob: %w", err)
	}
	return indices, nil
}

func readIndexDeltas(raw []byte, tableLen, max int) ([]int, error) {
	if max == 0 {
		max = MaxIndicesPerJob
	}
	var (
		out  []int
		prev = -1
		off  int
	)
	for off < len(raw) {
		delta, n := readUvarint(raw[off:])
		if n <= 0 {
			return nil, fmt.Errorf("truncated varint at byte %d", off)
		}
		off += n
		var idx int
		if prev < 0 {
			idx = int(delta)
		} else {
			if delta == 0 {
				return nil, fmt.Errorf("zero delta after index %d", prev)
			}
			idx = prev + int(delta)
		}
		if idx < 0 || idx >= tableLen {
			return nil, fmt.Errorf("index %d is out of range [0,%d)", idx, tableLen)
		}
		if prev >= 0 && idx <= prev {
			return nil, fmt.Errorf("indices are not strictly increasing")
		}
		out = append(out, idx)
		prev = idx
		if len(out) > max {
			return nil, fmt.Errorf("more than %d indices", max)
		}
	}
	if off != len(raw) {
		return nil, fmt.Errorf("trailing bytes after indices")
	}
	return out, nil
}

func appendUvarint(buf []byte, x uint64) []byte {
	for x >= 0x80 {
		buf = append(buf, byte(x)|0x80)
		x >>= 7
	}
	return append(buf, byte(x))
}

func readUvarint(buf []byte) (uint64, int) {
	var (
		x    uint64
		s    uint
		i    int
		b    byte
		done bool
	)
	for i, b = range buf {
		if b < 0x80 {
			x |= uint64(b) << s
			done = true
			break
		}
		x |= uint64(b&0x7f) << s
		s += 7
		if s >= 64 {
			return 0, 0
		}
	}
	if !done {
		return 0, 0
	}
	return x, i + 1
}
