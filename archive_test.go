package goselfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"testing"
)

const binaryContent = "#!/bin/sh\necho updated\n"

func makeTarGz(t *testing.T, entries map[string]string) []byte {
	t.Helper()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range entries {
		header := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func makeZip(t *testing.T, entries map[string]string) []byte {
	t.Helper()

	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for name, content := range entries {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractBinaryFromTarGz(t *testing.T) {
	data := makeTarGz(t, map[string]string{
		"README.md": "docs",
		"LICENSE":   "license",
		"tool":      binaryContent,
	})

	for _, name := range []string{"tool_1.0.0_linux_amd64.tar.gz", "tool.tgz"} {
		got, err := extractBinary(Asset{Name: name}, data, "tool")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(got) != binaryContent {
			t.Errorf("%s: got %q", name, got)
		}
	}
}

func TestExtractBinaryFromZip(t *testing.T) {
	data := makeZip(t, map[string]string{
		"README.md": "docs",
		"tool":      binaryContent,
	})

	got, err := extractBinary(Asset{Name: "tool_1.0.0_windows_amd64.zip"}, data, "tool")
	if err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	if string(got) != binaryContent {
		t.Errorf("got %q", got)
	}
}

// A Windows archive names the executable tool.exe while Config.Binary stays
// "tool", so the .exe suffix has to be tolerated.
func TestExtractBinaryAcceptsExeSuffix(t *testing.T) {
	data := makeZip(t, map[string]string{"tool.exe": binaryContent})

	got, err := extractBinary(Asset{Name: "tool_1.0.0_windows_amd64.zip"}, data, "tool")
	if err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	if string(got) != binaryContent {
		t.Errorf("got %q", got)
	}
}

func TestExtractBinaryFindsNestedEntry(t *testing.T) {
	data := makeTarGz(t, map[string]string{"tool_1.0.0/bin/tool": binaryContent})

	got, err := extractBinary(Asset{Name: "tool.tar.gz"}, data, "tool")
	if err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	if string(got) != binaryContent {
		t.Errorf("got %q", got)
	}
}

func TestExtractBinaryAcceptsBareBinary(t *testing.T) {
	got, err := extractBinary(Asset{Name: "tool_linux_amd64"}, []byte(binaryContent), "tool")
	if err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	if string(got) != binaryContent {
		t.Errorf("got %q", got)
	}
}

// An entry naming a traversal path has nothing to escape into: only the base
// name is compared and archive paths never build an output path.
func TestExtractBinaryIgnoresTraversalPaths(t *testing.T) {
	data := makeTarGz(t, map[string]string{
		"../../../../etc/passwd": "malicious",
		"tool":                   binaryContent,
	})

	got, err := extractBinary(Asset{Name: "tool.tar.gz"}, data, "tool")
	if err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	if string(got) != binaryContent {
		t.Errorf("extracted the wrong entry: %q", got)
	}
}

func TestExtractBinaryErrors(t *testing.T) {
	cases := []struct {
		name  string
		asset Asset
		data  []byte
	}{
		{
			name:  "missing from tar.gz",
			asset: Asset{Name: "tool.tar.gz"},
			data:  makeTarGz(t, map[string]string{"README.md": "docs"}),
		},
		{
			name:  "missing from zip",
			asset: Asset{Name: "tool.zip"},
			data:  makeZip(t, map[string]string{"README.md": "docs"}),
		},
		{
			name:  "empty entry",
			asset: Asset{Name: "tool.tar.gz"},
			data:  makeTarGz(t, map[string]string{"tool": ""}),
		},
		{
			name:  "empty bare binary",
			asset: Asset{Name: "tool_linux_amd64"},
			data:  nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := extractBinary(c.asset, c.data, "tool")
			if !errors.Is(err, ErrBinaryNotFound) {
				t.Fatalf("got %v, want ErrBinaryNotFound", err)
			}
		})
	}
}

func TestExtractBinaryRejectsCorruptArchives(t *testing.T) {
	for _, name := range []string{"tool.tar.gz", "tool.zip"} {
		if _, err := extractBinary(Asset{Name: name}, []byte("not an archive"), "tool"); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}
