package server

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Protocol 775 (26.1.2) packet IDs and entity event constants for difficulty and gamerules.
const (
	// PlayChangeDifficultyCB is ClientboundChangeDifficultyPacket (0x0a in protocol 775).
	PlayChangeDifficultyCB int32 = protocol.PlayChangeDifficulty
	// PlayChangeDifficultySB is ServerboundChangeDifficultyPacket (0x04 in protocol 775).
	PlayChangeDifficultySB int32 = protocol.PlayChangeDifficultySB
	// PlayLockDifficultySB is ServerboundLockDifficultyPacket (0x1d in protocol 775).
	PlayLockDifficultySB int32 = protocol.PlayLockDifficultySB

	// ClientboundEntityEventPacket (0x22) debug info event IDs.
	EntityEventReducedDebugInfo byte = 22
	EntityEventFullDebugInfo    byte = 23
)

// String returns the canonical lowercase name of the difficulty ("peaceful", "easy", "normal", "hard").
func (d Difficulty) String() string {
	switch d {
	case DifficultyPeaceful:
		return "peaceful"
	case DifficultyEasy:
		return "easy"
	case DifficultyNormal:
		return "normal"
	case DifficultyHard:
		return "hard"
	default:
		return "normal"
	}
}

// DisplayName returns the capitalized display label of the difficulty ("Peaceful", "Easy", "Normal", "Hard").
func (d Difficulty) DisplayName() string {
	switch d {
	case DifficultyPeaceful:
		return "Peaceful"
	case DifficultyEasy:
		return "Easy"
	case DifficultyNormal:
		return "Normal"
	case DifficultyHard:
		return "Hard"
	default:
		return "Normal"
	}
}

// TranslationKey returns the vanilla translation key for the difficulty.
func (d Difficulty) TranslationKey() string {
	return "options.difficulty." + d.String()
}

// ParseDifficulty parses a difficulty name, single-letter alias, or numeric ID (0..3).
func ParseDifficulty(raw string) (Difficulty, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "peaceful", "p", "0":
		return DifficultyPeaceful, true
	case "easy", "e", "1":
		return DifficultyEasy, true
	case "normal", "n", "2":
		return DifficultyNormal, true
	case "hard", "h", "3":
		return DifficultyHard, true
	default:
		return DifficultyNormal, false
	}
}

// EncodeChangeDifficultyPacket encodes ClientboundChangeDifficultyPacket (0x0a in protocol 775):
// Byte(difficulty), Bool(locked).
func EncodeChangeDifficultyPacket(d Difficulty, locked bool) []byte {
	if d > DifficultyHard {
		d = DifficultyNormal
	}
	w := protocol.NewWriter(2)
	w.Byte(byte(d))
	w.Bool(locked)
	return w.Bytes()
}

// DecodeChangeDifficultyPacket decodes ClientboundChangeDifficultyPacket (0x0a in protocol 775).
func DecodeChangeDifficultyPacket(r *protocol.Reader) (d Difficulty, locked bool, err error) {
	b, err := r.ReadByte()
	if err != nil {
		return DifficultyNormal, false, err
	}
	locked, err = r.Bool()
	if err != nil {
		return DifficultyNormal, false, err
	}
	if b > byte(DifficultyHard) {
		return DifficultyNormal, locked, fmt.Errorf("difficulty: invalid difficulty byte %d", b)
	}
	return Difficulty(b), locked, nil
}

// EncodeChangeDifficultySBPacket encodes ServerboundChangeDifficultyPacket (0x04 in protocol 775):
// Byte(difficulty).
func EncodeChangeDifficultySBPacket(d Difficulty) []byte {
	if d > DifficultyHard {
		d = DifficultyNormal
	}
	w := protocol.NewWriter(1)
	w.Byte(byte(d))
	return w.Bytes()
}

// DecodeChangeDifficultySBPacket decodes ServerboundChangeDifficultyPacket (0x04 in protocol 775).
func DecodeChangeDifficultySBPacket(r *protocol.Reader) (Difficulty, error) {
	b, err := r.ReadByte()
	if err != nil {
		return DifficultyNormal, err
	}
	if b > byte(DifficultyHard) {
		return DifficultyNormal, fmt.Errorf("difficulty: invalid difficulty byte %d", b)
	}
	return Difficulty(b), nil
}

// EncodeLockDifficultySBPacket encodes ServerboundLockDifficultyPacket (0x1d in protocol 775):
// Bool(locked).
func EncodeLockDifficultySBPacket(locked bool) []byte {
	w := protocol.NewWriter(1)
	w.Bool(locked)
	return w.Bytes()
}

// DecodeLockDifficultySBPacket decodes ServerboundLockDifficultyPacket (0x1d in protocol 775).
func DecodeLockDifficultySBPacket(r *protocol.Reader) (bool, error) {
	return r.Bool()
}

// GameRuleType identifies whether a game rule stores a boolean or an integer value.
type GameRuleType uint8

const (
	GameRuleTypeBool GameRuleType = iota
	GameRuleTypeInt
)

// GameRuleDef describes one vanilla 26.1.2 game rule and its default value.
type GameRuleDef struct {
	Name        string
	Type        GameRuleType
	DefaultBool bool
	DefaultInt  int
}

