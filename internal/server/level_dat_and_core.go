package server

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/world"
)

// Vanilla 26.1.2 level.dat and Anvil DataVersion constants.
const (
	LevelDatDataVersion    int32  = 4790
	LevelDatNBTVersion     int32  = 19133
	LevelDatVersionName    string = "26.1.2"
	LevelDatVersionID      int32  = 4790
	LevelDatSeries         string = "main"
	LegacyLevelFlatCutoff  int32  = 2844 // 21w43a (1.18): chunk NBT moved from root."Level" to flat root
	RegionIOGenVersion2612 int32  = 55
)

// Standard vanilla chunk ticket type names and default levels/TTLs.
const (
	TicketTypeStart        = "start"
	TicketTypeDragon       = "dragon"
	TicketTypePlayer       = "player"
	TicketTypeForced       = "forced"
	TicketTypePortal       = "portal"
	TicketTypePostTeleport = "post_teleport"
	TicketTypeUnknown      = "unknown"

	TicketLevelStart        = 22
	TicketLevelDragon       = 24
	TicketLevelPortal       = 30
	TicketLevelPlayer       = 31
	TicketLevelForced       = 31
	TicketLevelPostTeleport = 33
	TicketLevelUnknown      = 33
	TicketLevelMax          = 44

	TicketPortalTTLTicks       int64 = 300
	TicketPostTeleportTTLTicks int64 = 5
	TicketUnknownTTLTicks      int64 = 1
)

// Deterministic server tick phases in execution order.
const (
	TickPhaseTimeWeather   = "time_weather"
	TickPhaseChunkTickets  = "chunk_tickets"
	TickPhaseBlockTicks    = "block_ticks"
	TickPhaseBlockEntities = "block_entities"
	TickPhaseEntities      = "entities"
	TickPhaseWorldBorder   = "world_border"
)

// TickPhaseOrder lists the canonical deterministic order of phases within a single server tick.
var TickPhaseOrder = []string{
	TickPhaseTimeWeather,
	TickPhaseChunkTickets,
	TickPhaseBlockTicks,
	TickPhaseBlockEntities,
	TickPhaseEntities,
	TickPhaseWorldBorder,
}

// LevelData represents the complete vanilla Minecraft 26.1.2 level.dat "Data" compound.
type LevelData struct {
	DataVersion     int32
	Version         int32
	VersionName     string
	VersionID       int32
	VersionSnapshot bool
	VersionSeries   string

	LevelName        string
	Seed             int64
	GenerateFeatures bool
	BonusChest       bool
	GeneratorName    string

	SpawnX     int32
	SpawnY     int32
	SpawnZ     int32
	SpawnAngle float32

	Time       int64
	DayTime    int64
	LastPlayed int64

	GameType         int32
	Hardcore         bool
	Difficulty       byte
	DifficultyLocked bool
	AllowCommands    bool
	Initialized      bool
	WasModded        bool

	Raining          bool
	RainTime         int32
	Thundering       bool
	ThunderTime      int32
	ClearWeatherTime int32

	WanderingTraderSpawnDelay  int32
	WanderingTraderSpawnChance int32

	WorldBorder       WorldBorderSettings
	GameRules         map[string]string
	ServerBrands      []string
	EnabledFeatures   []string
	DataPacksEnabled  []string
	DataPacksDisabled []string

	DragonFight      *nbt.Compound
	CustomBossEvents *nbt.Compound
	ScheduledEvents  []nbt.Tag
}

// DefaultLevelData returns a LevelData initialized with vanilla 26.1.2 defaults.
func DefaultLevelData() LevelData {
	rules := make(map[string]string, len(vanillaGameRules))
	for _, def := range vanillaGameRules {
		if def.Type == GameRuleTypeBool {
			rules[def.Name] = strconv.FormatBool(def.DefaultBool)
		} else {
			rules[def.Name] = strconv.Itoa(def.DefaultInt)
		}
	}
	return LevelData{
		DataVersion:                LevelDatDataVersion,
		Version:                    LevelDatNBTVersion,
		VersionName:                LevelDatVersionName,
		VersionID:                  LevelDatVersionID,
		VersionSnapshot:            false,
		VersionSeries:              LevelDatSeries,
		LevelName:                  "world",
		Seed:                       0,
		GenerateFeatures:           true,
		BonusChest:                 false,
		GeneratorName:              "minecraft:normal",
		SpawnX:                     8,
		SpawnY:                     int32(world.FlatSurfaceY + 1),
		SpawnZ:                     8,
		SpawnAngle:                 0,
		Time:                       0,
		DayTime:                    0,
		LastPlayed:                 time.Now().UnixMilli(),
		GameType:                   int32(GameModeSurvival),
		Hardcore:                   false,
		Difficulty:                 byte(DifficultyNormal),
		DifficultyLocked:           false,
		AllowCommands:              true,
		Initialized:                true,
		WasModded:                  false,
		Raining:                    false,
		RainTime:                   int32(WeatherClearMinTicks),
		Thundering:                 false,
		ThunderTime:                int32(WeatherThunderDelayMinTicks),
		ClearWeatherTime:           0,
		WanderingTraderSpawnDelay:  24000,
		WanderingTraderSpawnChance: 25,
		WorldBorder:                DefaultWorldBorderSettings(),
		GameRules:                  rules,
		ServerBrands:               []string{"RegionIO"},
		EnabledFeatures:            []string{"minecraft:vanilla"},
		DataPacksEnabled:           []string{"vanilla"},
		DataPacksDisabled:          nil,
	}
}

// ExportLevelData snapshots the server's live state (seed, time, weather, spawn,
// difficulty, hardcore, gamerules, world border, wandering trader, dragon fight,
// boss events, scheduled functions) into a LevelData struct.
func (s *Server) ExportLevelData() LevelData {
	ld := DefaultLevelData()
	if s == nil {
		return ld
	}
	if s.cfg.WorldDir != "" {
		base := filepath.Base(filepath.Clean(s.cfg.WorldDir))
		if base != "" && base != "." && base != "/" {
			ld.LevelName = base
		}
	}
	ld.Seed = s.cfg.WorldSeed
	ld.GenerateFeatures = s.cfg.GenerateStructures
	if s.cfg.LevelType != "" {
		lt := strings.ToLower(strings.TrimSpace(s.cfg.LevelType))
		if !strings.Contains(lt, ":") {
			lt = "minecraft:" + lt
		}
		ld.GeneratorName = lt
	}

	sx, sy, sz, syaw := s.WorldSpawn()
	ld.SpawnX = int32(sx)
	ld.SpawnY = int32(sy)
	ld.SpawnZ = int32(sz)
	ld.SpawnAngle = syaw

	gt, dt := s.WorldTime()
	ld.Time = gt
	ld.DayTime = dt
	ld.LastPlayed = time.Now().UnixMilli()

	ld.GameType = int32(s.DefaultGameMode())
	ld.Hardcore = s.IsHardcore()
	ld.Difficulty = byte(s.Difficulty())
	ld.DifficultyLocked = s.DifficultyLocked()

	w := s.Weather()
	ld.Raining = w.Raining
	ld.Thundering = w.Thundering
	ld.ClearWeatherTime = int32(w.ClearTime)
	ld.RainTime = int32(w.RainTime)
	ld.ThunderTime = int32(w.ThunderTime)

	if wb := s.WorldBorder(); wb != nil {
		ld.WorldBorder = wb.Settings()
	}

	for _, def := range vanillaGameRules {
		if _, valStr, ok := s.GameRuleString(def.Name); ok {
			ld.GameRules[def.Name] = valStr
		}
	}
	return ld
}

