// Command genfeatures extracts the vanilla feature datapack subset from the
// Mojang bundler jar into one deterministic archive embedded by worldgen.
package main

import (
	"archive/zip"
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var prefixes = []string{
	"data/minecraft/worldgen/configured_feature/",
	"data/minecraft/worldgen/placed_feature/",
	"data/minecraft/worldgen/biome/",
	"data/minecraft/tags/block/",
}

func main() {
	server := flag.String("server", "server.jar", "Mojang bundler server.jar")
	output := flag.String("output", "internal/worldgen/feature_data.zip", "output archive")
	flag.Parse()

	outer, err := zip.OpenReader(*server)
	if err != nil {
		fatal(err)
	}
	defer outer.Close()
	var innerBytes []byte
	for _, file := range outer.File {
		if strings.HasPrefix(file.Name, "META-INF/versions/") && strings.HasSuffix(file.Name, ".jar") {
			innerBytes, err = readZipFile(file)
			if err != nil {
				fatal(err)
			}
			break
		}
	}
	if len(innerBytes) == 0 {
		fatal(fmt.Errorf("%s contains no inner server jar", *server))
	}
	inner, err := zip.NewReader(bytes.NewReader(innerBytes), int64(len(innerBytes)))
	if err != nil {
		fatal(err)
	}
	type entry struct {
		name string
		data []byte
	}
	entries := make([]entry, 0, 1024)
	for _, file := range inner.File {
		if file.FileInfo().IsDir() || !strings.HasSuffix(file.Name, ".json") || !selected(file.Name) {
			continue
		}
		data, err := readZipFile(file)
		if err != nil {
			fatal(err)
		}
		entries = append(entries, entry{name: file.Name, data: data})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	if len(entries) < 500 {
		fatal(fmt.Errorf("only %d feature datapack files found; wrong server version", len(entries)))
	}

	if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
		fatal(err)
	}
	tmp := *output + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		header.SetModTime(time.Unix(0, 0).UTC())
		writer, err := zw.CreateHeader(header)
		if err != nil {
			fatal(err)
		}
		if _, err := writer.Write(entry.data); err != nil {
			fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		fatal(err)
	}
	if err := f.Sync(); err != nil {
		fatal(err)
	}
	if err := f.Close(); err != nil {
		fatal(err)
	}
	if err := os.Rename(tmp, *output); err != nil {
		fatal(err)
	}
	fmt.Printf("wrote %s with %d vanilla datapack files\n", *output, len(entries))
}

func selected(name string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func readZipFile(file *zip.File) ([]byte, error) {
	r, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "genfeatures:", err)
	os.Exit(1)
}