var vanillaGameRules = []GameRuleDef{
	// Boolean game rules
	{Name: "announceAdvancements", Type: GameRuleTypeBool, DefaultBool: true},
	{Name: "blockExplosionDropDecay", Type: GameRuleTypeBool, DefaultBool: true},
	{Name: "commandBlockOutput", Type: GameRuleTypeBool, DefaultBool: true},
	{Name: "disableElytraMovementCheck", Type: GameRuleTypeBool, DefaultBool: false},
	{Name: "disablePlayerMovementCheck", Type: GameRuleTypeBool, DefaultBool: false},
	{Name: "disableRaids", Type: GameRuleTypeBool, DefaultBool: false},
	{Name: "doDaylightCycle", Type: GameRuleTypeBool, DefaultBool: true},
	{Name: "doEntityDrops", Type: GameRuleTypeBool, DefaultBool: true},
	{Name: "doFireTick", Type: GameRuleTypeBool, DefaultBool: true},
	{Name: "doImmediateRespawn", Type: GameRuleTypeBool, DefaultBool: false},
	{Name: "doInsomnia", Type: GameRuleTypeBool, DefaultBool: true},
	{Name: "doLimitedCrafting", Type: GameRuleTypeBool, DefaultBool: false},
	{Name: "doMobLoot", Type: GameRuleTypeBool, DefaultBool: true},
	{Name: "doMobSpawning", Type: GameRuleTypeBool, DefaultBool: true},
	{Name: "doPatrolSpawning", Type: GameRuleTypeBool, DefaultBool: true},
	{Name: "doTileDrops", Type: GameRuleTypeBool, DefaultBool: true},
	{Name: "doTraderSpawning", Type: GameRuleTypeBool, DefaultBool: true},
	{Name: "doWardenSpawning", Type: GameRuleTypeBool, DefaultBool: true},
	{Name: "doWeatherCycle", Type: GameRuleTypeBool, DefaultBool: true},
	{Name: "drowningDamage", Type: GameRuleTypeBool, DefaultBool: true},
	{Name: "enderPearlsVanishOnDeath", Type: GameRuleTypeBool, DefaultBool: true},
	{Name: "fallDamage", Type: GameRuleTypeBool, DefaultBool: true},
	{Name: "fireDamage", Type: GameRuleTypeBool, DefaultBool: true},
	{Name: "forgiveDeadPlayers", Type: GameRuleTypeBool, DefaultBool: true},
	{Name: "freezeDamage", Type: GameRuleTypeBool, DefaultBool: true},
	{Name: "globalSoundEvents", Type: GameRuleTypeBool, DefaultBool: true},
	{Name: "keepInventory", Type: GameRuleTypeBool, DefaultBool: false},
	{Name: "lavaSourceConversion", Type: GameRuleTypeBool, DefaultBool: false},
	{Name: "logAdminCommands", Type: GameRuleTypeBool, DefaultBool: true},
	{Name: "mobExplosionDropDecay", Type: GameRuleTypeBool, DefaultBool: true},
	{Name: "mobGriefing", Type: GameRuleTypeBool, DefaultBool: true},
	{Name: "naturalRegeneration", Type: GameRuleTypeBool, DefaultBool: true},
	{Name: "projectilesCanBreakBlocks", Type: GameRuleTypeBool, DefaultBool: true},
	{Name: "pvp", Type: GameRuleTypeBool, DefaultBool: true},
	{Name: "reducedDebugInfo", Type: GameRuleTypeBool, DefaultBool: false},
	{Name: "sendCommandFeedback", Type: GameRuleTypeBool, DefaultBool: true},
	{Name: "showDeathMessages", Type: GameRuleTypeBool, DefaultBool: true},
	{Name: "spectatorsGenerateChunks", Type: GameRuleTypeBool, DefaultBool: true},
	{Name: "tntExplodes", Type: GameRuleTypeBool, DefaultBool: true},
	{Name: "tntExplosionDropDecay", Type: GameRuleTypeBool, DefaultBool: false},
	{Name: "universalAnger", Type: GameRuleTypeBool, DefaultBool: false},
	{Name: "waterSourceConversion", Type: GameRuleTypeBool, DefaultBool: true},

	// Integer game rules
	{Name: "commandModificationBlockLimit", Type: GameRuleTypeInt, DefaultInt: 32768},
	{Name: "maxCommandChainLength", Type: GameRuleTypeInt, DefaultInt: 65536},
	{Name: "maxCommandForkCount", Type: GameRuleTypeInt, DefaultInt: 65536},
	{Name: "maxEntityCramming", Type: GameRuleTypeInt, DefaultInt: 24},
	{Name: "minecartMaxSpeed", Type: GameRuleTypeInt, DefaultInt: 8},
	{Name: "playersNetherPortalCreativeDelay", Type: GameRuleTypeInt, DefaultInt: 1},
	{Name: "playersNetherPortalDefaultDelay", Type: GameRuleTypeInt, DefaultInt: 80},
	{Name: "playersSleepingPercentage", Type: GameRuleTypeInt, DefaultInt: 100},
	{Name: "randomTickSpeed", Type: GameRuleTypeInt, DefaultInt: 3},
	{Name: "snowAccumulationHeight", Type: GameRuleTypeInt, DefaultInt: 1},
	{Name: "spawnChunkRadius", Type: GameRuleTypeInt, DefaultInt: 2},
	{Name: "spawnRadius", Type: GameRuleTypeInt, DefaultInt: 10},
}

var gameRuleByNormalizedKey map[string]GameRuleDef

func init() {
	gameRuleByNormalizedKey = make(map[string]GameRuleDef, len(vanillaGameRules)*2)
	for _, def := range vanillaGameRules {
		key := normalizeGameRuleKey(def.Name)
		gameRuleByNormalizedKey[key] = def
	}
}

func normalizeGameRuleKey(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.TrimPrefix(s, "minecraft:")
	s = strings.ReplaceAll(s, "_", "")
	return s
}

// LookupGameRule resolves a game rule definition by name (case-insensitive and
// accepting both camelCase and snake_case).
func LookupGameRule(name string) (GameRuleDef, bool) {
	def, ok := gameRuleByNormalizedKey[normalizeGameRuleKey(name)]
	return def, ok
}

// AllGameRuleNames returns a sorted slice of all canonical vanilla game rule names.
func AllGameRuleNames() []string {
	names := make([]string, 0, len(vanillaGameRules))
	for _, def := range vanillaGameRules {
		names = append(names, def.Name)
	}
	sort.Strings(names)
	return names
}

type serverWorldSettings struct {
	mu               sync.RWMutex
	hardcore         bool
	difficultyLocked bool
	boolRules        map[string]bool
	intRules         map[string]int
}

var serverWorldSettingsMap sync.Map // map[*Server]*serverWorldSettings

func (s *Server) worldSettings() *serverWorldSettings {
	if s == nil {
		return nil
	}
	if v, ok := serverWorldSettingsMap.Load(s); ok {
		return v.(*serverWorldSettings)
	}
	boolMap := make(map[string]bool, len(vanillaGameRules))
	intMap := make(map[string]int, 16)
	for _, def := range vanillaGameRules {
		if def.Type == GameRuleTypeBool {
			boolMap[def.Name] = def.DefaultBool
		} else {
			intMap[def.Name] = def.DefaultInt
		}
	}
	created := &serverWorldSettings{
		boolRules: boolMap,
		intRules:  intMap,
	}
	actual, _ := serverWorldSettingsMap.LoadOrStore(s, created)
	return actual.(*serverWorldSettings)
}