// ApplyLevelData applies a loaded LevelData snapshot to the server's live state.
func (s *Server) ApplyLevelData(ld LevelData) {
	if s == nil {
		return
	}
	if ld.Seed != 0 && s.cfg.WorldSeed == 0 {
		s.cfg.WorldSeed = ld.Seed
	}
	s.SetWorldTime(ld.Time, ld.DayTime)

	// Restore weather state directly into worldClock without spuriously broadcasting if no players are online.
	s.clock.mu.Lock()
	s.clock.weatherInit = true
	s.clock.raining = ld.Raining
	s.clock.thundering = ld.Thundering
	s.clock.clearTime = int64(ld.ClearWeatherTime)
	s.clock.rainTime = int64(ld.RainTime)
	s.clock.thunderTime = int64(ld.ThunderTime)
	s.clock.mu.Unlock()

	if ld.GameType >= 0 && ld.GameType <= int32(GameModeSpectator) {
		s.SetDefaultGameMode(uint8(ld.GameType))
	}
	if ld.Difficulty <= byte(DifficultyHard) {
		s.SetDifficulty(Difficulty(ld.Difficulty))
	}
	s.SetHardcore(ld.Hardcore)
	s.SetDifficultyLocked(ld.DifficultyLocked || ld.Hardcore)

	for k, v := range ld.GameRules {
		_, _, _ = s.SetGameRuleFromString(k, v)
	}

	if ld.SpawnX != 0 || ld.SpawnY != 0 || ld.SpawnZ != 0 || ld.SpawnAngle != 0 {
		spCfg := getWorldSpawnConfig(s)
		spCfg.mu.Lock()
		spCfg.custom = true
		spCfg.x = int(ld.SpawnX)
		spCfg.y = int(ld.SpawnY)
		spCfg.z = int(ld.SpawnZ)
		spCfg.yaw = ld.SpawnAngle
		spCfg.mu.Unlock()
	}

	if wb := s.WorldBorder(); wb != nil && ld.WorldBorder.Size > 0 {
		wb.ApplySettings(ld.WorldBorder)
	}
}

// LevelDataToNBT serializes LevelData into a root NBT compound containing the
// vanilla "Data" compound.
func LevelDataToNBT(ld LevelData) *nbt.Compound {
	if ld.DataVersion <= 0 {
		ld.DataVersion = LevelDatDataVersion
	}
	if ld.Version <= 0 {
		ld.Version = LevelDatNBTVersion
	}
	if ld.VersionName == "" {
		ld.VersionName = LevelDatVersionName
	}
	if ld.VersionID <= 0 {
		ld.VersionID = ld.DataVersion
	}
	if ld.VersionSeries == "" {
		ld.VersionSeries = LevelDatSeries
	}
	if ld.LevelName == "" {
		ld.LevelName = "world"
	}
	if ld.GeneratorName == "" {
		ld.GeneratorName = "minecraft:normal"
	}
	if ld.WorldBorder.Size <= 0 {
		ld.WorldBorder = DefaultWorldBorderSettings()
	}

	data := nbt.NewCompound()
	data.Set("DataVersion", nbt.Int(ld.DataVersion))
	data.Set("version", nbt.Int(ld.Version))
	data.Set("LevelName", nbt.String(ld.LevelName))
	data.Set("initialized", boolToNBTByte(ld.Initialized))
	data.Set("WasModded", boolToNBTByte(ld.WasModded))
	data.Set("allowCommands", boolToNBTByte(ld.AllowCommands))
	data.Set("hardcore", boolToNBTByte(ld.Hardcore))
	data.Set("GameType", nbt.Int(ld.GameType))
	data.Set("Difficulty", nbt.Byte(int8(ld.Difficulty)))
	data.Set("DifficultyLocked", boolToNBTByte(ld.DifficultyLocked))

	data.Set("Time", nbt.Long(ld.Time))
	data.Set("DayTime", nbt.Long(ld.DayTime))
	data.Set("LastPlayed", nbt.Long(ld.LastPlayed))

	data.Set("SpawnX", nbt.Int(ld.SpawnX))
	data.Set("SpawnY", nbt.Int(ld.SpawnY))
	data.Set("SpawnZ", nbt.Int(ld.SpawnZ))
	data.Set("SpawnAngle", nbt.Float(ld.SpawnAngle))

	data.Set("raining", boolToNBTByte(ld.Raining))
	data.Set("rainTime", nbt.Int(ld.RainTime))
	data.Set("thundering", boolToNBTByte(ld.Thundering))
	data.Set("thunderTime", nbt.Int(ld.ThunderTime))
	data.Set("clearWeatherTime", nbt.Int(ld.ClearWeatherTime))

	data.Set("WanderingTraderSpawnDelay", nbt.Int(ld.WanderingTraderSpawnDelay))
	data.Set("WanderingTraderSpawnChance", nbt.Int(ld.WanderingTraderSpawnChance))

	// Version compound
	verComp := nbt.NewCompound().
		Set("Id", nbt.Int(ld.VersionID)).
		Set("Name", nbt.String(ld.VersionName)).
		Set("Series", nbt.String(ld.VersionSeries)).
		Set("Snapshot", boolToNBTByte(ld.VersionSnapshot))
	data.Set("Version", verComp)

	// WorldGenSettings compound
	dimsComp := nbt.NewCompound()
	for _, dim := range []struct {
		key      string
		settings string
		biomeSrc *nbt.Compound
	}{
		{
			key:      world.DimensionOverworld,
			settings: "minecraft:overworld",
			biomeSrc: nbt.NewCompound().
				Set("type", nbt.String("minecraft:multi_noise")).
				Set("preset", nbt.String("minecraft:overworld")),
		},
		{
			key:      world.DimensionNether,
			settings: "minecraft:nether",
			biomeSrc: nbt.NewCompound().
				Set("type", nbt.String("minecraft:multi_noise")).
				Set("preset", nbt.String("minecraft:nether")),
		},
		{
			key:      world.DimensionEnd,
			settings: "minecraft:end",
			biomeSrc: nbt.NewCompound().
				Set("type", nbt.String("minecraft:the_end")),
		},
	} {
		genComp := nbt.NewCompound().
			Set("type", nbt.String("minecraft:noise")).
			Set("settings", nbt.String(dim.settings)).
			Set("biome_source", dim.biomeSrc)
		dimsComp.Set(dim.key, nbt.NewCompound().
			Set("type", nbt.String(dim.key)).
			Set("generator", genComp))
	}
	wgs := nbt.NewCompound().
		Set("seed", nbt.Long(ld.Seed)).
		Set("generate_features", boolToNBTByte(ld.GenerateFeatures)).
		Set("bonus_chest", boolToNBTByte(ld.BonusChest)).
		Set("dimensions", dimsComp)
	data.Set("WorldGenSettings", wgs)
	// Also store RandomSeed for legacy tools reading level.dat directly.
	data.Set("RandomSeed", nbt.Long(ld.Seed))
	data.Set("generatorName", nbt.String(ld.GeneratorName))

	// World border (both 26.1.2 world_border subcompound and legacy Border* keys)
	data.Set("world_border", ld.WorldBorder.ToNBT())
	data.Set("BorderCenterX", nbt.Double(ld.WorldBorder.CenterX))
	data.Set("BorderCenterZ", nbt.Double(ld.WorldBorder.CenterZ))
	data.Set("BorderSize", nbt.Double(ld.WorldBorder.Size))
	data.Set("BorderSafeZone", nbt.Double(ld.WorldBorder.SafeZone))
	data.Set("BorderDamagePerBlock", nbt.Double(ld.WorldBorder.DamagePerBlock))
	data.Set("BorderWarningBlocks", nbt.Double(float64(ld.WorldBorder.WarningBlocks)))
	data.Set("BorderWarningTime", nbt.Double(float64(ld.WorldBorder.WarningTime)))
	data.Set("BorderSizeLerpTime", nbt.Long(ld.WorldBorder.LerpTime))
	data.Set("BorderSizeLerpTarget", nbt.Double(ld.WorldBorder.LerpTarget))

	// GameRules compound
	rulesComp := nbt.NewCompound()
	ruleKeys := make([]string, 0, len(ld.GameRules))
	for k := range ld.GameRules {
		ruleKeys = append(ruleKeys, k)
	}
	sort.Strings(ruleKeys)
	for _, k := range ruleKeys {
		rulesComp.Set(k, nbt.String(ld.GameRules[k]))
	}
	data.Set("GameRules", rulesComp)

	// ServerBrands, enabled_features, DataPacks
	brands := ld.ServerBrands
	if len(brands) == 0 {
		brands = []string{"RegionIO"}
	}
	data.Set("ServerBrands", stringsToNBTList(brands))

	features := ld.EnabledFeatures
	if len(features) == 0 {
		features = []string{"minecraft:vanilla"}
	}
	data.Set("enabled_features", stringsToNBTList(features))

	dpEnabled := ld.DataPacksEnabled
	if len(dpEnabled) == 0 {
		dpEnabled = []string{"vanilla"}
	}
	dpComp := nbt.NewCompound().
		Set("Enabled", stringsToNBTList(dpEnabled)).
		Set("Disabled", stringsToNBTList(ld.DataPacksDisabled))
	data.Set("DataPacks", dpComp)

	if ld.DragonFight != nil {
		data.Set("DragonFight", ld.DragonFight)
	} else {
		data.Set("DragonFight", nbt.NewCompound())
	}
	if ld.CustomBossEvents != nil {
		data.Set("CustomBossEvents", ld.CustomBossEvents)
	} else {
		data.Set("CustomBossEvents", nbt.NewCompound())
	}
	data.Set("ScheduledEvents", nbt.List{
		ElemID: nbt.TagCompound,
		Elems:  ld.ScheduledEvents,
	})

	return nbt.NewCompound().Set("Data", data)
}

