package server

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// OpEntry represents an operator record in ops.json.
type OpEntry struct {
	UUID                string `json:"uuid"`
	Name                string `json:"name"`
	Level               int    `json:"level"`
	BypassesPlayerLimit bool   `json:"bypassesPlayerLimit"`
}

// WhitelistEntry represents an allowed player record in whitelist.json.
type WhitelistEntry struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
}

// FormatUUID formats a 16-byte UUID in canonical 8-4-4-4-12 hex representation.
func FormatUUID(u [16]byte) string {
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
}

// ParseProperties reads a server.properties-formatted stream and applies the
// recognized keys onto base. Unknown keys are ignored so vanilla files load
// without error; malformed values for known keys return an error.
func ParseProperties(r io.Reader, base Config) (Config, error) {
	cfg := base
	scanner := bufio.NewScanner(r)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		key, val, ok := splitPropertyLine(line)
		if !ok {
			return base, fmt.Errorf("server.properties:%d: missing '=' or ':' separator", lineNo)
		}
		val = unescapePropertyValue(val)
		switch strings.ToLower(key) {
		case "server-port", "port":
			port, err := strconv.Atoi(strings.TrimSpace(val))
			if err != nil || port < 1 || port > 65535 {
				return base, fmt.Errorf("server.properties:%d: invalid port %q", lineNo, val)
			}
			cfg.Port = port
		case "server-ip", "host":
			if trimmed := strings.TrimSpace(val); trimmed != "" {
				cfg.Host = trimmed
			}
		case "level-seed", "seed":
			trimmed := strings.TrimSpace(val)
			if trimmed != "" {
				cfg.WorldSeed = parseSeedProperty(trimmed)
			}
		case "view-distance":
			vd, err := strconv.Atoi(strings.TrimSpace(val))
			if err != nil || vd < 2 || vd > 16 {
				return base, fmt.Errorf("server.properties:%d: invalid view-distance %q", lineNo, val)
			}
			cfg.MaxViewDistance = vd
		case "max-players":
			mp, err := strconv.Atoi(strings.TrimSpace(val))
			if err != nil || mp < 1 {
				return base, fmt.Errorf("server.properties:%d: invalid max-players %q", lineNo, val)
			}
			cfg.MaxPlayers = mp
		case "motd":
			cfg.MOTD = val
		case "online-mode":
			b, err := strconv.ParseBool(strings.TrimSpace(val))
			if err != nil {
				return base, fmt.Errorf("server.properties:%d: invalid online-mode %q", lineNo, val)
			}
			cfg.OnlineMode = b
		case "white-list", "whitelist", "enforce-whitelist":
			b, err := strconv.ParseBool(strings.TrimSpace(val))
			if err != nil {
				return base, fmt.Errorf("server.properties:%d: invalid white-list %q", lineNo, val)
			}
			cfg.EnforceWhitelist = b
		case "level-name", "world-dir":
			if trimmed := strings.TrimSpace(val); trimmed != "" {
				cfg.WorldDir = trimmed
			}
		case "network-compression-threshold":
			ct, err := strconv.Atoi(strings.TrimSpace(val))
			if err != nil {
				return base, fmt.Errorf("server.properties:%d: invalid network-compression-threshold %q", lineNo, val)
			}
			cfg.CompressionThreshold = ct
		case "difficulty":
			d, ok := ParseDifficulty(val)
			if !ok {
				return base, fmt.Errorf("server.properties:%d: invalid difficulty %q", lineNo, val)
			}
			cfg.Difficulty = d.String()
		case "hardcore":
			b, err := strconv.ParseBool(strings.TrimSpace(val))
			if err != nil {
				return base, fmt.Errorf("server.properties:%d: invalid hardcore %q", lineNo, val)
			}
			cfg.Hardcore = b
		}
	}
	if err := scanner.Err(); err != nil {
		return base, err
	}
	return cfg, nil
}

func splitPropertyLine(line string) (key, value string, ok bool) {
	escaped := false
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if ch == '=' || ch == ':' {
			return strings.TrimSpace(line[:i]), strings.TrimLeft(line[i+1:], " \t"), true
		}
	}
	return "", "", false
}