// IsHardcore reports whether the server is running in Hardcore mode.
func (s *Server) IsHardcore() bool {
	if s == nil {
		return false
	}
	ws := s.worldSettings()
	ws.mu.RLock()
	hc := ws.hardcore
	ws.mu.RUnlock()
	return hc
}

// SetHardcore enables or disables Hardcore mode. Enabling Hardcore mode forces
// the server difficulty to DifficultyHard and locks difficulty changes.
func (s *Server) SetHardcore(hardcore bool) {
	if s == nil {
		return
	}
	ws := s.worldSettings()
	ws.mu.Lock()
	ws.hardcore = hardcore
	if hardcore {
		ws.difficultyLocked = true
	}
	ws.mu.Unlock()
	if hardcore {
		s.SetDifficulty(DifficultyHard)
		s.BroadcastDifficulty()
	}
}

// DifficultyLocked reports whether the world's difficulty is locked.
func (s *Server) DifficultyLocked() bool {
	if s == nil {
		return false
	}
	ws := s.worldSettings()
	ws.mu.RLock()
	locked := ws.difficultyLocked || ws.hardcore
	ws.mu.RUnlock()
	return locked
}

// SetDifficultyLocked locks or unlocks the world's difficulty and broadcasts
// ClientboundChangeDifficultyPacket (0x0a) to all connected players.
func (s *Server) SetDifficultyLocked(locked bool) {
	if s == nil {
		return
	}
	ws := s.worldSettings()
	ws.mu.Lock()
	if ws.hardcore {
		locked = true
	}
	ws.difficultyLocked = locked
	ws.mu.Unlock()
	s.BroadcastDifficulty()
}

// EncodeServerDifficultyPacket encodes ClientboundChangeDifficultyPacket (0x0a)
// for the server's current difficulty and lock state.
func (s *Server) EncodeServerDifficultyPacket() []byte {
	if s == nil {
		return EncodeChangeDifficultyPacket(DifficultyNormal, false)
	}
	return EncodeChangeDifficultyPacket(s.Difficulty(), s.DifficultyLocked())
}

// SendDifficultyToPlayer sends the server's current difficulty and lock state
// to session via ClientboundChangeDifficultyPacket (0x0a).
func (s *Server) SendDifficultyToPlayer(session *PlayerSession) {
	if s == nil || session == nil || session.send == nil {
		return
	}
	_ = session.send(PlayChangeDifficultyCB, s.EncodeServerDifficultyPacket())
}

// BroadcastDifficulty broadcasts ClientboundChangeDifficultyPacket (0x0a) to all
// connected players.
func (s *Server) BroadcastDifficulty() {
	if s == nil || s.players == nil {
		return
	}
	s.Broadcast(PlayChangeDifficultyCB, s.EncodeServerDifficultyPacket())
}

// IsHostileMobType reports whether typeName is a hostile mob that despawns in Peaceful difficulty.
func IsHostileMobType(typeName string) bool {
	name := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(typeName)), "minecraft:")
	switch name {
	case "zombie", "skeleton", "creeper", "spider", "cave_spider", "enderman", "endermite",
		"witch", "slime", "magma_cube", "blaze", "ghast", "wither_skeleton", "hoglin", "zoglin",
		"piglin_brute", "zombified_piglin", "drowned", "husk", "stray", "bogged", "phantom",
		"silverfish", "vindicator", "evoker", "pillager", "ravager", "vex", "guardian",
		"elder_guardian", "shulker", "warden", "breeze", "creaking":
		return true
	default:
		return false
	}
}

// DespawnHostileMobs removes all hostile mobs across all dimensions and
// broadcasts PlayRemoveEntities to nearby viewers (used in Peaceful difficulty).
func (s *Server) DespawnHostileMobs() int {
	if s == nil || s.entities == nil {
		return 0
	}
	all := s.entities.All()
	var removed int
	for _, ent := range all {
		if ent.IsItem() || !IsHostileMobType(ent.TypeName) {
			continue
		}
		s.entities.Remove(ent.ID)
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, protocol.PlayRemoveEntities,
			encodeRemoveEntitiesPacket(nil, []int32{ent.ID}))
		removed++
	}
	return removed
}

// ChangeDifficulty updates the server's active difficulty, despawns hostile mobs
// if switching to Peaceful, broadcasts ClientboundChangeDifficultyPacket (0x0a),
// and persists world settings. In Hardcore mode, difficulty remains Hard.
func (s *Server) ChangeDifficulty(d Difficulty) bool {
	if s == nil {
		return false
	}
	if d > DifficultyHard {
		d = DifficultyNormal
	}
	if s.IsHardcore() {
		d = DifficultyHard
	}
	prev := s.Difficulty()
	s.SetDifficulty(d)
	if d == DifficultyPeaceful {
		s.DespawnHostileMobs()
	}
	s.BroadcastDifficulty()
	_ = s.SaveWorldSettingsFile()
	return prev != d
}

// HandleChangeDifficultyPacket processes ServerboundChangeDifficultyPacket (0x04).
func (s *Server) HandleChangeDifficultyPacket(session *PlayerSession, r *protocol.Reader) error {
	d, err := DecodeChangeDifficultySBPacket(r)
	if err != nil {
		return err
	}
	if s == nil || !s.HasAdminPermission(session) || s.DifficultyLocked() || s.IsHardcore() {
		return nil
	}
	s.ChangeDifficulty(d)
	return nil
}

// HandleLockDifficultyPacket processes ServerboundLockDifficultyPacket (0x1d).
func (s *Server) HandleLockDifficultyPacket(session *PlayerSession, r *protocol.Reader) error {
	locked, err := DecodeLockDifficultySBPacket(r)
	if err != nil {
		return err
	}
	if s == nil || !s.HasAdminPermission(session) {
		return nil
	}
	s.SetDifficultyLocked(locked)
	_ = s.SaveWorldSettingsFile()
	return nil
}