// LevelDataFromNBT parses a LevelData from a level.dat root compound (either
// wrapping a "Data" subcompound or flat at the root).
func LevelDataFromNBT(root *nbt.Compound) (LevelData, error) {
	ld := DefaultLevelData()
	if root == nil {
		return ld, errors.New("server: nil level.dat NBT compound")
	}
	data := root
	if dTag, ok := root.Get("Data"); ok {
		if sub, ok := dTag.(*nbt.Compound); ok && sub != nil {
			data = sub
		}
	}

	if v, ok := nbtInt32(data, "DataVersion"); ok {
		ld.DataVersion = v
	}
	if v, ok := nbtInt32(data, "version"); ok {
		ld.Version = v
	}
	if s, ok := nbtStringVal(data, "LevelName"); ok {
		ld.LevelName = s
	}
	if b, ok := nbtBoolVal(data, "initialized"); ok {
		ld.Initialized = b
	}
	if b, ok := nbtBoolVal(data, "WasModded"); ok {
		ld.WasModded = b
	}
	if b, ok := nbtBoolVal(data, "allowCommands"); ok {
		ld.AllowCommands = b
	}
	if b, ok := nbtBoolVal(data, "hardcore"); ok {
		ld.Hardcore = b
	}
	if v, ok := nbtInt32(data, "GameType"); ok {
		ld.GameType = v
	}
	if v, ok := nbtInt32(data, "Difficulty"); ok && v >= 0 && v <= 3 {
		ld.Difficulty = byte(v)
	}
	if b, ok := nbtBoolVal(data, "DifficultyLocked"); ok {
		ld.DifficultyLocked = b
	}

	if v, ok := nbtInt64(data, "Time"); ok {
		ld.Time = v
	}
	if v, ok := nbtInt64(data, "DayTime"); ok {
		ld.DayTime = v
	}
	if v, ok := nbtInt64(data, "LastPlayed"); ok {
		ld.LastPlayed = v
	}

	if v, ok := nbtInt32(data, "SpawnX"); ok {
		ld.SpawnX = v
	}
	if v, ok := nbtInt32(data, "SpawnY"); ok {
		ld.SpawnY = v
	}
	if v, ok := nbtInt32(data, "SpawnZ"); ok {
		ld.SpawnZ = v
	}
	if v, ok := nbtFloat64(data, "SpawnAngle"); ok {
		ld.SpawnAngle = float32(v)
	}

	if b, ok := nbtBoolVal(data, "raining"); ok {
		ld.Raining = b
	}
	if v, ok := nbtInt32(data, "rainTime"); ok {
		ld.RainTime = v
	}
	if b, ok := nbtBoolVal(data, "thundering"); ok {
		ld.Thundering = b
	}
	if v, ok := nbtInt32(data, "thunderTime"); ok {
		ld.ThunderTime = v
	}
	if v, ok := nbtInt32(data, "clearWeatherTime"); ok {
		ld.ClearWeatherTime = v
	}

	if v, ok := nbtInt32(data, "WanderingTraderSpawnDelay"); ok {
		ld.WanderingTraderSpawnDelay = v
	}
	if v, ok := nbtInt32(data, "WanderingTraderSpawnChance"); ok {
		ld.WanderingTraderSpawnChance = v
	}

	if vTag, ok := data.Get("Version"); ok {
		if vc, ok := vTag.(*nbt.Compound); ok && vc != nil {
			if id, ok := nbtInt32(vc, "Id"); ok {
				ld.VersionID = id
			}
			if name, ok := nbtStringVal(vc, "Name"); ok {
				ld.VersionName = name
			}
			if series, ok := nbtStringVal(vc, "Series"); ok {
				ld.VersionSeries = series
			}
			if snap, ok := nbtBoolVal(vc, "Snapshot"); ok {
				ld.VersionSnapshot = snap
			}
		}
	}

	if seed, ok := nbtInt64(data, "RandomSeed"); ok {
		ld.Seed = seed
	}
	if genName, ok := nbtStringVal(data, "generatorName"); ok {
		ld.GeneratorName = genName
	}
	if wgsTag, ok := data.Get("WorldGenSettings"); ok {
		if wgs, ok := wgsTag.(*nbt.Compound); ok && wgs != nil {
			if seed, ok := nbtInt64(wgs, "seed"); ok {
				ld.Seed = seed
			}
			if gf, ok := nbtBoolVal(wgs, "generate_features"); ok {
				ld.GenerateFeatures = gf
			}
			if bc, ok := nbtBoolVal(wgs, "bonus_chest"); ok {
				ld.BonusChest = bc
			}
		}
	}

	ld.WorldBorder = WorldBorderSettingsFromNBT(data)

	if grTag, ok := data.Get("GameRules"); ok {
		if grComp, ok := grTag.(*nbt.Compound); ok && grComp != nil {
			for _, k := range grComp.Keys() {
				if vTag, ok := grComp.Get(k); ok {
					switch tv := vTag.(type) {
					case nbt.String:
						ld.GameRules[k] = string(tv)
					case nbt.Byte:
						ld.GameRules[k] = strconv.FormatBool(tv != 0)
					case nbt.Int:
						ld.GameRules[k] = strconv.Itoa(int(tv))
					}
				}
			}
		}
	}

	if sb, ok := nbtStringSlice(data, "ServerBrands"); ok && len(sb) > 0 {
		ld.ServerBrands = sb
	}
	if ef, ok := nbtStringSlice(data, "enabled_features"); ok && len(ef) > 0 {
		ld.EnabledFeatures = ef
	}
	if dpTag, ok := data.Get("DataPacks"); ok {
		if dpc, ok := dpTag.(*nbt.Compound); ok && dpc != nil {
			if en, ok := nbtStringSlice(dpc, "Enabled"); ok {
				ld.DataPacksEnabled = en
			}
			if dis, ok := nbtStringSlice(dpc, "Disabled"); ok {
				ld.DataPacksDisabled = dis
			}
		}
	}
	if dfTag, ok := data.Get("DragonFight"); ok {
		if dfc, ok := dfTag.(*nbt.Compound); ok {
			ld.DragonFight = dfc
		}
	}
	if cbTag, ok := data.Get("CustomBossEvents"); ok {
		if cbc, ok := cbTag.(*nbt.Compound); ok {
			ld.CustomBossEvents = cbc
		}
	}
	if seTag, ok := data.Get("ScheduledEvents"); ok {
		if sel, ok := seTag.(nbt.List); ok {
			ld.ScheduledEvents = sel.Elems
		}
	}

	return ld, nil
}

