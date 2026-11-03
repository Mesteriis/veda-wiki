package store

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func testPNG(t *testing.T, shade uint8) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, 1, 1))
	img.SetGray(0, 0, color.Gray{Y: shade})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func countDirSyncs(t *testing.T) *int {
	t.Helper()
	original := syncDir
	count := new(int)
	syncDir = func(dir string) error { *count++; return original(dir) }
	t.Cleanup(func() { syncDir = original })
	return count
}

func TestWriteBlobsSyncsDirectoryOncePerBatch(t *testing.T) {
	s := testStore(t)
	syncs := countDirSyncs(t)
	blobs := map[string][]byte{"a.png": testPNG(t, 1), "b.png": testPNG(t, 2), "c.png": testPNG(t, 3)}
	refs, err := s.WriteBlobs(blobs)
	if err != nil {
		t.Fatal(err)
	}
	if *syncs != 1 {
		t.Fatal("directory syncs for one batch", *syncs)
	}
	for name, data := range blobs {
		stored, err := s.ReadBlob(refs[name].SHA)
		if err != nil || !bytes.Equal(stored, data) {
			t.Fatal("blob not stored", name, err)
		}
	}
	if _, err = s.WriteBlobs(blobs); err != nil {
		t.Fatal(err)
	}
	if _, err = s.WriteBlob(blobs["a.png"]); err != nil {
		t.Fatal(err)
	}
	if *syncs != 1 {
		t.Fatal("unchanged blobs synced the directory", *syncs)
	}
	if _, err = s.WriteBlobs(map[string][]byte{"a.png": blobs["a.png"], "d.png": testPNG(t, 4)}); err != nil {
		t.Fatal(err)
	}
	if *syncs != 2 {
		t.Fatal("mixed batch directory syncs", *syncs-1)
	}
}
