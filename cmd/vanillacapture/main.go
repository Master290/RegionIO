// Command vanillacapture runs the official server for fixed chunks and writes a
// block-by-block parity fixture consumed by internal/world tests.
package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"regionio/internal/world"
)

const fixtureMagic = "RIOPAR02"

type chunkPos struct{ x, z int32 }

func main() {
	serverJar := flag.String("server", "server.jar", "Mojang bundler server.jar")
	java := flag.String("java", "java", "Java 25 executable")
	seed := flag.Int64("seed", 12345, "world seed")
	chunksFlag := flag.String("chunks", "0,0;1,0;0,1;-1,-1", "semicolon-separated chunk coordinates")
	output := flag.String("output", "internal/world/testdata/vanilla_overworld_12345.bin", "output fixture")
	keep := flag.Bool("keep", false, "keep the temporary vanilla world")
	flag.Parse()

	chunks, err := parseChunks(*chunksFlag)
	if err != nil {
		fatal(err)
	}
	jar, err := filepath.Abs(*serverJar)
	if err != nil {
		fatal(err)
	}
	if _, err := os.Stat(jar); err != nil {
		fatal(fmt.Errorf("server jar: %w", err))
	}
	if err := requireJava25(*java); err != nil {
		fatal(err)
	}

	work, err := os.MkdirTemp("", "regionio-vanilla-capture-")
	if err != nil {
		fatal(err)
	}
	if !*keep {
		defer os.RemoveAll(work)
	} else {
		fmt.Fprintf(os.Stderr, "vanilla workspace: %s\n", work)
	}
	if err := prepareServer(work, *seed); err != nil {
		fatal(err)
	}
	if err := runServer(*java, jar, work, chunks); err != nil {
		fatal(err)
	}
	overworld := filepath.Join(work, "world", "dimensions", "minecraft", "overworld")
	if err := writeFixture(overworld, *output, *seed, chunks); err != nil {
		fatal(err)
	}
	fmt.Printf("wrote %s: seed %d, %d chunks\n", *output, *seed, len(chunks))
}

func requireJava25(java string) error {
	cmd := exec.Command(java, "-version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("Java 25 is required: %w", err)
	}
	version := string(out)
	if !strings.Contains(version, `version "25`) && !strings.Contains(version, `openjdk 25`) {
		return fmt.Errorf("Java 25 is required; %s -version returned %q", java, strings.TrimSpace(version))
	}
	return nil
}

func prepareServer(dir string, seed int64) error {
	if err := os.WriteFile(filepath.Join(dir, "eula.txt"), []byte("eula=true\n"), 0o644); err != nil {
		return err
	}
	properties := fmt.Sprintf("level-seed=%d\nonline-mode=false\nspawn-protection=0\nview-distance=2\nsimulation-distance=2\nmax-tick-time=-1\n", seed)
	return os.WriteFile(filepath.Join(dir, "server.properties"), []byte(properties), 0o644)
}

func runServer(java, jar, dir string, chunks []chunkPos) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, java, "-Xms1G", "-Xmx2G", "-jar", jar, "nogui")
	cmd.Dir = dir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return err
	}

	events := make(chan string, 4)
	scanDone := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(output)
		for scanner.Scan() {
			line := scanner.Text()
			fmt.Println(line)
			if strings.Contains(line, "Done (") || strings.Contains(line, "Saved the game") {
				events <- line
			}
		}
		close(events)
		scanDone <- scanner.Err()
	}()
	if err := waitFor(events, "Done (", 5*time.Minute); err != nil {
		_ = stdin.Close()
		_ = cmd.Wait()
		return fmt.Errorf("vanilla startup: %w", err)
	}
	for _, chunk := range chunks {
		x, z := int64(chunk.x)*16, int64(chunk.z)*16
		if _, err := fmt.Fprintf(stdin, "execute in minecraft:overworld run forceload add %d %d\n", x, z); err != nil {
			return err
		}
	}
	// Force-load tickets are processed asynchronously by the server tick. Give
	// terrain, decoration, and lighting time to reach FULL before flushing.
	time.Sleep(20 * time.Second)
	if _, err := io.WriteString(stdin, "save-all flush\n"); err != nil {
		return err
	}
	if err := waitFor(events, "Saved the game", 5*time.Minute); err != nil {
		return fmt.Errorf("vanilla save: %w", err)
	}
	if _, err := io.WriteString(stdin, "stop\n"); err != nil {
		return err
	}
	_ = stdin.Close()
	if err := cmd.Wait(); err != nil {
		return err
	}
	if err := <-scanDone; err != nil {
		return err
	}
	return ctx.Err()
}

