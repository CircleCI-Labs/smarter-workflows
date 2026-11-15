package impactmap

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
)

func isGzip(data []byte) bool {
	return len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b
}

// gunzipData decompresses gzip bytes with hostile-input limits.
func gunzipData(data []byte) ([]byte, error) {
	if len(data) > MaxMapCompressedBytes {
		return nil, fmt.Errorf("impact map: compressed file is %d bytes, over the limit of %d", len(data), MaxMapCompressedBytes)
	}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("impact map: gzip: %w", err)
	}
	defer zr.Close()
	raw, err := io.ReadAll(io.LimitReader(zr, int64(MaxMapDecompressedBytes)+1))
	if err != nil {
		return nil, fmt.Errorf("impact map: gzip: %w", err)
	}
	if len(raw) > MaxMapDecompressedBytes {
		return nil, fmt.Errorf("impact map: decompressed size exceeds %d bytes", MaxMapDecompressedBytes)
	}
	// Tiny compressed payloads that expand enormously are zip-bomb shaped; large
	// maps legitimately exceed 32× (JSON gzip ratios above ~100× are common).
	if len(data) < 64<<10 && len(data) > 0 && len(raw) > len(data)*MaxDecompressRatio {
		return nil, fmt.Errorf("impact map: decompressed size %d bytes is over %d× the compressed size", len(raw), MaxDecompressRatio)
	}
	return raw, nil
}

// gzipData compresses JSON for on-disk storage.
func gzipData(raw []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if err != nil {
		return nil, err
	}
	zw.Name = ""
	zw.ModTime = gzipEpoch
	if _, err := zw.Write(raw); err != nil {
		_ = zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
