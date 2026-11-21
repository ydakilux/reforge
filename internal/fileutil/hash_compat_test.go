package fileutil_test

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/zeebo/blake3"

	"github.com/ydakilux/reforge/internal/fileutil"
)

// legacyPartialHash is the original Seek-based algorithm. The ReadAt-based
// rewrite must stay byte-for-byte compatible, otherwise every hash already
// stored in users' databases would change and files would be reconverted.
func legacyPartialHash(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	size := info.Size()
	chunk := int64(fileutil.HashChunkSize)
	h := blake3.New()
	buf := make([]byte, chunk)
	n, _ := io.ReadFull(f, buf)
	h.Write(buf[:n])
	if size > chunk*2 {
		f.Seek(size/2, 0) //nolint:errcheck
		n, _ = io.ReadFull(f, buf)
		h.Write(buf[:n])
	}
	if size > chunk {
		f.Seek(-chunk, 2) //nolint:errcheck
		n, _ = io.ReadFull(f, buf)
		h.Write(buf[:n])
	}
	sz := make([]byte, 8)
	binary.LittleEndian.PutUint64(sz, uint64(size))
	h.Write(sz)
	return fmt.Sprintf("%x", h.Sum(nil))
}

func TestGetFileHash_PartialMatchesLegacyAlgorithm(t *testing.T) {
	c := fileutil.HashChunkSize
	for _, size := range []int{0, 1, 1000, c, c + 1, 2 * c, 2*c + 12345} {
		path := filepath.Join(t.TempDir(), "f.bin")
		data := make([]byte, size)
		for i := range data {
			data[i] = byte(i*31 + i/7)
		}
		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Fatal(err)
		}
		got, err := fileutil.GetFileHash(path, true)
		if err != nil {
			t.Fatalf("size %d: %v", size, err)
		}
		if want := legacyPartialHash(t, path); got != want {
			t.Errorf("size %d: hash %s != legacy %s", size, got, want)
		}
	}
}

func TestGetFileHash_DirectoryIsAnError(t *testing.T) {
	if _, err := fileutil.GetFileHash(t.TempDir(), true); err == nil {
		t.Error("expected an error when hashing a directory (read failure must not be swallowed)")
	}
}