func unescapePropertyValue(s string) string {
	if !strings.ContainsRune(s, '\\') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			switch s[i] {
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			default:
				b.WriteByte(s[i])
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// parseSeedProperty parses a numeric (decimal or 0x hex) seed, or falls back to
// Java's String.hashCode() for non-numeric strings as vanilla Minecraft does.
func parseSeedProperty(raw string) int64 {
	if v, err := strconv.ParseInt(raw, 0, 64); err == nil {
		return v
	}
	var h int32
	for _, r := range raw {
		h = 31*h + r
	}
	return int64(h)
}

// LoadPropertiesFile loads server.properties from path onto base. If path does
// not exist, base is returned without error.
func LoadPropertiesFile(path string, base Config) (Config, error) {
	if path == "" {
		return base, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return base, nil
		}
		return base, err
	}
	defer f.Close()
	return ParseProperties(f, base)
}

// WriteProperties serializes cfg in server.properties format.
func WriteProperties(w io.Writer, cfg Config) error {
	diff := cfg.Difficulty
	if diff == "" {
		diff = "normal"
	}
	_, err := fmt.Fprintf(w,
		"#Minecraft server properties\n"+
			"server-ip=%s\n"+
			"server-port=%d\n"+
			"level-name=%s\n"+
			"level-seed=%d\n"+
			"view-distance=%d\n"+
			"max-players=%d\n"+
			"motd=%s\n"+
			"online-mode=%t\n"+
			"white-list=%t\n"+
			"network-compression-threshold=%d\n"+
			"difficulty=%s\n"+
			"hardcore=%t\n",
		cfg.Host,
		cfg.Port,
		cfg.WorldDir,
		cfg.WorldSeed,
		cfg.MaxViewDistance,
		cfg.MaxPlayers,
		strings.ReplaceAll(cfg.MOTD, "\n", `\n`),
		cfg.OnlineMode,
		cfg.EnforceWhitelist,
		cfg.CompressionThreshold,
		diff,
		cfg.Hardcore,
	)
	return err
}

// SavePropertiesFile writes cfg to path in server.properties format.
func SavePropertiesFile(path string, cfg Config) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return WriteProperties(f, cfg)
}

// ParseOpsJSON decodes ops.json content. Both vanilla object arrays and plain
// string username arrays are accepted.
func ParseOpsJSON(data []byte) ([]OpEntry, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, nil
	}
	var entries []OpEntry
	if err := json.Unmarshal(data, &entries); err == nil {
		for i := range entries {
			entries[i].Name = strings.TrimSpace(entries[i].Name)
			if entries[i].Name == "" {
				return nil, fmt.Errorf("ops.json: entry %d has empty name", i)
			}
			if entries[i].UUID == "" {
				entries[i].UUID = FormatUUID(OfflineUUID(entries[i].Name))
			}
			if entries[i].Level == 0 {
				entries[i].Level = 4
			}
		}
		return entries, nil
	}
	var names []string
	if err := json.Unmarshal(data, &names); err != nil {
		return nil, fmt.Errorf("ops.json: %w", err)
	}
	entries = make([]OpEntry, 0, len(names))
	for i, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, fmt.Errorf("ops.json: entry %d has empty name", i)
		}
		entries = append(entries, OpEntry{
			UUID:  FormatUUID(OfflineUUID(name)),
			Name:  name,
			Level: 4,
		})
	}
	return entries, nil
}

// LoadOpsFile reads ops.json from path. A missing file returns an empty list.
func LoadOpsFile(path string) ([]OpEntry, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return ParseOpsJSON(data)
}

// SaveOpsFile writes entries to path as indented JSON.
func SaveOpsFile(path string, entries []OpEntry) error {
	if entries == nil {
		entries = []OpEntry{}
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// ParseWhitelistJSON decodes whitelist.json content. Both vanilla object arrays
// and plain string username arrays are accepted.
func ParseWhitelistJSON(data []byte) ([]WhitelistEntry, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, nil
	}
	var entries []WhitelistEntry
	if err := json.Unmarshal(data, &entries); err == nil {
		for i := range entries {
			entries[i].Name = strings.TrimSpace(entries[i].Name)
			if entries[i].Name == "" {
				return nil, fmt.Errorf("whitelist.json: entry %d has empty name", i)
			}
			if entries[i].UUID == "" {
				entries[i].UUID = FormatUUID(OfflineUUID(entries[i].Name))
			}
		}
		return entries, nil
	}
	var names []string
	if err := json.Unmarshal(data, &names); err != nil {
		return nil, fmt.Errorf("whitelist.json: %w", err)
	}
	entries = make([]WhitelistEntry, 0, len(names))
	for i, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, fmt.Errorf("whitelist.json: entry %d has empty name", i)
		}
		entries = append(entries, WhitelistEntry{
			UUID: FormatUUID(OfflineUUID(name)),
			Name: name,
		})
	}
	return entries, nil
}

// LoadWhitelistFile reads whitelist.json from path. A missing file returns an
// empty list.
func LoadWhitelistFile(path string) ([]WhitelistEntry, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return ParseWhitelistJSON(data)
}

// SaveWhitelistFile writes entries to path as indented JSON.
func SaveWhitelistFile(path string, entries []WhitelistEntry) error {
	if entries == nil {
		entries = []WhitelistEntry{}
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