// HandleDifficultyCommand implements the vanilla /difficulty command:
//   - /difficulty
//   - /difficulty <peaceful|easy|normal|hard>
func (s *Server) HandleDifficultyCommand(sender *PlayerSession, args []string) string {
	if s == nil {
		return "Server unavailable"
	}
	if !s.HasAdminPermission(sender) {
		return "You do not have permission to use this command"
	}
	if len(args) == 0 {
		return fmt.Sprintf("The difficulty is %s", s.Difficulty().DisplayName())
	}
	if len(args) > 1 {
		return "Usage: /difficulty [peaceful|easy|normal|hard]"
	}
	d, ok := ParseDifficulty(args[0])
	if !ok {
		return fmt.Sprintf("Unknown difficulty %q. Expected peaceful, easy, normal, or hard", args[0])
	}
	if s.IsHardcore() && d != DifficultyHard {
		return "The difficulty cannot be changed in Hardcore Mode"
	}
	changed := s.ChangeDifficulty(d)
	if !changed {
		return fmt.Sprintf("The difficulty did not change; it is already set to %s", d.DisplayName())
	}
	return fmt.Sprintf("The difficulty has been set to %s", d.DisplayName())
}

// GameRuleBool returns the current boolean value of a game rule (or false if unknown).
func (s *Server) GameRuleBool(name string) bool {
	def, ok := LookupGameRule(name)
	if !ok || def.Type != GameRuleTypeBool {
		return false
	}
	if s == nil {
		return def.DefaultBool
	}
	switch def.Name {
	case "doDaylightCycle":
		return s.DaylightCycle()
	case "doWeatherCycle":
		return s.WeatherCycle()
	case "naturalRegeneration":
		return s.NaturalRegeneration()
	}
	ws := s.worldSettings()
	ws.mu.RLock()
	val, exists := ws.boolRules[def.Name]
	ws.mu.RUnlock()
	if !exists {
		return def.DefaultBool
	}
	return val
}

// SetGameRuleBool sets a boolean game rule and triggers any protocol/server side effects.
func (s *Server) SetGameRuleBool(name string, val bool) bool {
	if s == nil {
		return false
	}
	def, ok := LookupGameRule(name)
	if !ok || def.Type != GameRuleTypeBool {
		return false
	}
	ws := s.worldSettings()
	ws.mu.Lock()
	ws.boolRules[def.Name] = val
	ws.mu.Unlock()

	switch def.Name {
	case "doDaylightCycle":
		s.SetDaylightCycle(val)
	case "doWeatherCycle":
		s.SetWeatherCycle(val)
	case "naturalRegeneration":
		s.SetNaturalRegeneration(val)
	case "doImmediateRespawn":
		v := float32(0.0)
		if val {
			v = 1.0
		}
		if s.players != nil {
			s.Broadcast(protocol.PlayGameEvent, EncodeWeatherGameEvent(protocol.GameEventEnableRespawnScreen, v))
		}
	case "doLimitedCrafting":
		v := float32(0.0)
		if val {
			v = 1.0
		}
		if s.players != nil {
			s.Broadcast(protocol.PlayGameEvent, EncodeWeatherGameEvent(protocol.GameEventLimitedCrafting, v))
		}
	case "reducedDebugInfo":
		ev := EntityEventFullDebugInfo
		if val {
			ev = EntityEventReducedDebugInfo
		}
		for _, p := range s.onlinePlayersSnapshot() {
			if p != nil && p.send != nil {
				_ = p.send(protocol.PlayEntityEvent, EncodeEntityEventPacket(p.EntityID, ev))
			}
		}
	}
	_ = s.SaveWorldSettingsFile()
	return true
}

// GameRuleInt returns the current integer value of a game rule (or 0 if unknown).
func (s *Server) GameRuleInt(name string) int {
	def, ok := LookupGameRule(name)
	if !ok || def.Type != GameRuleTypeInt {
		return 0
	}
	if s == nil {
		return def.DefaultInt
	}
	if def.Name == "randomTickSpeed" && s.chunks != nil {
		return s.RandomTickSpeed()
	}
	ws := s.worldSettings()
	ws.mu.RLock()
	val, exists := ws.intRules[def.Name]
	ws.mu.RUnlock()
	if !exists {
		return def.DefaultInt
	}
	return val
}

// SetGameRuleInt sets an integer game rule and triggers any server side effects.
func (s *Server) SetGameRuleInt(name string, val int) bool {
	if s == nil {
		return false
	}
	def, ok := LookupGameRule(name)
	if !ok || def.Type != GameRuleTypeInt {
		return false
	}
	if def.Name == "randomTickSpeed" && val < 0 {
		val = 0
	}
	ws := s.worldSettings()
	ws.mu.Lock()
	ws.intRules[def.Name] = val
	ws.mu.Unlock()

	switch def.Name {
	case "randomTickSpeed":
		s.SetRandomTickSpeed(val)
	case "playersSleepingPercentage":
		s.TrySkipNightBySleep()
	}
	_ = s.SaveWorldSettingsFile()
	return true
}

// GameRuleString returns the canonical rule name and string-formatted current value.
func (s *Server) GameRuleString(name string) (canonName, value string, ok bool) {
	def, ok := LookupGameRule(name)
	if !ok {
		return "", "", false
	}
	if def.Type == GameRuleTypeBool {
		return def.Name, strconv.FormatBool(s.GameRuleBool(def.Name)), true
	}
	return def.Name, strconv.Itoa(s.GameRuleInt(def.Name)), true
}

// SetGameRuleFromString parses rawValue according to the rule's type and updates it.
func (s *Server) SetGameRuleFromString(name, rawValue string) (canonName, formattedValue string, err error) {
	def, ok := LookupGameRule(name)
	if !ok {
		return "", "", fmt.Errorf("Unknown game rule: %s", name)
	}
	trimmed := strings.TrimSpace(rawValue)
	if def.Type == GameRuleTypeBool {
		val, err := strconv.ParseBool(trimmed)
		if err != nil {
			return def.Name, "", fmt.Errorf("Invalid boolean value: %s", rawValue)
		}
		s.SetGameRuleBool(def.Name, val)
		return def.Name, strconv.FormatBool(val), nil
	}
	val, err := strconv.Atoi(trimmed)
	if err != nil || (def.Name == "randomTickSpeed" && val < 0) || (def.Name == "spawnRadius" && val < 0) {
		return def.Name, "", fmt.Errorf("Invalid integer value: %s", rawValue)
	}
	s.SetGameRuleInt(def.Name, val)
	return def.Name, strconv.Itoa(val), nil
}

