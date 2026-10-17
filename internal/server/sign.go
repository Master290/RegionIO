package server

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Protocol 775 (26.1.2) sign & block-entity packet IDs.
const (
	// PlayBlockEntityDataCB is ClientboundBlockEntityDataPacket (0x06 in protocol 775).
	PlayBlockEntityDataCB int32 = 0x06
	// PlayOpenSignEditorCB is ClientboundOpenSignEditorPacket (0x3c in protocol 775).
	PlayOpenSignEditorCB int32 = 0x3c
	// PlaySignUpdateSB is ServerboundSignUpdatePacket (0x3c in protocol 775).
	PlaySignUpdateSB int32 = 0x3c
)

// BuiltInRegistries.BLOCK_ENTITY_TYPE indices for signs (protocol 775 / 26.1.2).
const (
	BlockEntityTypeSign        int32 = 7 // minecraft:sign
	BlockEntityTypeHangingSign int32 = 8 // minecraft:hanging_sign
)

// Vanilla 26.1.2 sign constants.
const (
	SignTextLineCount   = 4
	MaxSignLineRunes    = 384
	DefaultSignDyeColor = "black"
	MaxSignEditDistSq   = 64.0
)

// IsHangingSignBlock reports whether state is a ceiling or wall hanging sign block.
func IsHangingSignBlock(state uint16) bool {
	name := strings.TrimPrefix(world.BlockName(state), "minecraft:")
	return strings.HasSuffix(name, "_hanging_sign") || strings.HasSuffix(name, "_wall_hanging_sign")
}

// IsSignBlock reports whether state is any standing sign, wall sign, ceiling hanging sign, or wall hanging sign.
func IsSignBlock(state uint16) bool {
	name := strings.TrimPrefix(world.BlockName(state), "minecraft:")
	return strings.HasSuffix(name, "_sign")
}

// SignBlockEntityTypeForState returns ("minecraft:hanging_sign", 8, true) for hanging signs,
// ("minecraft:sign", 7, true) for regular signs, or ("", 0, false) for non-sign blocks.
func SignBlockEntityTypeForState(state uint16) (string, int32, bool) {
	if IsHangingSignBlock(state) {
		return "minecraft:hanging_sign", BlockEntityTypeHangingSign, true
	}
	if IsSignBlock(state) {
		return "minecraft:sign", BlockEntityTypeSign, true
	}
	return "", 0, false
}

// DyeColorFromItemName returns the canonical vanilla dye color ("white".."black")
// if itemName is one of the 16 dye items ("minecraft:<color>_dye").
func DyeColorFromItemName(itemName string) (string, bool) {
	short := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(itemName)), "minecraft:")
	if !strings.HasSuffix(short, "_dye") {
		return "", false
	}
	color := strings.TrimSuffix(short, "_dye")
	if IsValidSignDyeColor(color) {
		return color, true
	}
	return "", false
}

// IsValidSignDyeColor reports whether color is one of the 16 vanilla DyeColor names.
func IsValidSignDyeColor(color string) bool {
	switch strings.ToLower(strings.TrimSpace(color)) {
	case "white", "orange", "magenta", "light_blue", "yellow", "lime", "pink", "gray",
		"light_gray", "cyan", "purple", "blue", "brown", "green", "red", "black":
		return true
	default:
		return false
	}
}

