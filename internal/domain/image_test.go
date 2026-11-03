package domain

import (
	"bytes"
	"net/http"
	"testing"
)

// sniffImage replaces net/http.DetectContentType so domain stays free of
// net/http; both must agree on every accepted image type.
func TestSniffImageMatchesStdlib(t *testing.T) {
	samples := [][]byte{
		append([]byte("\x89PNG\r\n\x1a\n"), 1, 2, 3),
		{0xff, 0xd8, 0xff, 0xe0, 0, 1},
		[]byte("GIF87a rest"),
		[]byte("GIF89a rest"),
		[]byte("RIFF\x01\x02\x03\x04WEBPVP8 x"),
		[]byte("RIFF\x01\x02\x03\x04WEBPXXXX"),
		[]byte("RIFF\x01\x02\x03\x04WAVEfmt "),
		[]byte("<html>"),
		[]byte("  \x89PNG\r\n\x1a\n"),
		[]byte("GIF8"),
		bytes.Repeat([]byte{0}, 20),
	}
	for _, b := range samples {
		want := http.DetectContentType(b)
		switch want {
		case "image/png", "image/jpeg", "image/webp", "image/gif":
		default:
			want = ""
		}
		if got := sniffImage(b); got != want {
			t.Fatalf("sniffImage(%q) = %q, stdlib %q", b, got, want)
		}
	}
}