func waitFor(lines <-chan string, text string, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				return errors.New("server exited before expected log message")
			}
			if strings.Contains(line, text) {
				return nil
			}
		case <-timer.C:
			return fmt.Errorf("timeout waiting for %q", text)
		}
	}
}

func writeFixture(worldDir, output string, seed int64, chunks []chunkPos) error {
	store, err := world.NewStore(worldDir)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return err
	}
	tmp := output + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	var header [24]byte
	copy(header[:8], fixtureMagic)
	binary.BigEndian.PutUint64(header[8:16], uint64(seed))
	binary.BigEndian.PutUint32(header[16:20], uint32(len(chunks)))
	binary.BigEndian.PutUint32(header[20:24], 4790)
	if _, err := f.Write(header[:]); err != nil {
		return err
	}
	var value [8]byte
	for _, pos := range chunks {
		chunk, err := store.LoadVanillaChunk(pos.x, pos.z)
		if err != nil {
			return fmt.Errorf("load vanilla chunk (%d,%d): %w", pos.x, pos.z, err)
		}
		binary.BigEndian.PutUint32(value[:4], uint32(pos.x))
		binary.BigEndian.PutUint32(value[4:], uint32(pos.z))
		if _, err := f.Write(value[:]); err != nil {
			return err
		}
		for y := world.MinY; y < world.MinY+world.WorldHeight; y++ {
			for z := 0; z < 16; z++ {
				for x := 0; x < 16; x++ {
					binary.BigEndian.PutUint16(value[:2], chunk.GetBlock(x, y, z))
					if _, err := f.Write(value[:2]); err != nil {
						return err
					}
				}
			}
		}
		for y := world.MinY; y < world.MinY+world.WorldHeight; y += 4 {
			for z := 0; z < 16; z += 4 {
				for x := 0; x < 16; x += 4 {
					binary.BigEndian.PutUint16(value[:2], chunk.GetBiome(x, y, z))
					if _, err := f.Write(value[:2]); err != nil {
						return err
					}
				}
			}
		}
		heightmaps, err := store.LoadVanillaHeightmaps(pos.x, pos.z)
		if err != nil {
			return fmt.Errorf("load vanilla heightmaps (%d,%d): %w", pos.x, pos.z, err)
		}
		for _, heightmap := range heightmaps {
			for _, y := range heightmap {
				binary.BigEndian.PutUint16(value[:2], uint16(y))
				if _, err := f.Write(value[:2]); err != nil {
					return err
				}
			}
		}
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, output); err != nil {
		return err
	}
	ok = true
	return nil
}

func parseChunks(raw string) ([]chunkPos, error) {
	parts := strings.Split(raw, ";")
	chunks := make([]chunkPos, 0, len(parts))
	seen := make(map[chunkPos]bool)
	for _, part := range parts {
		coords := strings.Split(strings.TrimSpace(part), ",")
		if len(coords) != 2 {
			return nil, fmt.Errorf("invalid chunk %q", part)
		}
		x, err := strconv.ParseInt(strings.TrimSpace(coords[0]), 10, 32)
		if err != nil {
			return nil, err
		}
		z, err := strconv.ParseInt(strings.TrimSpace(coords[1]), 10, 32)
		if err != nil {
			return nil, err
		}
		pos := chunkPos{int32(x), int32(z)}
		if !seen[pos] {
			chunks = append(chunks, pos)
			seen[pos] = true
		}
	}
	if len(chunks) == 0 {
		return nil, errors.New("no chunks requested")
	}
	return chunks, nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "vanillacapture:", err)
	os.Exit(1)
}