// HandleGameRuleCommand implements the vanilla /gamerule command for all 54
// boolean and integer game rules:
//   - /gamerule
//   - /gamerule <rule>
//   - /gamerule <rule> <value>
func (s *Server) HandleGameRuleCommand(sender *PlayerSession, args []string) string {
	if s == nil {
		return "Server unavailable"
	}
	if !s.HasAdminPermission(sender) {
		return "You do not have permission to use this command"
	}
	if len(args) == 0 {
		names := AllGameRuleNames()
		return fmt.Sprintf("Available game rules (%d): %s", len(names), strings.Join(names, ", "))
	}
	if len(args) > 2 {
		return "Usage: /gamerule <rule> [value]"
	}
	if len(args) == 1 {
		canon, val, ok := s.GameRuleString(args[0])
		if !ok {
			return fmt.Sprintf("Unknown game rule: %s", args[0])
		}
		return fmt.Sprintf("Gamerule %s is currently set to: %s", canon, val)
	}
	canon, val, err := s.SetGameRuleFromString(args[0], args[1])
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("Gamerule %s is now set to: %s", canon, val)
}

// Convenience typed accessors for common gameplay gamerules.

func (s *Server) KeepInventory() bool          { return s.GameRuleBool("keepInventory") }
func (s *Server) SetKeepInventory(v bool)      { s.SetGameRuleBool("keepInventory", v) }
func (s *Server) DoMobSpawning() bool          { return s.GameRuleBool("doMobSpawning") }
func (s *Server) SetDoMobSpawning(v bool)      { s.SetGameRuleBool("doMobSpawning", v) }
func (s *Server) DoMobLoot() bool              { return s.GameRuleBool("doMobLoot") }
func (s *Server) SetDoMobLoot(v bool)          { s.SetGameRuleBool("doMobLoot", v) }
func (s *Server) DoTileDrops() bool            { return s.GameRuleBool("doTileDrops") }
func (s *Server) SetDoTileDrops(v bool)        { s.SetGameRuleBool("doTileDrops", v) }
func (s *Server) DoEntityDrops() bool          { return s.GameRuleBool("doEntityDrops") }
func (s *Server) SetDoEntityDrops(v bool)      { s.SetGameRuleBool("doEntityDrops", v) }
func (s *Server) MobGriefing() bool            { return s.GameRuleBool("mobGriefing") }
func (s *Server) SetMobGriefing(v bool)        { s.SetGameRuleBool("mobGriefing", v) }
func (s *Server) DoImmediateRespawn() bool     { return s.GameRuleBool("doImmediateRespawn") }
func (s *Server) SetDoImmediateRespawn(v bool) { s.SetGameRuleBool("doImmediateRespawn", v) }
func (s *Server) ReducedDebugInfo() bool       { return s.GameRuleBool("reducedDebugInfo") }
func (s *Server) SetReducedDebugInfo(v bool)   { s.SetGameRuleBool("reducedDebugInfo", v) }
func (s *Server) DoLimitedCrafting() bool      { return s.GameRuleBool("doLimitedCrafting") }
func (s *Server) SetDoLimitedCrafting(v bool)  { s.SetGameRuleBool("doLimitedCrafting", v) }
func (s *Server) FallDamage() bool             { return s.GameRuleBool("fallDamage") }
func (s *Server) SetFallDamage(v bool)         { s.SetGameRuleBool("fallDamage", v) }
func (s *Server) FireDamage() bool             { return s.GameRuleBool("fireDamage") }
func (s *Server) SetFireDamage(v bool)         { s.SetGameRuleBool("fireDamage", v) }
func (s *Server) DrowningDamage() bool         { return s.GameRuleBool("drowningDamage") }
func (s *Server) SetDrowningDamage(v bool)     { s.SetGameRuleBool("drowningDamage", v) }
func (s *Server) FreezeDamage() bool           { return s.GameRuleBool("freezeDamage") }
func (s *Server) SetFreezeDamage(v bool)       { s.SetGameRuleBool("freezeDamage", v) }
func (s *Server) PvP() bool                    { return s.GameRuleBool("pvp") }
func (s *Server) SetPvP(v bool)                { s.SetGameRuleBool("pvp", v) }
func (s *Server) ShowDeathMessages() bool      { return s.GameRuleBool("showDeathMessages") }
func (s *Server) SetShowDeathMessages(v bool)  { s.SetGameRuleBool("showDeathMessages", v) }
func (s *Server) SendCommandFeedback() bool    { return s.GameRuleBool("sendCommandFeedback") }
func (s *Server) SetSendCommandFeedback(v bool) {
	s.SetGameRuleBool("sendCommandFeedback", v)
}
func (s *Server) AnnounceAdvancements() bool     { return s.GameRuleBool("announceAdvancements") }
func (s *Server) SetAnnounceAdvancements(v bool) { s.SetGameRuleBool("announceAdvancements", v) }
func (s *Server) PlayersSleepingPercentage() int { return s.GameRuleInt("playersSleepingPercentage") }
func (s *Server) SetPlayersSleepingPercentage(v int) {
	s.SetGameRuleInt("playersSleepingPercentage", v)
}
func (s *Server) SpawnRadius() int      { return s.GameRuleInt("spawnRadius") }
func (s *Server) SetSpawnRadius(v int)  { s.SetGameRuleInt("spawnRadius", v) }