// MarshalLevelDat encodes LevelData as gzip-compressed named NBT (matching vanilla level.dat).
func MarshalLevelDat(ld LevelData) ([]byte, error) {
	rawNBT := nbt.MarshalNamed("", LevelDataToNBT(ld))
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(rawNBT); err != nil {
		_ = zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// UnmarshalLevelDat decodes LevelData from gzip-compressed or raw uncompressed NBT bytes.
func UnmarshalLevelDat(raw []byte) (LevelData, error) {
	if len(raw) == 0 {
		return DefaultLevelData(), errors.New("server: empty level.dat payload")
	}
	payload := raw
	if len(raw) >= 2 && raw[0] == 0x1f && raw[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return DefaultLevelData(), fmt.Errorf("server: gunzip level.dat: %w", err)
		}
		decompressed, err := io.ReadAll(zr)
		_ = zr.Close()
		if err != nil {
			return DefaultLevelData(), fmt.Errorf("server: read gunzipped level.dat: %w", err)
		}
		payload = decompressed
	}
	_, tag, err := nbt.UnmarshalNamed(payload)
	if err != nil {
		// Try anonymous root compound fallback
		tag, err = nbt.Unmarshal(payload)
		if err != nil {
			return DefaultLevelData(), fmt.Errorf("server: decode level.dat NBT: %w", err)
		}
	}
	comp, ok := tag.(*nbt.Compound)
	if !ok || comp == nil {
		return DefaultLevelData(), errors.New("server: level.dat root tag is not a compound")
	}
	return LevelDataFromNBT(comp)
}

func (s *Server) hasPersistentWorldDir() bool {
	if s == nil || s.cfg.WorldDir == "" {
		return false
	}
	if s.store == nil && s.cfg.WorldDir == "world" {
		return false
	}
	return true
}

func (s *Server) levelDatPath() string {
	if !s.hasPersistentWorldDir() {
		return ""
	}
	return filepath.Join(s.cfg.WorldDir, "level.dat")
}

// SaveLevelDat writes <WorldDir>/level.dat atomically (rotating any previous
// level.dat to level.dat_old, matching vanilla LevelStorageSource).
func (s *Server) SaveLevelDat() error {
	path := s.levelDatPath()
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	encoded, err := MarshalLevelDat(s.ExportLevelData())
	if err != nil {
		return err
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, encoded, 0o644); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		_ = os.Rename(path, path+"_old")
	}
	return os.Rename(tmpPath, path)
}

// LoadLevelDat loads <WorldDir>/level.dat (falling back to <WorldDir>/level.dat_old
// if level.dat is corrupted) and applies its state to the Server.
func (s *Server) LoadLevelDat() error {
	path := s.levelDatPath()
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		rawOld, errOld := os.ReadFile(path + "_old")
		if errOld != nil {
			if os.IsNotExist(errOld) {
				return nil
			}
			return errOld
		}
		raw = rawOld
	}
	ld, err := UnmarshalLevelDat(raw)
	if err != nil {
		// Fallback to level.dat_old when primary level.dat is corrupted.
		if rawOld, errOld := os.ReadFile(path + "_old"); errOld == nil {
			if ldOld, errOld2 := UnmarshalLevelDat(rawOld); errOld2 == nil {
				s.ApplyLevelData(ldOld)
				return nil
			}
		}
		return err
	}
	s.ApplyLevelData(ld)
	return nil
}

// PackChunkPosLong packs chunk coordinates (cx, cz) into a single int64 using
// vanilla ChunkPos.asLong(x, z): low 32 bits = uint32(cx), high 32 bits = uint32(cz).
func PackChunkPosLong(cx, cz int32) int64 {
	return int64(uint32(cx)) | (int64(uint32(cz)) << 32)
}

// UnpackChunkPosLong unpacks a vanilla ChunkPos long back into (cx, cz).
func UnpackChunkPosLong(packed int64) (cx, cz int32) {
	return int32(uint32(packed)), int32(uint32(uint64(packed) >> 32))
}

func (s *Server) forceLoadedChunksFilePath(dim string) string {
	if !s.hasPersistentWorldDir() {
		return ""
	}
	switch world.NormalizeDimension(dim) {
	case world.DimensionNether:
		return filepath.Join(s.cfg.WorldDir, "DIM-1", "data", "chunks.dat")
	case world.DimensionEnd:
		return filepath.Join(s.cfg.WorldDir, "DIM1", "data", "chunks.dat")
	default:
		return filepath.Join(s.cfg.WorldDir, "data", "chunks.dat")
	}
}