// SanitizeSignLine validates and filters a single line of sign text (up to 384 runes,
// stripping control characters and section signs).
func SanitizeSignLine(raw string) (string, error) {
	if utf8.RuneCountInString(raw) > MaxSignLineRunes {
		return "", fmt.Errorf("sign line exceeds maximum length of %d characters", MaxSignLineRunes)
	}
	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range raw {
		if r == 0 || r == '§' || unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String(), nil
}

// --- SignText & SignBlockEntity ---

// SignText holds the 4 lines, dye color, and glowing state for one side (front or back)
// of a sign block entity, matching vanilla net.minecraft.world.level.block.entity.SignText.
type SignText struct {
	Messages         [SignTextLineCount]ChatComponent
	FilteredMessages [SignTextLineCount]ChatComponent
	HasFiltered      bool
	Color            string
	HasGlowingText   bool
}

// NewDefaultSignText returns a blank SignText with Color="black" and HasGlowingText=false.
func NewDefaultSignText() SignText {
	return SignText{
		Messages: [SignTextLineCount]ChatComponent{
			TextComponent(""),
			TextComponent(""),
			TextComponent(""),
			TextComponent(""),
		},
		Color:          DefaultSignDyeColor,
		HasGlowingText: false,
	}
}

// Clone returns a deep copy of st.
func (st SignText) Clone() SignText {
	cp := st
	if cp.Color == "" {
		cp.Color = DefaultSignDyeColor
	}
	return cp
}

// PlainLines returns the 4 plain-text strings on this sign face.
func (st SignText) PlainLines() [SignTextLineCount]string {
	var out [SignTextLineCount]string
	for i := 0; i < SignTextLineCount; i++ {
		out[i] = st.Messages[i].PlainText()
	}
	return out
}

// HasAnyMessageText reports whether at least one of the 4 lines has non-empty plain text.
func (st SignText) HasAnyMessageText() bool {
	for i := 0; i < SignTextLineCount; i++ {
		if strings.TrimSpace(st.Messages[i].PlainText()) != "" {
			return true
		}
	}
	return false
}

// ToNBT serializes SignText into the vanilla front_text / back_text NBT compound:
//   - messages: List of 4 String tags (JSON chat components)
//   - filtered_messages: optional List of 4 String tags
//   - color: String ("black", "red", ...)
//   - has_glowing_text: Byte (0 or 1)
func (st SignText) ToNBT() *nbt.Compound {
	comp := nbt.NewCompound()
	msgElems := make([]nbt.Tag, SignTextLineCount)
	for i := 0; i < SignTextLineCount; i++ {
		msgElems[i] = nbt.String(st.Messages[i].ToJSON())
	}
	comp.Set("messages", nbt.List{ElemID: nbt.TagString, Elems: msgElems})

	if st.HasFiltered {
		filtElems := make([]nbt.Tag, SignTextLineCount)
		for i := 0; i < SignTextLineCount; i++ {
			filtElems[i] = nbt.String(st.FilteredMessages[i].ToJSON())
		}
		comp.Set("filtered_messages", nbt.List{ElemID: nbt.TagString, Elems: filtElems})
	}

	col := st.Color
	if !IsValidSignDyeColor(col) {
		col = DefaultSignDyeColor
	}
	comp.Set("color", nbt.String(col))
	comp.Set("has_glowing_text", boolToNBTByte(st.HasGlowingText))
	return comp
}

// SignTextFromNBT decodes a SignText from a vanilla front_text / back_text NBT compound.
func SignTextFromNBT(comp *nbt.Compound) SignText {
	out := NewDefaultSignText()
	if comp == nil {
		return out
	}
	if col, ok := nbtStringTag(comp, "color"); ok && IsValidSignDyeColor(col) {
		out.Color = strings.ToLower(strings.TrimSpace(col))
	}
	if glow, ok := nbtByteTag(comp, "has_glowing_text"); ok {
		out.HasGlowingText = glow != 0
	}
	if msgList, ok := nbtListTag(comp, "messages"); ok {
		for i := 0; i < len(msgList.Elems) && i < SignTextLineCount; i++ {
			switch v := msgList.Elems[i].(type) {
			case nbt.String:
				out.Messages[i] = ParseChatComponentOrText(string(v))
			case *nbt.Compound:
				out.Messages[i] = ChatComponentFromNBT(v)
			}
		}
	}
	if filtList, ok := nbtListTag(comp, "filtered_messages"); ok && len(filtList.Elems) > 0 {
		out.HasFiltered = true
		for i := 0; i < len(filtList.Elems) && i < SignTextLineCount; i++ {
			switch v := filtList.Elems[i].(type) {
			case nbt.String:
				out.FilteredMessages[i] = ParseChatComponentOrText(string(v))
			case *nbt.Compound:
				out.FilteredMessages[i] = ChatComponentFromNBT(v)
			}
		}
	}
	return out
}

// SignBlockEntity represents a minecraft:sign or minecraft:hanging_sign block entity.
type SignBlockEntity struct {
	ID         string // "minecraft:sign" or "minecraft:hanging_sign"
	Dimension  string
	X          int
	Y          int
	Z          int
	FrontText  SignText
	BackText   SignText
	IsWaxed    bool
	EditorUUID [16]byte
	HasEditor  bool
}

// NewSignBlockEntity creates a new SignBlockEntity at (dimension, x, y, z).
func NewSignBlockEntity(id, dimension string, x, y, z int) *SignBlockEntity {
	if id != "minecraft:hanging_sign" {
		id = "minecraft:sign"
	}
	return &SignBlockEntity{
		ID:        id,
		Dimension: world.NormalizeDimension(dimension),
		X:         x,
		Y:         y,
		Z:         z,
		FrontText: NewDefaultSignText(),
		BackText:  NewDefaultSignText(),
	}
}

// Clone returns a deep copy of s.
func (s *SignBlockEntity) Clone() *SignBlockEntity {
	if s == nil {
		return nil
	}
	return &SignBlockEntity{
		ID:         s.ID,
		Dimension:  s.Dimension,
		X:          s.X,
		Y:          s.Y,
		Z:          s.Z,
		FrontText:  s.FrontText.Clone(),
		BackText:   s.BackText.Clone(),
		IsWaxed:    s.IsWaxed,
		EditorUUID: s.EditorUUID,
		HasEditor:  s.HasEditor,
	}
}

// BlockEntityTypeIndex returns BlockEntityTypeHangingSign (8) or BlockEntityTypeSign (7).
func (s *SignBlockEntity) BlockEntityTypeIndex() int32 {
	if s != nil && s.ID == "minecraft:hanging_sign" {
		return BlockEntityTypeHangingSign
	}
	return BlockEntityTypeSign
}

// GetText returns FrontText when isFront is true, or BackText otherwise.
func (s *SignBlockEntity) GetText(isFront bool) SignText {
	if s == nil {
		return NewDefaultSignText()
	}
	if isFront {
		return s.FrontText
	}
	return s.BackText
}

// SetText updates FrontText when isFront is true, or BackText otherwise.
func (s *SignBlockEntity) SetText(text SignText, isFront bool) {
	if s == nil {
		return
	}
	if isFront {
		s.FrontText = text.Clone()
	} else {
		s.BackText = text.Clone()
	}
}

// ToUpdateTagNBT returns the clientbound BlockEntityData NBT payload
// (is_waxed, front_text, back_text).
func (s *SignBlockEntity) ToUpdateTagNBT() *nbt.Compound {
	comp := nbt.NewCompound()
	if s == nil {
		return comp
	}
	comp.Set("is_waxed", boolToNBTByte(s.IsWaxed))
	comp.Set("front_text", s.FrontText.ToNBT())
	comp.Set("back_text", s.BackText.ToNBT())
	return comp
}

// ToNBT returns the full chunk block_entities NBT compound (id, x, y, z, dimension, is_waxed, front_text, back_text).
func (s *SignBlockEntity) ToNBT() *nbt.Compound {
	comp := s.ToUpdateTagNBT()
	if s == nil {
		return comp
	}
	id := s.ID
	if id != "minecraft:hanging_sign" {
		id = "minecraft:sign"
	}
	comp.Set("id", nbt.String(id))
	comp.Set("x", nbt.Int(int32(s.X)))
	comp.Set("y", nbt.Int(int32(s.Y)))
	comp.Set("z", nbt.Int(int32(s.Z)))
	if s.Dimension != "" {
		comp.Set("dimension", nbt.String(world.NormalizeDimension(s.Dimension)))
	}
	return comp
}

// SignBlockEntityFromNBT decodes a SignBlockEntity from a chunk block_entities or update-tag NBT compound.
func SignBlockEntityFromNBT(comp *nbt.Compound) (*SignBlockEntity, error) {
	if comp == nil {
		return nil, errors.New("server: nil sign NBT")
	}
	id, _ := nbtStringTag(comp, "id")
	if id != "minecraft:hanging_sign" {
		id = "minecraft:sign"
	}
	x, _ := nbtIntTag(comp, "x")
	y, _ := nbtIntTag(comp, "y")
	z, _ := nbtIntTag(comp, "z")
	dim, _ := nbtStringTag(comp, "dimension")
	if dim == "" {
		dim = world.DimensionOverworld
	}
	waxed, _ := nbtByteTag(comp, "is_waxed")
	frontComp, _ := nbtCompoundTag(comp, "front_text")
	backComp, _ := nbtCompoundTag(comp, "back_text")

	return &SignBlockEntity{
		ID:        id,
		Dimension: world.NormalizeDimension(dim),
		X:         int(x),
		Y:         int(y),
		Z:         int(z),
		FrontText: SignTextFromNBT(frontComp),
		BackText:  SignTextFromNBT(backComp),
		IsWaxed:   waxed != 0,
	}, nil
}

// --- Sign Packets (OpenSignEditor 0x3c, SignUpdate 0x3c, BlockEntityData 0x06) ---

// EncodeOpenSignEditorPacket encodes ClientboundOpenSignEditorPacket (0x3c, protocol 775):
//   - Position(x, y, z)
//   - Bool(isFrontText)
func EncodeOpenSignEditorPacket(x, y, z int, isFrontText bool) []byte {
	w := protocol.NewWriter(12)
	w.Position(x, y, z)
	w.Bool(isFrontText)
	return w.Bytes()
}

// DecodeOpenSignEditorPacket decodes ClientboundOpenSignEditorPacket (0x3c, protocol 775).
func DecodeOpenSignEditorPacket(r *protocol.Reader) (x, y, z int, isFrontText bool, err error) {
	x, y, z, err = r.Position()
	if err != nil {
		return 0, 0, 0, false, err
	}
	isFrontText, err = r.Bool()
	if err != nil {
		return 0, 0, 0, false, err
	}
	return x, y, z, isFrontText, nil
}

// EncodeSignUpdatePacket encodes ServerboundSignUpdatePacket (0x3c, protocol 775):
//   - Position(x, y, z)
//   - Bool(isFrontText)
//   - 4 x String(line, max=384)
func EncodeSignUpdatePacket(x, y, z int, isFrontText bool, lines [SignTextLineCount]string) []byte {
	w := protocol.NewWriter(64)
	w.Position(x, y, z)
	w.Bool(isFrontText)
	for i := 0; i < SignTextLineCount; i++ {
		w.String(lines[i])
	}
	return w.Bytes()
}

// DecodeSignUpdatePacket decodes ServerboundSignUpdatePacket (0x3c, protocol 775).
func DecodeSignUpdatePacket(r *protocol.Reader) (x, y, z int, isFrontText bool, lines [SignTextLineCount]string, err error) {
	x, y, z, err = r.Position()
	if err != nil {
		return 0, 0, 0, false, lines, err
	}
	isFrontText, err = r.Bool()
	if err != nil {
		return 0, 0, 0, false, lines, err
	}
	for i := 0; i < SignTextLineCount; i++ {
		line, err := r.String()
		if err != nil {
			return 0, 0, 0, false, lines, err
		}
		if utf8.RuneCountInString(line) > MaxSignLineRunes {
			return 0, 0, 0, false, lines, fmt.Errorf("server: sign line %d length %d exceeds %d", i, utf8.RuneCountInString(line), MaxSignLineRunes)
		}
		lines[i] = line
	}
	return x, y, z, isFrontText, lines, nil
}

// EncodeBlockEntityDataPacket encodes ClientboundBlockEntityDataPacket (0x06, protocol 775):
//   - Position(x, y, z)
//   - VarInt(blockEntityType)
//   - AnonymousNBT(data)
func EncodeBlockEntityDataPacket(x, y, z int, blockEntityType int32, data *nbt.Compound) []byte {
	if data == nil {
		data = nbt.NewCompound()
	}
	rawNBT := nbt.Marshal(data)
	w := protocol.NewWriter(16 + len(rawNBT))
	w.Position(x, y, z)
	w.VarInt(blockEntityType)
	w.Raw(rawNBT)
	return w.Bytes()
}

// DecodeBlockEntityDataPacket decodes ClientboundBlockEntityDataPacket (0x06, protocol 775).
func DecodeBlockEntityDataPacket(r *protocol.Reader) (x, y, z int, blockEntityType int32, data *nbt.Compound, err error) {
	x, y, z, err = r.Position()
	if err != nil {
		return 0, 0, 0, 0, nil, err
	}
	blockEntityType, err = r.VarInt()
	if err != nil {
		return 0, 0, 0, 0, nil, err
	}
	tag, err := readAnonymousNBTTag(r)
	if err != nil {
		return 0, 0, 0, 0, nil, err
	}
	if comp, ok := tag.(*nbt.Compound); ok {
		data = comp
	} else {
		data = nbt.NewCompound()
	}
	return x, y, z, blockEntityType, data, nil
}

// --- Server Sign Store & Gameplay Logic ---

type serverSignStore struct {
	mu    sync.RWMutex
	signs map[blockPosDimKey]*SignBlockEntity
}

var signStores sync.Map // map[*Server]*serverSignStore

func (s *Server) signStore() *serverSignStore {
	if s == nil {
		return &serverSignStore{signs: make(map[blockPosDimKey]*SignBlockEntity)}
	}
	if v, ok := signStores.Load(s); ok {
		return v.(*serverSignStore)
	}
	st := &serverSignStore{signs: make(map[blockPosDimKey]*SignBlockEntity)}
	actual, _ := signStores.LoadOrStore(s, st)
	return actual.(*serverSignStore)
}

// GetSignAt returns a copy of the SignBlockEntity at (dimension, x, y, z), if present.
func (s *Server) GetSignAt(dimension string, x, y, z int) (*SignBlockEntity, bool) {
	if s == nil {
		return nil, false
	}
	key := blockPosDimKey{dim: world.NormalizeDimension(dimension), x: x, y: y, z: z}
	st := s.signStore()
	st.mu.RLock()
	sign := st.signs[key].Clone()
	st.mu.RUnlock()
	if sign == nil {
		return nil, false
	}
	return sign, true
}

// SetSignAt stores or updates the SignBlockEntity at (sign.Dimension, sign.X, sign.Y, sign.Z)
// and broadcasts ClientboundBlockEntityDataPacket (0x06) to players in the chunk.
func (s *Server) SetSignAt(sign *SignBlockEntity) {
	if s == nil || sign == nil {
		return
	}
	cp := sign.Clone()
	cp.Dimension = world.NormalizeDimension(cp.Dimension)
	key := blockPosDimKey{dim: cp.Dimension, x: cp.X, y: cp.Y, z: cp.Z}
	st := s.signStore()
	st.mu.Lock()
	st.signs[key] = cp
	st.mu.Unlock()

	s.broadcastSignBlockEntity(cp)
}

// RemoveSignAt removes any SignBlockEntity at (dimension, x, y, z) when the sign block is broken.
func (s *Server) RemoveSignAt(dimension string, x, y, z int) bool {
	if s == nil {
		return false
	}
	key := blockPosDimKey{dim: world.NormalizeDimension(dimension), x: x, y: y, z: z}
	st := s.signStore()
	st.mu.Lock()
	_, existed := st.signs[key]
	delete(st.signs, key)
	st.mu.Unlock()
	return existed
}

func (s *Server) broadcastSignBlockEntity(sign *SignBlockEntity) {
	if s == nil || sign == nil {
		return
	}
	pkt := EncodeBlockEntityDataPacket(sign.X, sign.Y, sign.Z, sign.BlockEntityTypeIndex(), sign.ToUpdateTagNBT())
	cx := int32(sign.X >> 4)
	cz := int32(sign.Z >> 4)
	s.BroadcastChunk(cx, cz, PlayBlockEntityDataCB, pkt)
}

// SendChunkSignBlockEntitiesToSession sends ClientboundBlockEntityDataPacket (0x06)
// for all signs in chunk (cx, cz) of dimension to session.
func (s *Server) SendChunkSignBlockEntitiesToSession(session *PlayerSession, dimension string, cx, cz int32) {
	if s == nil || session == nil || session.send == nil {
		return
	}
	normDim := world.NormalizeDimension(dimension)
	st := s.signStore()
	st.mu.RLock()
	var packets [][]byte
	for key, sign := range st.signs {
		if sign != nil && key.dim == normDim && int32(key.x>>4) == cx && int32(key.z>>4) == cz {
			packets = append(packets, EncodeBlockEntityDataPacket(sign.X, sign.Y, sign.Z, sign.BlockEntityTypeIndex(), sign.ToUpdateTagNBT()))
		}
	}
	st.mu.RUnlock()

	for _, pkt := range packets {
		_ = session.send(PlayBlockEntityDataCB, pkt)
	}
}

// OnSignPlaced initializes a SignBlockEntity when session places a sign or hanging sign
// at (x, y, z), broadcasts its initial BlockEntityData, and sends OpenSignEditor (0x3c)
// to session so the sign editor screen opens immediately.
func (s *Server) OnSignPlaced(session *PlayerSession, x, y, z int, isFrontText bool) {
	if s == nil || session == nil || session.IsDead() {
		return
	}
	snap := session.Snapshot()
	dim := world.NormalizeDimension(snap.Dimension)

	beID := "minecraft:sign"
	if cache := s.chunksForDimension(dim); cache != nil {
		if resolvedID, _, ok := SignBlockEntityTypeForState(cache.GetBlock(x, y, z)); ok {
			beID = resolvedID
		}
	}

	sign := NewSignBlockEntity(beID, dim, x, y, z)
	sign.EditorUUID = snap.Profile.UUID
	sign.HasEditor = true

	key := blockPosDimKey{dim: dim, x: x, y: y, z: z}
	st := s.signStore()
	st.mu.Lock()
	st.signs[key] = sign.Clone()
	st.mu.Unlock()

	s.broadcastSignBlockEntity(sign)
	if session.send != nil {
		_ = session.send(PlayOpenSignEditorCB, EncodeOpenSignEditorPacket(x, y, z, isFrontText))
	}
}

// HandleSignUpdatePacket processes ServerboundSignUpdatePacket (0x3c) when a player
// finishes editing the front or back of a sign.
func (s *Server) HandleSignUpdatePacket(session *PlayerSession, r *protocol.Reader) error {
	if s == nil || session == nil || session.IsDead() {
		return nil
	}
	x, y, z, isFrontText, rawLines, err := DecodeSignUpdatePacket(r)
	if err != nil {
		return err
	}

	snap := session.Snapshot()
	dx := snap.X - (float64(x) + 0.5)
	dy := snap.Y - (float64(y) + 0.5)
	dz := snap.Z - (float64(z) + 0.5)
	if dx*dx+dy*dy+dz*dz > MaxSignEditDistSq {
		return nil
	}

	var cleanComps [SignTextLineCount]ChatComponent
	for i := 0; i < SignTextLineCount; i++ {
		cleaned, err := SanitizeSignLine(rawLines[i])
		if err != nil {
			return err
		}
		cleanComps[i] = TextComponent(cleaned)
	}

	dim := world.NormalizeDimension(snap.Dimension)
	key := blockPosDimKey{dim: dim, x: x, y: y, z: z}

	st := s.signStore()
	st.mu.Lock()
	sign := st.signs[key]
	if sign == nil {
		// If a sign block exists in the world cache without a block entity yet, create one.
		if cache := s.chunksForDimension(dim); cache != nil {
			if beID, _, ok := SignBlockEntityTypeForState(cache.GetBlock(x, y, z)); ok {
				sign = NewSignBlockEntity(beID, dim, x, y, z)
				st.signs[key] = sign
			}
		}
	}
	if sign == nil || sign.IsWaxed {
		st.mu.Unlock()
		return nil
	}
	if sign.HasEditor && sign.EditorUUID != snap.Profile.UUID {
		st.mu.Unlock()
		return nil
	}

	side := sign.GetText(isFrontText)
	side.Messages = cleanComps
	side.HasFiltered = false
	sign.SetText(side, isFrontText)
	sign.HasEditor = false
	sign.EditorUUID = [16]byte{}
	signCopy := sign.Clone()
	st.mu.Unlock()

	s.broadcastSignBlockEntity(signCopy)
	return nil
}

// InteractWithSign handles right-clicking (use_item_on) an existing sign at (x, y, z):
//   - If waxed: plays waxed_interact_fail sound and rejects edits/dyes.
//   - If holding minecraft:honeycomb: waxes the sign (is_waxed = true).
//   - If holding minecraft:glow_ink_sac (and side has text and is not glowing): enables glowing text.
//   - If holding minecraft:ink_sac (and side is glowing): disables glowing text.
//   - If holding a dye (*_dye) (and side has text and a different color): changes the side's color.
//   - Otherwise: opens the sign editor for that side (front or back) via ClientboundOpenSignEditorPacket (0x3c).
func (s *Server) InteractWithSign(session *PlayerSession, hand int32, x, y, z int, isFrontText bool) bool {
	if s == nil || session == nil || session.IsDead() {
		return false
	}
	snap := session.Snapshot()
	dim := world.NormalizeDimension(snap.Dimension)
	key := blockPosDimKey{dim: dim, x: x, y: y, z: z}

	st := s.signStore()
	st.mu.Lock()
	sign := st.signs[key]
	if sign == nil {
		if cache := s.chunksForDimension(dim); cache != nil {
			if beID, _, ok := SignBlockEntityTypeForState(cache.GetBlock(x, y, z)); ok {
				sign = NewSignBlockEntity(beID, dim, x, y, z)
				st.signs[key] = sign
			}
		}
	}
	if sign == nil {
		st.mu.Unlock()
		return false
	}

	cx := int32(x >> 4)
	cz := int32(z >> 4)
	fx, fy, fz := float64(x)+0.5, float64(y)+0.5, float64(z)+0.5

	// Waxed signs cannot be modified or re-edited.
	if sign.IsWaxed {
		st.mu.Unlock()
		s.BroadcastChunk(cx, cz, PlaySoundPacketID,
			EncodeSoundPacket("minecraft:block.sign.waxed_interact_fail", SoundSourceBlocks, fx, fy, fz, 1.0, 1.0, 0))
		return true
	}

	slot, held := session.handSlotAndStack(hand)
	heldID := held.ResolvedID()
	side := sign.GetText(isFrontText)

	// 1. Honeycomb -> wax the sign.
	if heldID == "minecraft:honeycomb" {
		sign.IsWaxed = true
		sign.HasEditor = false
		signCopy := sign.Clone()
		st.mu.Unlock()

		s.consumeOneHandItemInSurvival(session, slot, held, snap.GameMode)
		s.broadcastSignBlockEntity(signCopy)
		s.BroadcastChunk(cx, cz, PlaySoundPacketID,
			EncodeSoundPacket("minecraft:item.honeycomb.wax_on", SoundSourceBlocks, fx, fy, fz, 1.0, 1.0, 0))
		return true
	}

	// 2. Glow Ink Sac -> make text glow.
	if heldID == "minecraft:glow_ink_sac" {
		if !side.HasGlowingText && side.HasAnyMessageText() {
			side.HasGlowingText = true
			sign.SetText(side, isFrontText)
			signCopy := sign.Clone()
			st.mu.Unlock()

			s.consumeOneHandItemInSurvival(session, slot, held, snap.GameMode)
			s.broadcastSignBlockEntity(signCopy)
			s.BroadcastChunk(cx, cz, PlaySoundPacketID,
				EncodeSoundPacket("minecraft:item.glow_ink_sac.use", SoundSourceBlocks, fx, fy, fz, 1.0, 1.0, 0))
			return true
		}
		st.mu.Unlock()
		return true
	}

	// 3. Ink Sac -> remove glowing text.
	if heldID == "minecraft:ink_sac" {
		if side.HasGlowingText {
			side.HasGlowingText = false
			sign.SetText(side, isFrontText)
			signCopy := sign.Clone()
			st.mu.Unlock()

			s.consumeOneHandItemInSurvival(session, slot, held, snap.GameMode)
			s.broadcastSignBlockEntity(signCopy)
			s.BroadcastChunk(cx, cz, PlaySoundPacketID,
				EncodeSoundPacket("minecraft:item.ink_sac.use", SoundSourceBlocks, fx, fy, fz, 1.0, 1.0, 0))
			return true
		}
		st.mu.Unlock()
		return true
	}

	// 4. Dye item (*_dye) -> dye the sign text on the clicked side.
	if dyeColor, isDye := DyeColorFromItemName(heldID); isDye {
		if side.Color != dyeColor && side.HasAnyMessageText() {
			side.Color = dyeColor
			sign.SetText(side, isFrontText)
			signCopy := sign.Clone()
			st.mu.Unlock()

			s.consumeOneHandItemInSurvival(session, slot, held, snap.GameMode)
			s.broadcastSignBlockEntity(signCopy)
			s.BroadcastChunk(cx, cz, PlaySoundPacketID,
				EncodeSoundPacket("minecraft:item.dye.use", SoundSourceBlocks, fx, fy, fz, 1.0, 1.0, 0))
			return true
		}
		st.mu.Unlock()
		return true
	}

	// 5. Right-click with any other item / empty hand -> open sign editor for this side.
	sign.EditorUUID = snap.Profile.UUID
	sign.HasEditor = true
	signCopy := sign.Clone()
	st.mu.Unlock()

	s.broadcastSignBlockEntity(signCopy)
	if session.send != nil {
		_ = session.send(PlayOpenSignEditorCB, EncodeOpenSignEditorPacket(x, y, z, isFrontText))
	}
	return true
}

func (s *Server) consumeOneHandItemInSurvival(session *PlayerSession, slot int, held world.ItemStack, gameMode uint8) {
	if session == nil || held.IsEmpty() || gameMode == GameModeCreative {
		return
	}
	rem := held.Count - 1
	if rem <= 0 {
		session.SetInventorySlot(slot, world.ItemStack{})
	} else {
		session.SetInventorySlot(slot, world.NewItemStack(held.ResolvedItemID(), rem))
	}
	if session.send != nil {
		stateID := session.NextContainerStateID()
		_ = session.send(protocol.PlayContainerSetSlot, encodeContainerSetSlotPacket(stateID, slot, session.InventorySlot(slot)))
	}
}

// IsPlayerFacingSignFront computes whether a player at (playerX, playerZ) is on
// the front side of a sign at block (signX, signZ) whose facing yaw is signYawDeg
// (matching vanilla SignBlock.isFacingFrontText: |deltaAngle| <= 90 degrees).
func IsPlayerFacingSignFront(playerX, playerZ float64, signX, signZ int, signYawDeg float64) bool {
	dx := playerX - (float64(signX) + 0.5)
	dz := playerZ - (float64(signZ) + 0.5)
	angleToPlayer := math.Atan2(dz, dx)*180.0/math.Pi - 90.0
	diff := math.Mod(angleToPlayer-signYawDeg, 360.0)
	if diff > 180.0 {
		diff -= 360.0
	} else if diff < -180.0 {
		diff += 360.0
	}
	return math.Abs(diff) <= 90.0
}

// --- Chunk Block-Entity NBT Helpers & Disk Persistence (<WorldDir>/data/signs_lecterns.dat) ---

// ChunkSignAndLecternBlockEntitiesNBT returns all SignBlockEntity and LecternBlockEntity
// NBT compounds located in chunk (cx, cz) of dimension, suitable for appending into a
// chunk's "block_entities" NBT list.
func (s *Server) ChunkSignAndLecternBlockEntitiesNBT(dimension string, cx, cz int32) []*nbt.Compound {
	if s == nil {
		return nil
	}
	normDim := world.NormalizeDimension(dimension)
	var out []*nbt.Compound

	signSt := s.signStore()
	signSt.mu.RLock()
	for key, sign := range signSt.signs {
		if sign != nil && key.dim == normDim && int32(key.x>>4) == cx && int32(key.z>>4) == cz {
			out = append(out, sign.ToNBT())
		}
	}
	signSt.mu.RUnlock()

	lecSt := s.lecternStore()
	lecSt.mu.RLock()
	for key, lec := range lecSt.lecterns {
		if lec != nil && key.dim == normDim && int32(key.x>>4) == cx && int32(key.z>>4) == cz {
			out = append(out, lec.ToNBT())
		}
	}
	lecSt.mu.RUnlock()

	return out
}

// LoadChunkSignAndLecternBlockEntitiesNBT loads any minecraft:sign, minecraft:hanging_sign,
// and minecraft:lectern entries from a chunk's "block_entities" NBT list into the server stores.
func (s *Server) LoadChunkSignAndLecternBlockEntitiesNBT(dimension string, list nbt.List) {
	if s == nil {
		return
	}
	normDim := world.NormalizeDimension(dimension)
	for _, elem := range list.Elems {
		comp, ok := elem.(*nbt.Compound)
		if !ok || comp == nil {
			continue
		}
		id, _ := nbtStringTag(comp, "id")
		switch id {
		case "minecraft:sign", "minecraft:hanging_sign":
			if sign, err := SignBlockEntityFromNBT(comp); err == nil && sign != nil {
				if _, hasDim := nbtStringTag(comp, "dimension"); !hasDim {
					sign.Dimension = normDim
				}
				key := blockPosDimKey{dim: sign.Dimension, x: sign.X, y: sign.Y, z: sign.Z}
				st := s.signStore()
				st.mu.Lock()
				st.signs[key] = sign
				st.mu.Unlock()
			}
		case "minecraft:lectern":
			if lec, err := LecternBlockEntityFromNBT(comp); err == nil && lec != nil {
				if _, hasDim := nbtStringTag(comp, "dimension"); !hasDim {
					lec.Dimension = normDim
				}
				s.SetLecternAt(lec)
			}
		}
	}
}

// SaveSignsAndLecternsToNBT serializes all signs and lecterns across all dimensions into an NBT compound.
func (s *Server) SaveSignsAndLecternsToNBT() *nbt.Compound {
	root := nbt.NewCompound()
	root.Set("DataVersion", nbt.Int(mapDataVersion26_1_2))
	if s == nil {
		return root
	}

	signSt := s.signStore()
	signSt.mu.RLock()
	signKeys := make([]blockPosDimKey, 0, len(signSt.signs))
	for k := range signSt.signs {
		signKeys = append(signKeys, k)
	}
	sort.Slice(signKeys, func(i, j int) bool {
		if signKeys[i].dim != signKeys[j].dim {
			return signKeys[i].dim < signKeys[j].dim
		}
		if signKeys[i].x != signKeys[j].x {
			return signKeys[i].x < signKeys[j].x
		}
		if signKeys[i].y != signKeys[j].y {
			return signKeys[i].y < signKeys[j].y
		}
		return signKeys[i].z < signKeys[j].z
	})
	var elems []nbt.Tag
	for _, k := range signKeys {
		if sign := signSt.signs[k]; sign != nil {
			elems = append(elems, sign.ToNBT())
		}
	}
	signSt.mu.RUnlock()

	lecSt := s.lecternStore()
	lecSt.mu.RLock()
	lecKeys := make([]blockPosDimKey, 0, len(lecSt.lecterns))
	for k := range lecSt.lecterns {
		lecKeys = append(lecKeys, k)
	}
	sort.Slice(lecKeys, func(i, j int) bool {
		if lecKeys[i].dim != lecKeys[j].dim {
			return lecKeys[i].dim < lecKeys[j].dim
		}
		if lecKeys[i].x != lecKeys[j].x {
			return lecKeys[i].x < lecKeys[j].x
		}
		if lecKeys[i].y != lecKeys[j].y {
			return lecKeys[i].y < lecKeys[j].y
		}
		return lecKeys[i].z < lecKeys[j].z
	})
	for _, k := range lecKeys {
		if lec := lecSt.lecterns[k]; lec != nil {
			elems = append(elems, lec.ToNBT())
		}
	}
	lecSt.mu.RUnlock()

	elemID := byte(nbt.TagCompound)
	if len(elems) == 0 {
		elemID = nbt.TagEnd
	}
	root.Set("block_entities", nbt.List{ElemID: elemID, Elems: elems})
	return root
}

// LoadSignsAndLecternsFromNBT restores signs and lecterns from a saved NBT compound.
func (s *Server) LoadSignsAndLecternsFromNBT(root *nbt.Compound) error {
	if s == nil || root == nil {
		return nil
	}
	if list, ok := nbtListTag(root, "block_entities"); ok {
		s.LoadChunkSignAndLecternBlockEntitiesNBT(world.DimensionOverworld, list)
	}
	return nil
}

// SaveSignsAndLecternsFile writes <WorldDir>/data/signs_lecterns.dat as gzipped NBT.
func (s *Server) SaveSignsAndLecternsFile() error {
	if s == nil || s.cfg.WorldDir == "" {
		return nil
	}
	dir := filepath.Join(s.cfg.WorldDir, "data")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	raw := nbt.MarshalNamed("", s.SaveSignsAndLecternsToNBT())
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(raw); err != nil {
		_ = gz.Close()
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "signs_lecterns.dat"), buf.Bytes(), 0o644)
}

// LoadSignsAndLecternsFile loads <WorldDir>/data/signs_lecterns.dat if it exists.
func (s *Server) LoadSignsAndLecternsFile() error {
	if s == nil || s.cfg.WorldDir == "" {
		return nil
	}
	path := filepath.Join(s.cfg.WorldDir, "data", "signs_lecterns.dat")
	rawFile, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var nbtBytes []byte
	if len(rawFile) >= 2 && rawFile[0] == 0x1f && rawFile[1] == 0x8b {
		gz, err := gzip.NewReader(bytes.NewReader(rawFile))
		if err != nil {
			return err
		}
		nbtBytes, err = io.ReadAll(gz)
		_ = gz.Close()
		if err != nil {
			return err
		}
	} else {
		nbtBytes = rawFile
	}
	_, root, err := nbt.UnmarshalNamed(nbtBytes)
	if err != nil {
		return err
	}
	rootComp, ok := root.(*nbt.Compound)
	if !ok || rootComp == nil {
		return errors.New("server: signs_lecterns.dat root is not Compound")
	}
	return s.LoadSignsAndLecternsFromNBT(rootComp)
}