// DropPlayerDeathInventoryAndXP drops all items in session's inventory (except
// items with minecraft:vanishing_curse) and experience orbs (min(level*7, 100))
// at session's position, clearing session's inventory, carried cursor item, and
// experience when keepInventory is false.
func (s *Server) DropPlayerDeathInventoryAndXP(session *PlayerSession) (droppedItemIDs []int32, droppedXP int32) {
	if s == nil || session == nil || s.KeepInventory() {
		return nil, 0
	}
	snap := session.Snapshot()
	dim := world.NormalizeDimension(snap.Dimension)

	type droppedSlot struct {
		slot  int
		stack world.ItemStack
		vanish bool
	}
	var toDrop []droppedSlot
	for slot := 0; slot < world.PlayerInventorySlots; slot++ {
		st := session.InventorySlot(slot)
		if st.IsEmpty() {
			continue
		}
		meta := GetSlotItemMeta(session, slot)
		vanish := meta.EnchantmentLevel("minecraft:vanishing_curse") > 0
		toDrop = append(toDrop, droppedSlot{slot: slot, stack: st, vanish: vanish})
		session.SetInventorySlot(slot, world.ItemStack{})
		SetSlotItemMeta(session, slot, ItemMeta{})
		SetSlotPotionType(session, slot, "")
		ClearSlotMapID(session, slot)
		ClearSlotArmorTrim(session, slot)
		ClearSlotBannerMeta(session, slot)
	}
	carried := session.CarriedItem()
	carriedMeta := GetCarriedItemMeta(session)
	if !carried.IsEmpty() {
		vanish := carriedMeta.EnchantmentLevel("minecraft:vanishing_curse") > 0
		toDrop = append(toDrop, droppedSlot{slot: -1, stack: carried, vanish: vanish})
		session.SetCarriedItem(world.ItemStack{})
	}

	for _, ds := range toDrop {
		if ds.vanish {
			continue
		}
		if id, _ := s.SpawnItemEntityInDimension(dim, snap.X, snap.Y+0.5, snap.Z, ds.stack); id != 0 {
			droppedItemIDs = append(droppedItemIDs, id)
		}
	}

	lvl, totalXP, _ := session.Experience()
	if lvl > 0 || totalXP > 0 {
		if s.DoMobLoot() {
			xpDrop := lvl * 7
			if xpDrop > 100 {
				xpDrop = 100
			}
			if xpDrop > 0 {
				droppedXP = xpDrop
				s.SpawnExperienceOrbsInDimension(dim, snap.X, snap.Y+0.5, snap.Z, xpDrop)
			}
		}
		s.SetPlayerExperience(session, 0, 0, 0)
	}

	if session.send != nil {
		inv := session.Inventory()
		stateID := session.NextContainerStateID()
		_ = session.send(protocol.PlayContainerSetContent, encodeContainerSetContentPacket(stateID, inv))
	}
	return droppedItemIDs, droppedXP
}

// SaveWorldSettingsNBT serializes the server's difficulty, difficultyLocked,
// hardcore flag, and GameRules compound into NBT (matching vanilla level.dat format).
func (s *Server) SaveWorldSettingsNBT() *nbt.Compound {
	root := nbt.NewCompound()
	if s == nil {
		return root
	}
	root.Set("Difficulty", nbt.Byte(int8(s.Difficulty())))
	lockedByte := int8(0)
	if s.DifficultyLocked() {
		lockedByte = 1
	}
	root.Set("DifficultyLocked", nbt.Byte(lockedByte))
	hardcoreByte := int8(0)
	if s.IsHardcore() {
		hardcoreByte = 1
	}
	root.Set("hardcore", nbt.Byte(hardcoreByte))

	rulesComp := nbt.NewCompound()
	for _, def := range vanillaGameRules {
		_, valStr, ok := s.GameRuleString(def.Name)
		if ok {
			rulesComp.Set(def.Name, nbt.String(valStr))
		}
	}
	root.Set("GameRules", rulesComp)

	spCfg := getWorldSpawnConfig(s)
	spCfg.mu.RLock()
	if spCfg.custom {
		root.Set("SpawnCustom", nbt.Byte(1))
		root.Set("SpawnX", nbt.Int(int32(spCfg.x)))
		root.Set("SpawnY", nbt.Int(int32(spCfg.y)))
		root.Set("SpawnZ", nbt.Int(int32(spCfg.z)))
		root.Set("SpawnAngle", nbt.Float(spCfg.yaw))
	}
	spCfg.mu.RUnlock()
	return root
}

// LoadWorldSettingsNBT restores the server's difficulty, difficultyLocked,
// hardcore flag, and GameRules compound from NBT.
func (s *Server) LoadWorldSettingsNBT(root *nbt.Compound) {
	if s == nil || root == nil {
		return
	}
	if tag, ok := root.Get("Difficulty"); ok {
		if b, ok := tag.(nbt.Byte); ok && b >= 0 && b <= nbt.Byte(DifficultyHard) {
			s.SetDifficulty(Difficulty(b))
		}
	}
	if tag, ok := root.Get("DifficultyLocked"); ok {
		if b, ok := tag.(nbt.Byte); ok {
			ws := s.worldSettings()
			ws.mu.Lock()
			ws.difficultyLocked = b != 0
			ws.mu.Unlock()
		}
	}
	if tag, ok := root.Get("hardcore"); ok {
		if b, ok := tag.(nbt.Byte); ok {
			hc := b != 0
			ws := s.worldSettings()
			ws.mu.Lock()
			ws.hardcore = hc
			if hc {
				ws.difficultyLocked = true
			}
			ws.mu.Unlock()
			if hc {
				s.SetDifficulty(DifficultyHard)
			}
		}
	}
	if tag, ok := root.Get("GameRules"); ok {
		if rulesComp, ok := tag.(*nbt.Compound); ok && rulesComp != nil {
			for _, def := range vanillaGameRules {
				if vTag, exists := rulesComp.Get(def.Name); exists {
					if strVal, ok := vTag.(nbt.String); ok {
						if def.Type == GameRuleTypeBool {
							if b, err := strconv.ParseBool(strings.TrimSpace(string(strVal))); err == nil {
								ws := s.worldSettings()
								ws.mu.Lock()
								ws.boolRules[def.Name] = b
								ws.mu.Unlock()
								switch def.Name {
								case "doDaylightCycle":
									s.SetDaylightCycle(b)
								case "doWeatherCycle":
									s.SetWeatherCycle(b)
								case "naturalRegeneration":
									s.SetNaturalRegeneration(b)
								}
							}
						} else {
							if iv, err := strconv.Atoi(strings.TrimSpace(string(strVal))); err == nil {
								ws := s.worldSettings()
								ws.mu.Lock()
								ws.intRules[def.Name] = iv
								ws.mu.Unlock()
								if def.Name == "randomTickSpeed" && iv >= 0 {
									s.SetRandomTickSpeed(iv)
								}
							}
						}
					}
				}
			}
		}
	}
	if tag, ok := root.Get("SpawnCustom"); ok {
		if b, ok := tag.(nbt.Byte); ok && b != 0 {
			var sx, sy, sz int32
			var syaw float32
			if t, ok := root.Get("SpawnX"); ok {
				if v, ok := t.(nbt.Int); ok {
					sx = int32(v)
				}
			}
			if t, ok := root.Get("SpawnY"); ok {
				if v, ok := t.(nbt.Int); ok {
					sy = int32(v)
				}
			}
			if t, ok := root.Get("SpawnZ"); ok {
				if v, ok := t.(nbt.Int); ok {
					sz = int32(v)
				}
			}
			if t, ok := root.Get("SpawnAngle"); ok {
				if v, ok := t.(nbt.Float); ok {
					syaw = float32(v)
				}
			}
			spCfg := getWorldSpawnConfig(s)
			spCfg.mu.Lock()
			spCfg.custom = true
			spCfg.x = int(sx)
			spCfg.y = int(sy)
			spCfg.z = int(sz)
			spCfg.yaw = syaw
			spCfg.mu.Unlock()
		}
	}
}

