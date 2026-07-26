package goselfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"
)

// maxBinarySize bounds what is read out of an archive entry. The archive has
// already been checksum-verified by this point, so this guards against a
// malformed archive rather than a hostile one.
const maxBinarySize = 512 << 20

// extractBinary pulls the named executable out of a release asset.
//
// Only an entry's base name is compared and archive paths are never used to
// build an output path, so an entry naming ../../etc/passwd has nothing to
// escape into — it simply fails to match.
func extractBinary(asset Asset, data []byte, binary string) ([]byte, error) {
	switch {
	case hasExtension(asset.Name, ".tar.gz", ".tgz"):
		return fromTarGz(data, binary)
	case hasExtension(asset.Name, ".zip"):
		return fromZip(data, binary)
	default:
		// A bare binary published as its own asset.
		if len(data) == 0 {
			return nil, fmt.Errorf("%w: %s is empty", ErrBinaryNotFound, asset.Name)
		}
		return data, nil
	}
}

func hasExtension(name string, extensions ...string) bool {
	lower := strings.ToLower(name)
	for _, extension := range extensions {
		if strings.HasSuffix(lower, extension) {
			return true
		}
	}
	return false
}

func fromTarGz(data []byte, binary string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("read gzip: %w", err)
	}
	defer func() { _ = gz.Close() }()

	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%w: %s not in archive", ErrBinaryNotFound, binary)
		}
		if err != nil {
			return nil, fmt.Errorf("read tar: %w", err)
		}
		if header.Typeflag != tar.TypeReg || !isBinaryEntry(header.Name, binary) {
			continue
		}
		return readEntry(reader, binary)
	}
}

func fromZip(data []byte, binary string) ([]byte, error) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("read zip: %w", err)
	}

	for _, entry := range archive.File {
		if entry.FileInfo().IsDir() || !isBinaryEntry(entry.Name, binary) {
			continue
		}
		return readZipEntry(entry, binary)
	}

	return nil, fmt.Errorf("%w: %s not in archive", ErrBinaryNotFound, binary)
}

func readZipEntry(entry *zip.File, binary string) ([]byte, error) {
	file, err := entry.Open()
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", entry.Name, err)
	}
	defer func() { _ = file.Close() }()

	return readEntry(file, binary)
}

// isBinaryEntry matches an archive entry against the wanted binary name,
// tolerating the .exe suffix that a Windows build carries.
func isBinaryEntry(entryName, binary string) bool {
	base := path.Base(filepath.ToSlash(entryName))
	return base == binary || base == binary+".exe"
}

func readEntry(reader io.Reader, binary string) ([]byte, error) {
	content, err := io.ReadAll(io.LimitReader(reader, maxBinarySize))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", binary, err)
	}
	if len(content) == 0 {
		return nil, fmt.Errorf("%w: %s is empty in the archive", ErrBinaryNotFound, binary)
	}
	return content, nil
}