// MarshalForcedChunksNBT encodes a slice of [2]int32 chunk coordinates into
// vanilla's chunks.dat NBT format ({DataVersion: 4790, data: {Forced: [L; ...]}}).
func MarshalForcedChunksNBT(chunks [][2]int32, compress bool) ([]byte, error) {
	sorted := make([][2]int32, len(chunks))
	copy(sorted, chunks)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i][0] != sorted[j][0] {
			return sorted[i][0] < sorted[j][0]
		}
		return sorted[i][1] < sorted[j][1]
	})
	longs := make(nbt.LongArray, len(sorted))
	for i, c := range sorted {
		longs[i] = PackChunkPosLong(c[0], c[1])
	}
	inner := nbt.NewCompound().Set("Forced", longs)
	root := nbt.NewCompound().
		Set("DataVersion", nbt.Int(LevelDatDataVersion)).
		Set("data", inner)
	raw := nbt.MarshalNamed("", root)
	if !compress {
		return raw, nil
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		_ = zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// UnmarshalForcedChunksNBT decodes force-loaded chunk coordinates from vanilla's
// chunks.dat NBT payload (either gzipped or raw NBT).
func UnmarshalForcedChunksNBT(raw []byte) ([][2]int32, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	payload := raw
	if len(raw) >= 2 && raw[0] == 0x1f && raw[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		decompressed, err := io.ReadAll(zr)
		_ = zr.Close()
		if err != nil {
			return nil, err
		}
		payload = decompressed
	}
	_, tag, err := nbt.UnmarshalNamed(payload)
	if err != nil {
		tag, err = nbt.Unmarshal(payload)
		if err != nil {
			return nil, err
		}
	}
	root, ok := tag.(*nbt.Compound)
	if !ok || root == nil {
		return nil, errors.New("server: chunks.dat root is not a compound")
	}
	data := root
	if dTag, ok := root.Get("data"); ok {
		if sub, ok := dTag.(*nbt.Compound); ok && sub != nil {
			data = sub
		}
	}
	fTag, ok := data.Get("Forced")
	if !ok {
		return nil, nil
	}
	longs, ok := fTag.(nbt.LongArray)
	if !ok {
		return nil, errors.New("server: chunks.dat Forced tag is not a LongArray")
	}
	out := make([][2]int32, 0, len(longs))
	for _, packed := range longs {
		cx, cz := UnpackChunkPosLong(packed)
		out = append(out, [2]int32{cx, cz})
	}
	return out, nil
}

// SaveForceLoadedChunksFile persists force-loaded chunks for all three vanilla
// dimensions to their respective chunks.dat files.
func (s *Server) SaveForceLoadedChunksFile() error {
	if s == nil || s.cfg.WorldDir == "" {
		return nil
	}
	var firstErr error
	for _, dim := range []string{world.DimensionOverworld, world.DimensionNether, world.DimensionEnd} {
		chunks := s.ForceLoadedChunks(dim)
		path := s.forceLoadedChunksFilePath(dim)
		if path == "" {
			continue
		}
		if len(chunks) == 0 {
			if _, err := os.Stat(path); os.IsNotExist(err) {
				continue
			}
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		encoded, err := MarshalForcedChunksNBT(chunks, true)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := os.WriteFile(path, encoded, 0o644); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// LoadForceLoadedChunksFile restores force-loaded chunks for all three vanilla
// dimensions from their respective chunks.dat files and registers Forced tickets.
func (s *Server) LoadForceLoadedChunksFile() error {
	if s == nil || s.cfg.WorldDir == "" {
		return nil
	}
	var firstErr error
	for _, dim := range []string{world.DimensionOverworld, world.DimensionNether, world.DimensionEnd} {
		path := s.forceLoadedChunksFilePath(dim)
		if path == "" {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		coords, err := UnmarshalForcedChunksNBT(raw)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, c := range coords {
			s.SetChunkForceLoaded(dim, c[0], c[1], true)
			s.AddChunkTicket(dim, c[0], c[1], TicketTypeForced, TicketLevelForced, 0)
		}
	}
	return firstErr
}

// ChunkTicket represents a vanilla DistanceManager chunk ticket holding a chunk
// loaded at a given ticket level with an optional expiration timer in game ticks.
type ChunkTicket struct {
	Type           string
	Dimension      string
	ChunkX         int32
	ChunkZ         int32
	Level          int
	CreatedAtTick  int64
	TTLTicks       int64 // 0 = does not expire automatically
	RemainingTicks int64
}

type chunkTicketManager struct {
	mu      sync.RWMutex
	tickets map[string]map[[2]int32][]ChunkTicket // dim -> [cx, cz] -> tickets
}

var serverChunkTickets sync.Map // map[*Server]*chunkTicketManager

func (s *Server) ticketManager() *chunkTicketManager {
	if s == nil {
		return nil
	}
	if v, ok := serverChunkTickets.Load(s); ok {
		return v.(*chunkTicketManager)
	}
	created := &chunkTicketManager{
		tickets: make(map[string]map[[2]int32][]ChunkTicket),
	}
	actual, _ := serverChunkTickets.LoadOrStore(s, created)
	return actual.(*chunkTicketManager)
}

func defaultTicketLevelAndTTL(ticketType string) (level int, ttl int64) {
	switch strings.ToLower(strings.TrimSpace(ticketType)) {
	case TicketTypeStart:
		return TicketLevelStart, 0
	case TicketTypeDragon:
		return TicketLevelDragon, 0
	case TicketTypePortal:
		return TicketLevelPortal, TicketPortalTTLTicks
	case TicketTypePlayer:
		return TicketLevelPlayer, 0
	case TicketTypeForced:
		return TicketLevelForced, 0
	case TicketTypePostTeleport:
		return TicketLevelPostTeleport, TicketPostTeleportTTLTicks
	case TicketTypeUnknown:
		return TicketLevelUnknown, TicketUnknownTTLTicks
	default:
		return TicketLevelPlayer, 0
	}
}

// AddChunkTicket registers or refreshes a chunk ticket at (dim, cx, cz). If
// level <= 0 or ttlTicks < 0, the canonical vanilla level/TTL for ticketType is used.
func (s *Server) AddChunkTicket(dim string, cx, cz int32, ticketType string, level int, ttlTicks int64) ChunkTicket {
	normDim := world.NormalizeDimension(dim)
	normType := strings.ToLower(strings.TrimSpace(ticketType))
	if normType == "" {
		normType = TicketTypeUnknown
	}
	defLevel, defTTL := defaultTicketLevelAndTTL(normType)
	if level <= 0 {
		level = defLevel
	}
	if ttlTicks < 0 || (ttlTicks == 0 && defTTL > 0) {
		ttlTicks = defTTL
	}
	var nowTick int64
	if s != nil {
		nowTick, _ = s.WorldTime()
	}
	ticket := ChunkTicket{
		Type:           normType,
		Dimension:      normDim,
		ChunkX:         cx,
		ChunkZ:         cz,
		Level:          level,
		CreatedAtTick:  nowTick,
		TTLTicks:       ttlTicks,
		RemainingTicks: ttlTicks,
	}
	if s == nil {
		return ticket
	}
	mgr := s.ticketManager()
	mgr.mu.Lock()
	dimMap := mgr.tickets[normDim]
	if dimMap == nil {
		dimMap = make(map[[2]int32][]ChunkTicket)
		mgr.tickets[normDim] = dimMap
	}
	key := [2]int32{cx, cz}
	list := dimMap[key]
	replaced := false
	for i := range list {
		if list[i].Type == normType && list[i].Level == level {
			list[i] = ticket
			replaced = true
			break
		}
	}
	if !replaced {
		list = append(list, ticket)
	}
	dimMap[key] = list
	mgr.mu.Unlock()

	if cache := s.DimensionChunks(normDim); cache != nil {
		_ = cache.GetBlock(int(cx)*16, 64, int(cz)*16)
	}
	return ticket
}

// RemoveChunkTicket removes all tickets of ticketType (or all tickets if
// ticketType == "") at (dim, cx, cz) and returns the number removed.
func (s *Server) RemoveChunkTicket(dim string, cx, cz int32, ticketType string) int {
	if s == nil {
		return 0
	}
	normDim := world.NormalizeDimension(dim)
	normType := strings.ToLower(strings.TrimSpace(ticketType))
	mgr := s.ticketManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	dimMap := mgr.tickets[normDim]
	if dimMap == nil {
		return 0
	}
	key := [2]int32{cx, cz}
	list := dimMap[key]
	if len(list) == 0 {
		return 0
	}
	if normType == "" {
		delete(dimMap, key)
		return len(list)
	}
	kept := list[:0]
	removed := 0
	for _, t := range list {
		if t.Type == normType {
			removed++
		} else {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(dimMap, key)
	} else {
		dimMap[key] = kept
	}
	return removed
}

// ChunkTicketsAt returns a copy of all active tickets at (dim, cx, cz).
func (s *Server) ChunkTicketsAt(dim string, cx, cz int32) []ChunkTicket {
	if s == nil {
		return nil
	}
	normDim := world.NormalizeDimension(dim)
	mgr := s.ticketManager()
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	dimMap := mgr.tickets[normDim]
	if dimMap == nil {
		return nil
	}
	src := dimMap[[2]int32{cx, cz}]
	if len(src) == 0 {
		return nil
	}
	out := make([]ChunkTicket, len(src))
	copy(out, src)
	return out
}

// EffectiveChunkTicketLevel returns the minimum (strongest) ticket level at
// (dim, cx, cz), or TicketLevelMax+1 if the chunk has no active tickets.
func (s *Server) EffectiveChunkTicketLevel(dim string, cx, cz int32) int {
	if s == nil {
		return TicketLevelMax + 1
	}
	normDim := world.NormalizeDimension(dim)
	best := TicketLevelMax + 1
	if s.IsChunkForceLoaded(normDim, cx, cz) {
		best = TicketLevelForced
	}
	for _, t := range s.ChunkTicketsAt(normDim, cx, cz) {
		if t.Level < best {
			best = t.Level
		}
	}
	return best
}

// IsChunkEntityTicking reports whether chunk (cx, cz) in dim is in entity-ticking
// range (either via a ticket with level <= 31, force-loading, or within
// SimulationDistance of an online player in dim).
func (s *Server) IsChunkEntityTicking(dim string, cx, cz int32) bool {
	if s == nil {
		return false
	}
	normDim := world.NormalizeDimension(dim)
	if s.EffectiveChunkTicketLevel(normDim, cx, cz) <= TicketLevelPlayer {
		return true
	}
	simDist := int32(s.SimulationDistance())
	for _, p := range s.onlinePlayersSnapshot() {
		if p == nil || p.IsDead() {
			continue
		}
		snap := p.Snapshot()
		if world.NormalizeDimension(snap.Dimension) != normDim {
			continue
		}
		pcx := int32(int64(math.Floor(snap.X)) >> 4)
		pcz := int32(int64(math.Floor(snap.Z)) >> 4)
		if chunkDistance(pcx, pcz, cx, cz) <= simDist {
			return true
		}
	}
	return false
}

// ActiveTicketedChunks returns all chunk coordinates in dim that currently hold
// at least one ticket or are force-loaded.
func (s *Server) ActiveTicketedChunks(dim string) [][2]int32 {
	if s == nil {
		return nil
	}
	normDim := world.NormalizeDimension(dim)
	seen := make(map[[2]int32]bool)
	for _, fc := range s.ForceLoadedChunks(normDim) {
		seen[fc] = true
	}
	mgr := s.ticketManager()
	mgr.mu.RLock()
	if dimMap := mgr.tickets[normDim]; dimMap != nil {
		for k, list := range dimMap {
			if len(list) > 0 {
				seen[k] = true
			}
		}
	}
	mgr.mu.RUnlock()
	out := make([][2]int32, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] != out[j][0] {
			return out[i][0] < out[j][0]
		}
		return out[i][1] < out[j][1]
	})
	return out
}

// TickChunkTickets advances all timed chunk tickets by 1 tick and expires any
// tickets whose RemainingTicks reaches 0, returning the number of expired tickets.
func (s *Server) TickChunkTickets() int {
	if s == nil {
		return 0
	}
	mgr := s.ticketManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	expired := 0
	for _, dimMap := range mgr.tickets {
		for key, list := range dimMap {
			kept := list[:0]
			for _, t := range list {
				if t.TTLTicks > 0 {
					t.RemainingTicks--
					if t.RemainingTicks <= 0 {
						expired++
						continue
					}
				}
				kept = append(kept, t)
			}
			if len(kept) == 0 {
				delete(dimMap, key)
			} else {
				dimMap[key] = kept
			}
		}
	}
	return expired
}

// MigrateVanillaChunkNBT upgrades a vanilla chunk NBT compound (including pre-1.18
// DataVersion < 2844 chunks nested under a "Level" compound and legacy "Sections" /
// "TileEntities" / "Ticks" tags) into the flat 26.1.2 (DataVersion 4790) layout
// expected by world.Store. Returns the migrated compound and its original DataVersion.
func MigrateVanillaChunkNBT(root *nbt.Compound) (migrated *nbt.Compound, originalDataVersion int32, err error) {
	if root == nil {
		return nil, 0, errors.New("server: nil chunk NBT compound")
	}
	origDV, _ := nbtInt32(root, "DataVersion")
	out := nbt.NewCompound()
	for _, k := range root.Keys() {
		if k == "Level" {
			continue
		}
		if v, ok := root.Get(k); ok {
			out.Set(k, v)
		}
	}

	// Flatten pre-1.18 "Level" compound if present.
	if lvlTag, ok := root.Get("Level"); ok {
		if lvl, ok := lvlTag.(*nbt.Compound); ok && lvl != nil {
			for _, k := range lvl.Keys() {
				v, _ := lvl.Get(k)
				switch k {
				case "Sections":
					if secList, ok := v.(nbt.List); ok {
						out.Set("sections", migrateLegacySectionsNBT(secList))
					}
				case "TileEntities":
					if _, exists := out.Get("block_entities"); !exists {
						out.Set("block_entities", v)
					}
				case "TileTicks":
					if _, exists := out.Get("block_ticks"); !exists {
						out.Set("block_ticks", v)
					}
				case "LiquidTicks":
					if _, exists := out.Get("fluid_ticks"); !exists {
						out.Set("fluid_ticks", v)
					}
				case "Structures":
					if _, exists := out.Get("structures"); !exists {
						out.Set("structures", v)
					}
				default:
					if _, exists := out.Get(k); !exists {
						out.Set(k, v)
					}
				}
			}
		}
	}

	// Also convert top-level "Sections" if present.
	if _, hasSections := out.Get("sections"); !hasSections {
		if secTag, ok := out.Get("Sections"); ok {
			if secList, ok := secTag.(nbt.List); ok {
				out.Set("sections", migrateLegacySectionsNBT(secList))
			}
		}
	}

	if _, hasYPos := out.Get("yPos"); !hasYPos {
		out.Set("yPos", nbt.Int(-4))
	}
	if statusStr, ok := nbtStringVal(out, "Status"); !ok || statusStr == "" {
		out.Set("Status", nbt.String("minecraft:full"))
	} else if !strings.Contains(statusStr, ":") {
		out.Set("Status", nbt.String("minecraft:"+statusStr))
	}
	if _, ok := out.Get("block_entities"); !ok {
		out.Set("block_entities", nbt.List{ElemID: nbt.TagCompound})
	}
	if _, ok := out.Get("structures"); !ok {
		out.Set("structures", nbt.NewCompound())
	}
	out.Set("DataVersion", nbt.Int(LevelDatDataVersion))
	out.Set("RegionIOGeneratorVersion", nbt.Int(currentWorldGeneratorVersion()))
	return out, origDV, nil
}

var (
	worldGenVersionOnce sync.Once
	worldGenVersionVal  int32 = RegionIOGenVersion2612
)

func currentWorldGeneratorVersion() int32 {
	worldGenVersionOnce.Do(func() {
		tmpDir, err := os.MkdirTemp("", "regionio-genver-*")
		if err != nil {
			return
		}
		defer os.RemoveAll(tmpDir)
		st, err := world.NewStore(tmpDir)
		if err != nil {
			return
		}
		_ = st.SaveChunk(world.NewChunk(0, 0, 0))
		_ = st.Close()
		rf, err := world.OpenRegion(filepath.Join(tmpDir, "region"), 0, 0)
		if err != nil {
			return
		}
		defer rf.Close()
		raw, err := rf.ReadChunk(0, 0)
		if err != nil {
			return
		}
		if _, tag, err := nbt.UnmarshalNamed(raw); err == nil {
			if comp, ok := tag.(*nbt.Compound); ok {
				if v, ok := nbtInt32(comp, "RegionIOGeneratorVersion"); ok && v > 0 {
					worldGenVersionVal = v
				}
			}
		}
	})
	return worldGenVersionVal
}

func migrateLegacySectionsNBT(legacyList nbt.List) nbt.List {
	migrated := nbt.List{ElemID: nbt.TagCompound}
	for _, elem := range legacyList.Elems {
		sc, ok := elem.(*nbt.Compound)
		if !ok || sc == nil {
			continue
		}
		newSec := nbt.NewCompound()
		for _, k := range sc.Keys() {
			if k == "Palette" || k == "BlockStates" {
				continue
			}
			if v, ok := sc.Get(k); ok {
				newSec.Set(k, v)
			}
		}
		if _, hasBS := newSec.Get("block_states"); !hasBS {
			bs := nbt.NewCompound()
			if pal, ok := sc.Get("Palette"); ok {
				bs.Set("palette", pal)
			} else {
				bs.Set("palette", nbt.List{
					ElemID: nbt.TagCompound,
					Elems:  []nbt.Tag{nbt.NewCompound().Set("Name", nbt.String("minecraft:air"))},
				})
			}
			if data, ok := sc.Get("BlockStates"); ok {
				bs.Set("data", data)
			}
			newSec.Set("block_states", bs)
		}
		if _, hasBiomes := newSec.Get("biomes"); !hasBiomes {
			newSec.Set("biomes", nbt.NewCompound().Set("palette", nbt.List{
				ElemID: nbt.TagString,
				Elems:  []nbt.Tag{nbt.String("minecraft:plains")},
			}))
		}
		migrated.Elems = append(migrated.Elems, newSec)
	}
	return migrated
}

// ImportVanillaRegionFile reads all chunks in a vanilla region file r.<rx>.<rz>.mca
// from srcRegionDir, migrates their NBT to 26.1.2 (DataVersion 4790) with
// MigrateVanillaChunkNBT, and writes them into the server's region directory.
// Any corrupted chunk entries are caught and counted in recovered without aborting.
func (s *Server) ImportVanillaRegionFile(srcRegionDir string, rx, rz int) (imported int, recovered int, err error) {
	if s == nil || s.cfg.WorldDir == "" {
		return 0, 0, errors.New("server: WorldDir is required to import region files")
	}
	srcRF, err := world.OpenRegion(srcRegionDir, rx, rz)
	if err != nil {
		return 0, 0, err
	}
	defer srcRF.Close()

	dstRegionDir := filepath.Join(s.cfg.WorldDir, "region")
	if err := os.MkdirAll(dstRegionDir, 0o755); err != nil {
		return 0, 0, err
	}
	dstRF, err := world.OpenRegion(dstRegionDir, rx, rz)
	if err != nil {
		return 0, 0, err
	}
	defer dstRF.Close()

	for lz := 0; lz < 32; lz++ {
		for lx := 0; lx < 32; lx++ {
			raw, rErr := srcRF.ReadChunk(lx, lz)
			if errors.Is(rErr, world.ErrChunkNotFound) {
				continue
			}
			if rErr != nil {
				recovered++
				s.recordCorruptedChunkRecovery()
				continue
			}
			_, tag, uErr := nbt.UnmarshalNamed(raw)
			if uErr != nil {
				recovered++
				s.recordCorruptedChunkRecovery()
				continue
			}
			comp, ok := tag.(*nbt.Compound)
			if !ok || comp == nil {
				recovered++
				s.recordCorruptedChunkRecovery()
				continue
			}
			migrated, _, mErr := MigrateVanillaChunkNBT(comp)
			if mErr != nil {
				recovered++
				s.recordCorruptedChunkRecovery()
				continue
			}
			if wErr := dstRF.WriteChunk(lx, lz, nbt.MarshalNamed("", migrated)); wErr != nil {
				recovered++
				s.recordCorruptedChunkRecovery()
				continue
			}
			imported++
		}
	}
	return imported, recovered, nil
}

type serverResilienceState struct {
	corruptedChunksRecovered atomic.Int64
	chunkPanicsRecovered     atomic.Int64
	statusDisabled           atomic.Bool
}

// StatusEnabled reports whether the server responds to Server List Ping status requests.
func (s *Server) StatusEnabled() bool {
	if s == nil {
		return true
	}
	return !s.resilienceState().statusDisabled.Load()
}

// SetStatusEnabled enables or disables Server List Ping status responses at runtime.
func (s *Server) SetStatusEnabled(enabled bool) {
	if s == nil {
		return
	}
	s.resilienceState().statusDisabled.Store(!enabled)
	s.aclMu.Lock()
	s.cfg.EnableStatus = enabled
	s.aclMu.Unlock()
}

var serverResilienceMap sync.Map // map[*Server]*serverResilienceState

func (s *Server) resilienceState() *serverResilienceState {
	if s == nil {
		return &serverResilienceState{}
	}
	if v, ok := serverResilienceMap.Load(s); ok {
		return v.(*serverResilienceState)
	}
	actual, _ := serverResilienceMap.LoadOrStore(s, &serverResilienceState{})
	return actual.(*serverResilienceState)
}

func (s *Server) recordCorruptedChunkRecovery() {
	if s != nil {
		s.resilienceState().corruptedChunksRecovered.Add(1)
	}
}

// CorruptedChunksRecovered returns the total number of corrupted chunks or
// chunk panics recovered by the server without crashing.
func (s *Server) CorruptedChunksRecovered() int64 {
	if s == nil {
		return 0
	}
	st := s.resilienceState()
	return st.corruptedChunksRecovered.Load() + st.chunkPanicsRecovered.Load()
}

// SafeRunChunkCallback runs fn for chunk (dim, cx, cz) inside a panic-recovery
// boundary so a malformed chunk or block entity cannot crash the server process.
func (s *Server) SafeRunChunkCallback(dim string, cx, cz int32, fn func()) (recovered bool, err error) {
	if fn == nil {
		return false, nil
	}
	defer func() {
		if r := recover(); r != nil {
			recovered = true
			err = fmt.Errorf("server: recovered panic in chunk (%s, %d, %d): %v", world.NormalizeDimension(dim), cx, cz, r)
			if s != nil {
				s.resilienceState().chunkPanicsRecovered.Add(1)
			}
		}
	}()
	fn()
	return false, nil
}

// SafeLoadOrRecoverChunk loads chunk (cx, cz) from the server's store (including
// migrating vanilla chunks if needed). If the stored chunk is corrupted or panics
// during decoding, SafeLoadOrRecoverChunk catches the failure, increments the
// recovery counter, regenerates a clean replacement chunk, persists it over the
// damaged slot, and returns recovered=true without crashing the server.
func (s *Server) SafeLoadOrRecoverChunk(dim string, cx, cz int32) (ch *world.Chunk, recovered bool, err error) {
	if s == nil {
		return nil, false, errors.New("server: nil server")
	}
	normDim := world.NormalizeDimension(dim)
	var targetStore *world.Store
	if s.store != nil {
		if normDim == world.DimensionOverworld {
			targetStore = s.store
		} else if ds, dErr := s.store.DimensionStore(normDim); dErr == nil {
			targetStore = ds
		}
	}

	regenerate := func() (*world.Chunk, error) {
		var gen world.Generator
		switch normDim {
		case world.DimensionNether:
			gen, _ = world.NewNetherGenerators(s.cfg.WorldSeed)
		case world.DimensionEnd:
			gen, _ = world.NewEndGenerators(s.cfg.WorldSeed)
		default:
			gen, _ = world.NewVanillaRegionGenerators(s.cfg.WorldSeed)
		}
		fresh := gen(cx, cz)
		if targetStore != nil && fresh != nil {
			_ = targetStore.SaveChunk(fresh)
		}
		return fresh, nil
	}

	defer func() {
		if r := recover(); r != nil {
			s.resilienceState().chunkPanicsRecovered.Add(1)
			recovered = true
			ch, err = regenerate()
		}
	}()

	if targetStore != nil {
		loaded, lErr := targetStore.LoadChunk(cx, cz)
		if lErr == nil && loaded != nil {
			return loaded, false, nil
		}
		if errors.Is(lErr, world.ErrChunkNotFound) {
			// Check if a vanilla chunk without RegionIOGeneratorVersion exists on disk.
			if vLoaded, vErr := targetStore.LoadVanillaChunk(cx, cz); vErr == nil && vLoaded != nil {
				_ = targetStore.SaveChunk(vLoaded)
				return vLoaded, false, nil
			}
		} else if lErr != nil {
			// Corrupted chunk on disk! Recover by regenerating and overwriting.
			s.recordCorruptedChunkRecovery()
			fresh, gErr := regenerate()
			return fresh, true, gErr
		}
	}

	fresh, gErr := regenerate()
	return fresh, false, gErr
}

// TickPhaseDurations records the wall-clock duration spent in each phase of a tick.
type TickPhaseDurations struct {
	TimeWeather   time.Duration
	ChunkTickets  time.Duration
	BlockTicks    time.Duration
	BlockEntities time.Duration
	Entities      time.Duration
	WorldBorder   time.Duration
	Total         time.Duration
}

// TickMetricsSnapshot provides a concurrency-safe summary of server tick performance.
type TickMetricsSnapshot struct {
	TotalTicks       int64
	AverageMSPT      float64
	MinMSPT          float64
	MaxMSPT          float64
	P50MSPT          float64
	P95MSPT          float64
	P99MSPT          float64
	TPS              float64
	OverloadWarnings int64
	SkippedTicks     int64
	LastPhaseOrder   []string
	LastPhases       TickPhaseDurations
}

const tickHistoryWindow = 100

type serverTickProfiler struct {
	mu               sync.RWMutex
	totalTicks       int64
	samples          [tickHistoryWindow]time.Duration
	sampleCount      int
	sampleWriteIdx   int
	overloadWarnings int64
	skippedTicks     int64
	lastPhaseOrder   []string
	lastPhases       TickPhaseDurations
}

var serverTickProfilers sync.Map // map[*Server]*serverTickProfiler

func (s *Server) tickProfiler() *serverTickProfiler {
	if s == nil {
		return &serverTickProfiler{}
	}
	if v, ok := serverTickProfilers.Load(s); ok {
		return v.(*serverTickProfiler)
	}
	actual, _ := serverTickProfilers.LoadOrStore(s, &serverTickProfiler{})
	return actual.(*serverTickProfiler)
}

// RecordTickOverload evaluates a tick loop lag duration against vanilla's 2000ms
// overload warning threshold ("Can't keep up! Is the server overloaded?"),
// recording skipped ticks (behind / 50ms) when behind >= 2000ms.
func (s *Server) RecordTickOverload(behind time.Duration) (warned bool, skipped int64) {
	if s == nil || behind < 2*time.Second {
		return false, 0
	}
	skipped = int64(behind / (50 * time.Millisecond))
	prof := s.tickProfiler()
	prof.mu.Lock()
	prof.overloadWarnings++
	prof.skippedTicks += skipped
	prof.mu.Unlock()
	return true, skipped
}

// StepServerTick executes one complete server tick in deterministic phase order:
//  1. time_weather   (clock advance, weather cycle, sleep night-skip)
//  2. chunk_tickets  (ticket TTL expiry, thunderstorm chunk lightning & ice/snow)
//  3. block_ticks    (scheduled block/fluid ticks, redstone, random ticks, cauldrons)
//  4. block_entities (furnaces, brewing stands, hoppers, dispensers, campfires, beacons, conduits, sculk)
//  5. entities       (player hunger, lingering clouds, villagers, raids, maps, entity tracker AI/physics)
//  6. world_border   (world border size lerp and outside-border damage)
func (s *Server) StepServerTick(rng *rand.Rand) TickMetricsSnapshot {
	if s == nil {
		return TickMetricsSnapshot{}
	}
	if rng == nil {
		gt, _ := s.WorldTime()
		rng = rand.New(rand.NewSource(gt + 0x5E27E2))
	}

	tickStart := time.Now()
	var phases TickPhaseDurations
	order := make([]string, 0, len(TickPhaseOrder))

	// Phase 1: time_weather
	t0 := time.Now()
	order = append(order, TickPhaseTimeWeather)
	gameTime, _ := s.advanceWorldTime()
	if gameTime%timeSyncTicks == 0 {
		s.Broadcast(protocol.PlaySetTime, s.SetTimePacket())
	}
	s.TickWeather(rng)
	s.TrySkipNightBySleep()
	phases.TimeWeather = time.Since(t0)

	// Phase 2: chunk_tickets
	t1 := time.Now()
	order = append(order, TickPhaseChunkTickets)
	s.TickChunkTickets()
	if s.IsRaining() && s.IsThundering() {
		for _, c := range s.ActiveTicketedChunks(world.DimensionOverworld) {
			if rng.Intn(100000) == 0 {
				_, _, _ = s.TickThunderstormChunkLightning(c[0], c[1], rng)
			}
		}
	}
	phases.ChunkTickets = time.Since(t1)

	// Phase 3: block_ticks
	t2 := time.Now()
	order = append(order, TickPhaseBlockTicks)
	s.TickAllBlockUpdates()
	s.TickCauldrons(rng)
	phases.BlockTicks = time.Since(t2)

	// Phase 4: block_entities
	t3 := time.Now()
	order = append(order, TickPhaseBlockEntities)
	s.TickBrewingStands()
	s.TickHoppers()
	s.TickCampfires()
	s.TickBeacons()
	s.TickConduits()
	s.TickSculk()
	phases.BlockEntities = time.Since(t3)

	// Phase 5: entities
	t4 := time.Now()
	order = append(order, TickPhaseEntities)
	s.TickAllPlayersHunger()
	s.TickLingeringClouds()
	s.TickVillagers()
	s.TickMaps()
	s.TickEntities(rng)
	phases.Entities = time.Since(t4)

	// Phase 6: world_border
	t5 := time.Now()
	order = append(order, TickPhaseWorldBorder)
	s.TickWorldBorder()
	phases.WorldBorder = time.Since(t5)

	phases.Total = time.Since(tickStart)
	if phases.Total <= 0 {
		phases.Total = time.Nanosecond
	}

	prof := s.tickProfiler()
	prof.mu.Lock()
	prof.totalTicks++
	prof.samples[prof.sampleWriteIdx] = phases.Total
	prof.sampleWriteIdx = (prof.sampleWriteIdx + 1) % tickHistoryWindow
	if prof.sampleCount < tickHistoryWindow {
		prof.sampleCount++
	}
	prof.lastPhaseOrder = order
	prof.lastPhases = phases
	prof.mu.Unlock()

	return s.TickMetrics()
}

// TickMetrics returns a snapshot of the server's rolling tick performance metrics.
func (s *Server) TickMetrics() TickMetricsSnapshot {
	if s == nil {
		return TickMetricsSnapshot{TPS: 20.0}
	}
	prof := s.tickProfiler()
	prof.mu.RLock()
	total := prof.totalTicks
	count := prof.sampleCount
	samples := make([]time.Duration, count)
	for i := 0; i < count; i++ {
		samples[i] = prof.samples[i]
	}
	warnings := prof.overloadWarnings
	skipped := prof.skippedTicks
	lastOrder := append([]string(nil), prof.lastPhaseOrder...)
	lastPhases := prof.lastPhases
	prof.mu.RUnlock()

	snap := TickMetricsSnapshot{
		TotalTicks:       total,
		TPS:              20.0,
		OverloadWarnings: warnings,
		SkippedTicks:     skipped,
		LastPhaseOrder:   lastOrder,
		LastPhases:       lastPhases,
	}
	if count == 0 {
		return snap
	}

	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	var sum time.Duration
	for _, d := range samples {
		sum += d
	}
	avgMS := float64(sum.Nanoseconds()) / float64(count) / 1e6
	snap.AverageMSPT = avgMS
	snap.MinMSPT = float64(samples[0].Nanoseconds()) / 1e6
	snap.MaxMSPT = float64(samples[count-1].Nanoseconds()) / 1e6
	snap.P50MSPT = float64(samples[(count-1)*50/100].Nanoseconds()) / 1e6
	snap.P95MSPT = float64(samples[(count-1)*95/100].Nanoseconds()) / 1e6
	snap.P99MSPT = float64(samples[(count-1)*99/100].Nanoseconds()) / 1e6
	if avgMS > 50.0 {
		snap.TPS = 1000.0 / avgMS
	} else {
		snap.TPS = 20.0
	}
	return snap
}

// TickThunderstormChunkLightning executes vanilla ServerLevel.tickThunder lightning
// strike logic for Overworld chunk (cx, cz) when it is thundering: picks a random
// column in the chunk (or uses (cx*16+8, cz*16+8) when rng is nil), resolves the
// surface strike Y (and any lightning rod redirection within 128 blocks), and spawns
// a lightning bolt (plus optional skeleton trap horse on natural unredirected strikes).
func (s *Server) TickThunderstormChunkLightning(cx, cz int32, rng *rand.Rand) (boltID int32, strikePos [3]float64, struck bool) {
	if s == nil || !s.IsThundering() {
		return 0, [3]float64{}, false
	}
	cache := s.DimensionChunks(world.DimensionOverworld)
	if cache == nil {
		return 0, [3]float64{}, false
	}
	ox := int(cx) * 16
	oz := int(cz) * 16
	lx, lz := 8, 8
	if rng != nil {
		v := rng.Intn(256)
		lx = v & 15
		lz = (v >> 4) & 15
	}
	bx := ox + lx
	bz := oz + lz
	by := world.FlatSurfaceY + 1
	if safeY, ok := cache.SafeSpawnY(bx, bz); ok {
		by = safeY
	}
	// Check if precipitation is blocked (e.g. dry biome like desert/savanna) unless redirected to a lightning rod.
	biomeName := biomeResourceNameByID(cache.GetBiomeAt(bx, by, bz))
	if strings.Contains(biomeName, "desert") || strings.Contains(biomeName, "savanna") || strings.Contains(biomeName, "badlands") {
		if _, _, _, redir := s.RedirectLightningToCopperRod(world.DimensionOverworld, bx, by, bz, 128); !redir {
			return 0, [3]float64{}, false
		}
	}
	id, sx, sy, sz, _ := s.SpawnLightningBolt(world.DimensionOverworld, float64(bx)+0.5, float64(by), float64(bz)+0.5, rng)
	if id <= 0 {
		return 0, [3]float64{}, false
	}
	return id, [3]float64{sx, sy, sz}, true
}

func biomeResourceNameByID(id uint16) string {
	for _, reg := range registry.Synced() {
		if reg.Name == "minecraft:worldgen/biome" {
			if int(id) < len(reg.Entries) {
				return reg.Entries[id]
			}
			break
		}
	}
	return "minecraft:plains"
}

func stringsToNBTList(items []string) nbt.List {
	elems := make([]nbt.Tag, len(items))
	for i, s := range items {
		elems[i] = nbt.String(s)
	}
	return nbt.List{ElemID: nbt.TagString, Elems: elems}
}

func nbtStringVal(c *nbt.Compound, key string) (string, bool) {
	if c == nil {
		return "", false
	}
	t, ok := c.Get(key)
	if !ok {
		return "", false
	}
	s, ok := t.(nbt.String)
	return string(s), ok
}

func nbtBoolVal(c *nbt.Compound, key string) (bool, bool) {
	if c == nil {
		return false, false
	}
	t, ok := c.Get(key)
	if !ok {
		return false, false
	}
	switch v := t.(type) {
	case nbt.Byte:
		return v != 0, true
	case nbt.Int:
		return v != 0, true
	case nbt.String:
		b, err := strconv.ParseBool(strings.TrimSpace(string(v)))
		return b, err == nil
	}
	return false, false
}

func nbtStringSlice(c *nbt.Compound, key string) ([]string, bool) {
	if c == nil {
		return nil, false
	}
	t, ok := c.Get(key)
	if !ok {
		return nil, false
	}
	l, ok := t.(nbt.List)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(l.Elems))
	for _, e := range l.Elems {
		if s, ok := e.(nbt.String); ok {
			out = append(out, string(s))
		}
	}
	return out, true
}