func (s *Server) worldSettingsFilePath() string {
	if s == nil || s.cfg.WorldDir == "" {
		return ""
	}
	return filepath.Join(s.cfg.WorldDir, "data", "gamerules.dat")
}

// SaveWorldSettingsFile writes the server's difficulty, hardcore, and gamerules
// state to <WorldDir>/data/gamerules.dat.
func (s *Server) SaveWorldSettingsFile() error {
	path := s.worldSettingsFilePath()
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw := nbt.MarshalNamed("", s.SaveWorldSettingsNBT())
	return os.WriteFile(path, raw, 0o644)
}

// LoadWorldSettingsFile loads the server's difficulty, hardcore, and gamerules
// state from <WorldDir>/data/gamerules.dat if present.
func (s *Server) LoadWorldSettingsFile() error {
	path := s.worldSettingsFilePath()
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	_, tag, err := nbt.UnmarshalNamed(raw)
	if err != nil {
		return err
	}
	comp, ok := tag.(*nbt.Compound)
	if !ok {
		return nil
	}
	s.LoadWorldSettingsNBT(comp)
	return nil
}

// EncodePlayerLoginPacket encodes the clientbound play login (0x31) packet body
// for session, populated with the server's hardcore flag and gamerules
// (reducedDebugInfo, doImmediateRespawn, doLimitedCrafting).
func (s *Server) EncodePlayerLoginPacket(session *PlayerSession, viewDistance int32) []byte {
	var (
		entityID int32 = 1
		gameMode       = GameModeCreative
		dim            = world.DimensionOverworld
	)
	if session != nil {
		snap := session.Snapshot()
		entityID = snap.EntityID
		gameMode = snap.GameMode
		dim = snap.Dimension
	}
	maxPlayers := int32(20)
	var seed int64
	isHardcore := false
	reducedDebug := false
	enableRespawnScreen := true
	limitedCrafting := false
	if s != nil {
		maxPlayers = int32(s.cfg.MaxPlayers)
		seed = s.cfg.WorldSeed
		isHardcore = s.IsHardcore()
		reducedDebug = s.ReducedDebugInfo()
		enableRespawnScreen = !s.DoImmediateRespawn()
		limitedCrafting = s.DoLimitedCrafting()
	}
	if viewDistance <= 0 {
		viewDistance = 4
	}
	return EncodePlayLoginPacketWithFlags(
		entityID,
		gameMode,
		dim,
		maxPlayers,
		viewDistance,
		seed,
		isHardcore,
		reducedDebug,
		enableRespawnScreen,
		limitedCrafting,
	)
}

// CanSkipNightWithSleepingPlayers reports whether the current number of sleeping
// players in the Overworld satisfies the playersSleepingPercentage gamerule.
func (s *Server) CanSkipNightWithSleepingPlayers(sleepingCount, eligibleCount int) bool {
	if eligibleCount <= 0 || sleepingCount <= 0 {
		return false
	}
	pct := s.PlayersSleepingPercentage()
	if pct > 100 {
		return false
	}
	if pct <= 0 {
		return sleepingCount >= 1
	}
	required := int(math.Ceil(float64(eligibleCount) * float64(pct) / 100.0))
	if required < 1 {
		required = 1
	}
	return sleepingCount >= required
}

// DecodeAnimatePacket decodes ClientboundAnimatePacket (0x02).
func DecodeAnimatePacket(r *protocol.Reader) (entityID int32, action byte, err error) {
	entityID, err = r.VarInt()
	if err != nil {
		return 0, 0, err
	}
	action, err = r.ReadByte()
	if err != nil {
		return 0, 0, err
	}
	return entityID, action, nil
}

// EncodeSwingSBPacket encodes ServerboundSwingPacket (0x3f): VarInt(hand) where 0=main, 1=off.
func EncodeSwingSBPacket(hand int32) []byte {
	w := protocol.NewWriter(2)
	w.VarInt(hand)
	return w.Bytes()
}

// DecodeSwingSBPacket decodes ServerboundSwingPacket (0x3f).
func DecodeSwingSBPacket(r *protocol.Reader) (int32, error) {
	return r.VarInt()
}

