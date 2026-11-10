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

// ParseUUID parses a canonical 36-char dashed or 32-char hex UUID string.
func ParseUUID(s string) ([16]byte, error) {
	var out [16]byte
	cleaned := strings.ReplaceAll(strings.TrimSpace(s), "-", "")
	if len(cleaned) != 32 {
		return out, fmt.Errorf("invalid UUID length %d", len(cleaned))
	}
	for i := 0; i < 16; i++ {
		b, err := strconv.ParseUint(cleaned[i*2:i*2+2], 16, 8)
		if err != nil {
			return out, fmt.Errorf("invalid UUID hex at byte %d: %w", i, err)
		}
		out[i] = byte(b)
	}
	return out, nil
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
		case "enforce-secure-chat":
			b, err := strconv.ParseBool(strings.TrimSpace(val))
			if err != nil {
				return base, fmt.Errorf("server.properties:%d: invalid enforce-secure-chat %q", lineNo, val)
			}
			cfg.EnforceSecureChat = b
		case "hide-online-players":
			b, err := strconv.ParseBool(strings.TrimSpace(val))
			if err != nil {
				return base, fmt.Errorf("server.properties:%d: invalid hide-online-players %q", lineNo, val)
			}
			cfg.HideOnlinePlayers = b
		case "accepts-transfers":
			b, err := strconv.ParseBool(strings.TrimSpace(val))
			if err != nil {
				return base, fmt.Errorf("server.properties:%d: invalid accepts-transfers %q", lineNo, val)
			}
			cfg.AcceptsTransfers = b
		case "resource-pack":
			if trimmed := strings.TrimSpace(val); trimmed != "" {
				if cfg.ResourcePack == nil {
					cfg.ResourcePack = &ResourcePackInfo{}
				}
				cfg.ResourcePack.URL = trimmed
			}
		case "resource-pack-id":
			if trimmed := strings.TrimSpace(val); trimmed != "" {
				u, err := ParseUUID(trimmed)
				if err != nil {
					return base, fmt.Errorf("server.properties:%d: invalid resource-pack-id %q", lineNo, val)
				}
				if cfg.ResourcePack == nil {
					cfg.ResourcePack = &ResourcePackInfo{}
				}
				cfg.ResourcePack.ID = u
			}
		case "resource-pack-sha1":
			if trimmed := strings.TrimSpace(val); trimmed != "" {
				if cfg.ResourcePack == nil {
					cfg.ResourcePack = &ResourcePackInfo{}
				}
				cfg.ResourcePack.Hash = trimmed
			}
		case "require-resource-pack":
			b, err := strconv.ParseBool(strings.TrimSpace(val))
			if err != nil {
				return base, fmt.Errorf("server.properties:%d: invalid require-resource-pack %q", lineNo, val)
			}
			if cfg.ResourcePack == nil {
				cfg.ResourcePack = &ResourcePackInfo{}
			}
			cfg.ResourcePack.Required = b
		case "resource-pack-prompt":
			if trimmed := strings.TrimSpace(val); trimmed != "" {
				if cfg.ResourcePack == nil {
					cfg.ResourcePack = &ResourcePackInfo{}
				}
				if comp, err := ParseChatComponentJSON(trimmed); err == nil {
					cfg.ResourcePack.Prompt = &comp
				} else {
					comp := TextComponent(trimmed)
					cfg.ResourcePack.Prompt = &comp
				}
			}
		case "bug-report-link":
			if trimmed := strings.TrimSpace(val); trimmed != "" {
				cfg.BugReportLink = trimmed
			}
		case "simulation-distance":
			sd, err := strconv.Atoi(strings.TrimSpace(val))
			if err != nil || sd < 2 || sd > 32 {
				return base, fmt.Errorf("server.properties:%d: invalid simulation-distance %q", lineNo, val)
			}
			cfg.SimulationDistance = sd
		case "gamemode":
			gm := strings.ToLower(strings.TrimSpace(val))
			switch gm {
			case "0", "survival", "s":
				cfg.Gamemode = "survival"
			case "1", "creative", "c":
				cfg.Gamemode = "creative"
			case "2", "adventure", "a":
				cfg.Gamemode = "adventure"
			case "3", "spectator", "sp":
				cfg.Gamemode = "spectator"
			default:
				return base, fmt.Errorf("server.properties:%d: invalid gamemode %q", lineNo, val)
			}
		case "force-gamemode":
			b, err := strconv.ParseBool(strings.TrimSpace(val))
			if err != nil {
				return base, fmt.Errorf("server.properties:%d: invalid force-gamemode %q", lineNo, val)
			}
			cfg.ForceGamemode = b
		case "pvp":
			b, err := strconv.ParseBool(strings.TrimSpace(val))
			if err != nil {
				return base, fmt.Errorf("server.properties:%d: invalid pvp %q", lineNo, val)
			}
			cfg.PvP = b
		case "allow-flight":
			b, err := strconv.ParseBool(strings.TrimSpace(val))
			if err != nil {
				return base, fmt.Errorf("server.properties:%d: invalid allow-flight %q", lineNo, val)
			}
			cfg.AllowFlight = b
		case "spawn-protection":
			sp, err := strconv.Atoi(strings.TrimSpace(val))
			if err != nil || sp < 0 {
				return base, fmt.Errorf("server.properties:%d: invalid spawn-protection %q", lineNo, val)
			}
			cfg.SpawnProtection = sp
		case "max-world-size":
			mws, err := strconv.Atoi(strings.TrimSpace(val))
			if err != nil || mws < 1 || mws > 29999984 {
				return base, fmt.Errorf("server.properties:%d: invalid max-world-size %q", lineNo, val)
			}
			cfg.MaxWorldSize = mws
		case "max-tick-time":
			mtt, err := strconv.ParseInt(strings.TrimSpace(val), 10, 64)
			if err != nil {
				return base, fmt.Errorf("server.properties:%d: invalid max-tick-time %q", lineNo, val)
			}
			cfg.MaxTickTime = mtt
		case "rate-limit":
			rl, err := strconv.Atoi(strings.TrimSpace(val))
			if err != nil || rl < 0 {
				return base, fmt.Errorf("server.properties:%d: invalid rate-limit %q", lineNo, val)
			}
			cfg.RateLimit = rl
		case "enable-rcon":
			b, err := strconv.ParseBool(strings.TrimSpace(val))
			if err != nil {
				return base, fmt.Errorf("server.properties:%d: invalid enable-rcon %q", lineNo, val)
			}
			cfg.EnableRCON = b
		case "rcon.port":
			port, err := strconv.Atoi(strings.TrimSpace(val))
			if err != nil || port < 1 || port > 65535 {
				return base, fmt.Errorf("server.properties:%d: invalid rcon.port %q", lineNo, val)
			}
			cfg.RCONPort = port
		case "rcon.password":
			cfg.RCONPassword = val
		case "broadcast-rcon-to-ops":
			b, err := strconv.ParseBool(strings.TrimSpace(val))
			if err != nil {
				return base, fmt.Errorf("server.properties:%d: invalid broadcast-rcon-to-ops %q", lineNo, val)
			}
			cfg.BroadcastRCONToOps = b
		case "enable-query":
			b, err := strconv.ParseBool(strings.TrimSpace(val))
			if err != nil {
				return base, fmt.Errorf("server.properties:%d: invalid enable-query %q", lineNo, val)
			}
			cfg.EnableQuery = b
		case "query.port":
			port, err := strconv.Atoi(strings.TrimSpace(val))
			if err != nil || port < 1 || port > 65535 {
				return base, fmt.Errorf("server.properties:%d: invalid query.port %q", lineNo, val)
			}
			cfg.QueryPort = port
		case "enable-status":
			b, err := strconv.ParseBool(strings.TrimSpace(val))
			if err != nil {
				return base, fmt.Errorf("server.properties:%d: invalid enable-status %q", lineNo, val)
			}
			cfg.EnableStatus = b
		case "sync-chunk-writes":
			b, err := strconv.ParseBool(strings.TrimSpace(val))
			if err != nil {
				return base, fmt.Errorf("server.properties:%d: invalid sync-chunk-writes %q", lineNo, val)
			}
			cfg.SyncChunkWrites = b
		case "generate-structures":
			b, err := strconv.ParseBool(strings.TrimSpace(val))
			if err != nil {
				return base, fmt.Errorf("server.properties:%d: invalid generate-structures %q", lineNo, val)
			}
			cfg.GenerateStructures = b
		case "level-type":
			if trimmed := strings.TrimSpace(val); trimmed != "" {
				cfg.LevelType = trimmed
			}
		case "op-permission-level":
			lvl, err := strconv.Atoi(strings.TrimSpace(val))
			if err != nil || lvl < 1 || lvl > 4 {
				return base, fmt.Errorf("server.properties:%d: invalid op-permission-level %q", lineNo, val)
			}
			cfg.OpPermissionLevel = lvl
		case "function-permission-level":
			lvl, err := strconv.Atoi(strings.TrimSpace(val))
			if err != nil || lvl < 1 || lvl > 4 {
				return base, fmt.Errorf("server.properties:%d: invalid function-permission-level %q", lineNo, val)
			}
			cfg.FunctionPermissionLevel = lvl
		case "player-idle-timeout":
			to, err := strconv.Atoi(strings.TrimSpace(val))
			if err != nil || to < 0 {
				return base, fmt.Errorf("server.properties:%d: invalid player-idle-timeout %q", lineNo, val)
			}
			cfg.PlayerIdleTimeout = to
		case "prevent-proxy-connections":
			b, err := strconv.ParseBool(strings.TrimSpace(val))
			if err != nil {
				return base, fmt.Errorf("server.properties:%d: invalid prevent-proxy-connections %q", lineNo, val)
			}
			cfg.PreventProxyConnections = b
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
	gm := cfg.Gamemode
	if gm == "" {
		gm = "survival"
	}
	simDist := cfg.SimulationDistance
	if simDist < 2 {
		simDist = 10
	}
	rconPort := cfg.RCONPort
	if rconPort <= 0 {
		rconPort = 25575
	}
	queryPort := cfg.QueryPort
	if queryPort <= 0 {
		queryPort = 25565
	}
	maxWorldSize := cfg.MaxWorldSize
	if maxWorldSize <= 0 {
		maxWorldSize = 29999984
	}
	lvlType := cfg.LevelType
	if lvlType == "" {
		lvlType = "minecraft:normal"
	}
	opLvl := cfg.OpPermissionLevel
	if opLvl < 1 || opLvl > 4 {
		opLvl = 4
	}
	fnLvl := cfg.FunctionPermissionLevel
	if fnLvl < 1 || fnLvl > 4 {
		fnLvl = 2
	}
	_, err := fmt.Fprintf(w,
		"#Minecraft server properties\n"+
			"server-ip=%s\n"+
			"server-port=%d\n"+
			"level-name=%s\n"+
			"level-seed=%d\n"+
			"level-type=%s\n"+
			"generate-structures=%t\n"+
			"view-distance=%d\n"+
			"simulation-distance=%d\n"+
			"max-players=%d\n"+
			"motd=%s\n"+
			"online-mode=%t\n"+
			"white-list=%t\n"+
			"network-compression-threshold=%d\n"+
			"difficulty=%s\n"+
			"gamemode=%s\n"+
			"force-gamemode=%t\n"+
			"hardcore=%t\n"+
			"pvp=%t\n"+
			"allow-flight=%t\n"+
			"spawn-protection=%d\n"+
			"max-world-size=%d\n"+
			"max-tick-time=%d\n"+
			"rate-limit=%d\n"+
			"enable-rcon=%t\n"+
			"rcon.port=%d\n"+
			"rcon.password=%s\n"+
			"broadcast-rcon-to-ops=%t\n"+
			"enable-query=%t\n"+
			"query.port=%d\n"+
			"enable-status=%t\n"+
			"sync-chunk-writes=%t\n"+
			"op-permission-level=%d\n"+
			"function-permission-level=%d\n"+
			"player-idle-timeout=%d\n"+
			"prevent-proxy-connections=%t\n",
		cfg.Host,
		cfg.Port,
		cfg.WorldDir,
		cfg.WorldSeed,
		lvlType,
		cfg.GenerateStructures,
		cfg.MaxViewDistance,
		simDist,
		cfg.MaxPlayers,
		strings.ReplaceAll(cfg.MOTD, "\n", `\n`),
		cfg.OnlineMode,
		cfg.EnforceWhitelist,
		cfg.CompressionThreshold,
		diff,
		gm,
		cfg.ForceGamemode,
		cfg.Hardcore,
		cfg.PvP,
		cfg.AllowFlight,
		cfg.SpawnProtection,
		maxWorldSize,
		cfg.MaxTickTime,
		cfg.RateLimit,
		cfg.EnableRCON,
		rconPort,
		cfg.RCONPassword,
		cfg.BroadcastRCONToOps,
		cfg.EnableQuery,
		queryPort,
		cfg.EnableStatus,
		cfg.SyncChunkWrites,
		opLvl,
		fnLvl,
		cfg.PlayerIdleTimeout,
		cfg.PreventProxyConnections,
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
