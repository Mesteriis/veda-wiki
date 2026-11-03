package importer

import (
	"strings"
	"testing"
)

func TestWikiMetadataStrictSchema(t *testing.T) {
	m, e := ParseMetadata([]byte(metadataFixture + "url: https://example.org\n"))
	if e != nil || m.Title != "Handbook" {
		t.Fatal(m, e)
	}
	for _, body := range []string{metadataFixture + "visibility: public\n", metadataFixture + "---\ntitle: second\n", metadataFixture + "image: ../outside.png\n", metadataFixture + "title: Duplicate\n", "schema_version: 1\nkind: wiki\ntitle: &name Heading\ndescription: *name\n", metadataFixture + "url: javascript:alert(1)\n", strings.Replace(metadataFixture, "schema_version: 1", "schema_version: 2", 1)} {
		if _, e := ParseMetadata([]byte(body)); e == nil {
			t.Fatalf("accepted invalid YAML: %s", body)
		}
	}
}