// HandlePlayerSwingPacket processes ServerboundSwingPacket (0x3f): resets the
// player's attack cooldown and broadcasts ClientboundAnimatePacket (0x02) to
// nearby viewers in the same dimension.
func (s *Server) HandlePlayerSwingPacket(session *PlayerSession, r *protocol.Reader) error {
	hand, err := DecodeSwingSBPacket(r)
	if err != nil {
		return err
	}
	if session == nil {
		return nil
	}
	session.ResetAttackCooldown()
	if s == nil {
		return nil
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator {
		return nil
	}
	action := AnimateSwingMainHand
	if hand == 1 {
		action = AnimateSwingOffHand
	}
	cx := int32(int64(math.Floor(snap.X)) >> 4)
	cz := int32(int64(math.Floor(snap.Z)) >> 4)
	s.BroadcastDimensionChunkExcept(
		world.NormalizeDimension(snap.Dimension),
		cx, cz,
		session.Profile.UUID,
		protocol.PlayAnimate,
		EncodeAnimatePacket(session.EntityID, action),
	)
	return nil
}

// EncodePingRequestSBPacket encodes ServerboundPingRequestPacket (0x26): Int64(time).
func EncodePingRequestSBPacket(timePayload int64) []byte {
	w := protocol.NewWriter(8)
	w.Int64(timePayload)
	return w.Bytes()
}

// DecodePingRequestSBPacket decodes ServerboundPingRequestPacket (0x26).
func DecodePingRequestSBPacket(r *protocol.Reader) (int64, error) {
	return r.Int64()
}

// EncodePongResponsePacket encodes ClientboundPongResponsePacket (0x3e): Int64(time).
func EncodePongResponsePacket(timePayload int64) []byte {
	w := protocol.NewWriter(8)
	w.Int64(timePayload)
	return w.Bytes()
}

// DecodePongResponsePacket decodes ClientboundPongResponsePacket (0x3e).
func DecodePongResponsePacket(r *protocol.Reader) (int64, error) {
	return r.Int64()
}

// HandlePingRequestPacket processes ServerboundPingRequestPacket (0x26) in Play
// state and replies immediately with ClientboundPongResponsePacket (0x3e).
func (s *Server) HandlePingRequestPacket(session *PlayerSession, r *protocol.Reader) error {
	payload, err := DecodePingRequestSBPacket(r)
	if err != nil {
		return err
	}
	if session != nil && session.send != nil {
		return session.send(protocol.PlayPongResponse, EncodePongResponsePacket(payload))
	}
	return nil
}

// EncodeChangeGameModeSBPacket encodes ServerboundChangeGameModePacket (0x05): VarInt(mode).
func EncodeChangeGameModeSBPacket(mode uint8) []byte {
	w := protocol.NewWriter(2)
	w.VarInt(int32(mode))
	return w.Bytes()
}

// DecodeChangeGameModeSBPacket decodes ServerboundChangeGameModePacket (0x05).
func DecodeChangeGameModeSBPacket(r *protocol.Reader) (uint8, error) {
	mode, err := r.VarInt()
	if err != nil {
		return 0, err
	}
	if mode < 0 || mode > int32(GameModeSpectator) {
		return 0, fmt.Errorf("gamemode: invalid mode %d", mode)
	}
	return uint8(mode), nil
}

// HandleChangeGameModePacket processes ServerboundChangeGameModePacket (0x05,
// F3+F4 game mode switcher in 26.1.2) for operators.
func (s *Server) HandleChangeGameModePacket(session *PlayerSession, r *protocol.Reader) error {
	mode, err := DecodeChangeGameModeSBPacket(r)
	if err != nil {
		return err
	}
	if s == nil || session == nil || !s.HasAdminPermission(session) {
		return nil
	}
	s.SetPlayerGameMode(session, mode)
	return nil
}

// EncodeSetGameRuleSBPacket encodes ServerboundSetGameRulePacket (0x39):
// VarInt(count), followed by (ResourceKey<GameRule>, String value) entries.
func EncodeSetGameRuleSBPacket(entries map[string]string) []byte {
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	w := protocol.NewWriter(32 * (len(keys) + 1))
	w.VarInt(int32(len(keys)))
	for _, k := range keys {
		rk := k
		if !strings.Contains(rk, ":") {
			rk = "minecraft:" + rk
		}
		w.String(rk)
		w.String(entries[k])
	}
	return w.Bytes()
}

// DecodeSetGameRuleSBPacket decodes ServerboundSetGameRulePacket (0x39).
func DecodeSetGameRuleSBPacket(r *protocol.Reader) (map[string]string, error) {
	count, err := r.VarInt()
	if err != nil {
		return nil, err
	}
	if count < 0 || count > 256 {
		return nil, fmt.Errorf("gamerules: invalid entry count %d", count)
	}
	out := make(map[string]string, count)
	for i := int32(0); i < count; i++ {
		key, err := r.String()
		if err != nil {
			return nil, err
		}
		val, err := r.String()
		if err != nil {
			return nil, err
		}
		out[key] = val
	}
	return out, nil
}

// EncodeGameRuleValuesPacket encodes ClientboundGameRuleValuesPacket (0x27):
// VarInt(count), followed by (ResourceKey<GameRule>, String value) entries.
func EncodeGameRuleValuesPacket(values map[string]string) []byte {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	w := protocol.NewWriter(32 * (len(keys) + 1))
	w.VarInt(int32(len(keys)))
	for _, k := range keys {
		rk := k
		if !strings.Contains(rk, ":") {
			rk = "minecraft:" + rk
		}
		w.String(rk)
		w.String(values[k])
	}
	return w.Bytes()
}

// DecodeGameRuleValuesPacket decodes ClientboundGameRuleValuesPacket (0x27).
func DecodeGameRuleValuesPacket(r *protocol.Reader) (map[string]string, error) {
	return DecodeSetGameRuleSBPacket(r)
}

// HandleSetGameRulePacket processes ServerboundSetGameRulePacket (0x39) for
// operators, applying valid rule changes and broadcasting ClientboundGameRuleValuesPacket (0x27).
func (s *Server) HandleSetGameRulePacket(session *PlayerSession, r *protocol.Reader) error {
	entries, err := DecodeSetGameRuleSBPacket(r)
	if err != nil {
		return err
	}
	if s == nil || !s.HasAdminPermission(session) || len(entries) == 0 {
		return nil
	}
	applied := make(map[string]string, len(entries))
	for k, v := range entries {
		canon, formatted, err := s.SetGameRuleFromString(k, v)
		if err == nil {
			applied["minecraft:"+canon] = formatted
		}
	}
	if len(applied) > 0 && s.players != nil {
		s.Broadcast(protocol.PlayGameRuleValues, EncodeGameRuleValuesPacket(applied))
	}
	return nil
}


// HandleSpectateEntityPacket processes ServerboundSpectateEntityPacket (0x3e):
// VarInt(entityID), attaching a Spectator player's camera to targetEntityID.
func (s *Server) HandleSpectateEntityPacket(session *PlayerSession, r *protocol.Reader) error {
	if s == nil || session == nil || r == nil {
		return nil
	}
	targetEntityID, err := r.VarInt()
	if err != nil {
		return err
	}
	s.SetSpectatorCamera(session, targetEntityID)
	return nil
}
