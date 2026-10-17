package server

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"strings"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/registry"
)

// Protocol 775 (26.1.2) chat packet IDs.
const (
	// PlayDisguisedChatCB is ClientboundDisguisedChatPacket (0x21 in protocol 775).
	PlayDisguisedChatCB int32 = 0x21
	// PlayPlayerChatCB is ClientboundPlayerChatPacket (0x41 in protocol 775).
	PlayPlayerChatCB int32 = 0x41
	// PlaySystemChatCB is ClientboundSystemChatPacket (0x79 in protocol 775).
	PlaySystemChatCB int32 = protocol.PlaySystemChat
)

// Vanilla 26.1.2 synchronized minecraft:chat_type registry indices (matching
// internal/registry/synced_registries.json).
const (
	ChatTypeChat                   int32 = 0 // minecraft:chat
	ChatTypeEmoteCommand           int32 = 1 // minecraft:emote_command
	ChatTypeMsgCommandIncoming     int32 = 2 // minecraft:msg_command_incoming
	ChatTypeMsgCommandOutgoing     int32 = 3 // minecraft:msg_command_outgoing
	ChatTypeSayCommand             int32 = 4 // minecraft:say_command
	ChatTypeTeamMsgCommandIncoming int32 = 5 // minecraft:team_msg_command_incoming
	ChatTypeTeamMsgCommandOutgoing int32 = 6 // minecraft:team_msg_command_outgoing
)

// Vanilla 26.1.2 ChatFormatting color indices (0..15 for the 16 standard colors,
// 21 for reset).
const (
	ChatColorBlack       int32 = 0
	ChatColorDarkBlue    int32 = 1
	ChatColorDarkGreen   int32 = 2
	ChatColorDarkAqua    int32 = 3
	ChatColorDarkRed     int32 = 4
	ChatColorDarkPurple  int32 = 5
	ChatColorGold        int32 = 6
	ChatColorGray        int32 = 7
	ChatColorDarkGray    int32 = 8
	ChatColorBlue        int32 = 9
	ChatColorGreen       int32 = 10
	ChatColorAqua        int32 = 11
	ChatColorRed         int32 = 12
	ChatColorLightPurple int32 = 13
	ChatColorYellow      int32 = 14
	ChatColorWhite       int32 = 15
	ChatColorReset       int32 = 21
)

var chatColorNamesByID = map[int32]string{
	ChatColorBlack:       "black",
	ChatColorDarkBlue:    "dark_blue",
	ChatColorDarkGreen:   "dark_green",
	ChatColorDarkAqua:    "dark_aqua",
	ChatColorDarkRed:     "dark_red",
	ChatColorDarkPurple:  "dark_purple",
	ChatColorGold:        "gold",
	ChatColorGray:        "gray",
	ChatColorDarkGray:    "dark_gray",
	ChatColorBlue:        "blue",
	ChatColorGreen:       "green",
	ChatColorAqua:        "aqua",
	ChatColorRed:         "red",
	ChatColorLightPurple: "light_purple",
	ChatColorYellow:      "yellow",
	ChatColorWhite:       "white",
	ChatColorReset:       "reset",
}

var chatColorIDsByName = map[string]int32{
	"black":        ChatColorBlack,
	"dark_blue":    ChatColorDarkBlue,
	"dark_green":   ChatColorDarkGreen,
	"dark_aqua":    ChatColorDarkAqua,
	"dark_red":     ChatColorDarkRed,
	"dark_purple":  ChatColorDarkPurple,
	"gold":         ChatColorGold,
	"gray":         ChatColorGray,
	"dark_gray":    ChatColorDarkGray,
	"blue":         ChatColorBlue,
	"green":        ChatColorGreen,
	"aqua":         ChatColorAqua,
	"red":          ChatColorRed,
	"light_purple": ChatColorLightPurple,
	"yellow":       ChatColorYellow,
	"white":        ChatColorWhite,
	"reset":        ChatColorReset,
}

// ChatColorID returns the numeric ChatFormatting ID (0..15 or 21 for reset) for
// a named chat color.
func ChatColorID(name string) (int32, bool) {
	key := strings.ToLower(strings.TrimSpace(name))
	id, ok := chatColorIDsByName[key]
	return id, ok
}

// ChatColorName returns the canonical lowercase name for a ChatFormatting ID.
func ChatColorName(id int32) string {
	if name, ok := chatColorNamesByID[id]; ok {
		return name
	}
	return "reset"
}

// NormalizeChatColor validates a named chat color or hex color (#RRGGBB).
func NormalizeChatColor(color string) (string, bool) {
	trimmed := strings.TrimSpace(color)
	if trimmed == "" {
		return "", false
	}
	lower := strings.ToLower(trimmed)
	if _, ok := chatColorIDsByName[lower]; ok {
		return lower, true
	}
	if len(trimmed) == 7 && trimmed[0] == '#' {
		for i := 1; i < 7; i++ {
			c := trimmed[i]
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return "", false
			}
		}
		return strings.ToUpper(trimmed), true
	}
	return "", false
}

// ChatTypeDecoration describes how a chat_type formats its chat or narration
// parameters into a translatable component.
type ChatTypeDecoration struct {
	TranslationKey string
	Parameters     []string // e.g. ["sender", "content"] or ["target", "sender", "content"]
	Color          string
	Italic         bool
}

// ChatTypeDefinition holds one entry from the vanilla 26.1.2 minecraft:chat_type registry.
type ChatTypeDefinition struct {
	ID            string
	RegistryIndex int32
	Chat          ChatTypeDecoration
	Narration     ChatTypeDecoration
}

var vanillaChatTypes = []ChatTypeDefinition{
	{
		ID:            "minecraft:chat",
		RegistryIndex: ChatTypeChat,
		Chat: ChatTypeDecoration{
			TranslationKey: "chat.type.text",
			Parameters:     []string{"sender", "content"},
		},
		Narration: ChatTypeDecoration{
			TranslationKey: "chat.type.text.narrate",
			Parameters:     []string{"sender", "content"},
		},
	},
	{
		ID:            "minecraft:emote_command",
		RegistryIndex: ChatTypeEmoteCommand,
		Chat: ChatTypeDecoration{
			TranslationKey: "chat.type.emote",
			Parameters:     []string{"sender", "content"},
		},
		Narration: ChatTypeDecoration{
			TranslationKey: "chat.type.emote",
			Parameters:     []string{"sender", "content"},
		},
	},
	{
		ID:            "minecraft:msg_command_incoming",
		RegistryIndex: ChatTypeMsgCommandIncoming,
		Chat: ChatTypeDecoration{
			TranslationKey: "commands.message.display.incoming",
			Parameters:     []string{"sender", "content"},
			Color:          "gray",
			Italic:         true,
		},
		Narration: ChatTypeDecoration{
			TranslationKey: "chat.type.text.narrate",
			Parameters:     []string{"sender", "content"},
		},
	},
	{
		ID:            "minecraft:msg_command_outgoing",
		RegistryIndex: ChatTypeMsgCommandOutgoing,
		Chat: ChatTypeDecoration{
			TranslationKey: "commands.message.display.outgoing",
			Parameters:     []string{"target", "content"},
			Color:          "gray",
			Italic:         true,
		},
		Narration: ChatTypeDecoration{
			TranslationKey: "chat.type.text.narrate",
			Parameters:     []string{"sender", "content"},
		},
	},
	{
		ID:            "minecraft:say_command",
		RegistryIndex: ChatTypeSayCommand,
		Chat: ChatTypeDecoration{
			TranslationKey: "chat.type.announcement",
			Parameters:     []string{"sender", "content"},
		},
		Narration: ChatTypeDecoration{
			TranslationKey: "chat.type.text.narrate",
			Parameters:     []string{"sender", "content"},
		},
	},
	{
		ID:            "minecraft:team_msg_command_incoming",
		RegistryIndex: ChatTypeTeamMsgCommandIncoming,
		Chat: ChatTypeDecoration{
			TranslationKey: "chat.type.team.text",
			Parameters:     []string{"target", "sender", "content"},
		},
		Narration: ChatTypeDecoration{
			TranslationKey: "chat.type.text.narrate",
			Parameters:     []string{"sender", "content"},
		},
	},
	{
		ID:            "minecraft:team_msg_command_outgoing",
		RegistryIndex: ChatTypeTeamMsgCommandOutgoing,
		Chat: ChatTypeDecoration{
			TranslationKey: "chat.type.team.sent",
			Parameters:     []string{"target", "sender", "content"},
		},
		Narration: ChatTypeDecoration{
			TranslationKey: "chat.type.text.narrate",
			Parameters:     []string{"sender", "content"},
		},
	},
}

// AllChatTypes returns a copy of all 7 vanilla 26.1.2 minecraft:chat_type definitions.
func AllChatTypes() []ChatTypeDefinition {
	out := make([]ChatTypeDefinition, len(vanillaChatTypes))
	copy(out, vanillaChatTypes)
	return out
}

// ChatTypeByIndex returns the chat_type definition for a network registry index (0..6).
func ChatTypeByIndex(index int32) (ChatTypeDefinition, bool) {
	if index < 0 || int(index) >= len(vanillaChatTypes) {
		return ChatTypeDefinition{}, false
	}
	return vanillaChatTypes[index], true
}

// ChatTypeByID looks up a chat_type definition by resource location (e.g. "minecraft:chat").
func ChatTypeByID(id string) (ChatTypeDefinition, bool) {
	canon := canonicalResourceName(id)
	if idx := registry.Index("minecraft:chat_type", canon); idx >= 0 && idx < len(vanillaChatTypes) {
		return vanillaChatTypes[idx], true
	}
	for _, def := range vanillaChatTypes {
		if def.ID == canon {
			return def, true
		}
	}
	return ChatTypeDefinition{}, false
}

// Decorate applies a ChatTypeDecoration to the supplied sender, content, and
// optional target components, producing the resulting translatable ChatComponent.
func (d ChatTypeDecoration) Decorate(sender, content ChatComponent, target *ChatComponent) ChatComponent {
	args := make([]ChatComponent, 0, len(d.Parameters))
	for _, p := range d.Parameters {
		switch p {
		case "sender":
			args = append(args, sender)
		case "content":
			args = append(args, content)
		case "target":
			if target != nil {
				args = append(args, *target)
			} else {
				args = append(args, TextComponent(""))
			}
		}
	}
	comp := TranslationComponent(d.TranslationKey, args...)
	if d.Color != "" {
		comp.Color = d.Color
	}
	if d.Italic {
		italic := true
		comp.Italic = &italic
	}
	return comp
}

// ChatScoreRef represents a score component reference {"score":{"name":"...","objective":"..."}}.
type ChatScoreRef struct {
	Name      string `json:"name"`
	Objective string `json:"objective"`
}

// ChatComponent represents a Minecraft rich text component (plain text,
// translation, score, selector, extras, and styling) convertible to both
// network NBT (ComponentSerialization) and JSON (/tellraw, /title, scoreboard.dat).
type ChatComponent struct {
	Text          string          `json:"text,omitempty"`
	Translate     string          `json:"translate,omitempty"`
	With          []ChatComponent `json:"with,omitempty"`
	Score         *ChatScoreRef   `json:"score,omitempty"`
	Selector      string          `json:"selector,omitempty"`
	Extra         []ChatComponent `json:"extra,omitempty"`
	Color         string          `json:"color,omitempty"`
	Bold          *bool           `json:"bold,omitempty"`
	Italic        *bool           `json:"italic,omitempty"`
	Underlined    *bool           `json:"underlined,omitempty"`
	Strikethrough *bool           `json:"strikethrough,omitempty"`
	Obfuscated    *bool           `json:"obfuscated,omitempty"`
	Insertion     string          `json:"insertion,omitempty"`
}

// TextComponent constructs a literal text component.
func TextComponent(text string) ChatComponent {
	return ChatComponent{Text: text}
}

// ColoredTextComponent constructs a literal text component with a named or hex color.
func ColoredTextComponent(text, color string) ChatComponent {
	return ChatComponent{Text: text, Color: color}
}

// TranslationComponent constructs a translatable component with optional arguments.
func TranslationComponent(key string, with ...ChatComponent) ChatComponent {
	return ChatComponent{
		Translate: key,
		With:      with,
	}
}

// IsEmpty reports whether the component has no text, translation, score,
// selector, or extra children.
func (c ChatComponent) IsEmpty() bool {
	return c.Text == "" && c.Translate == "" && c.Score == nil && c.Selector == "" && len(c.Extra) == 0
}

// hasStyleOrChildren reports whether the component needs a full NBT compound
// instead of a bare nbt.String tag.
func (c ChatComponent) hasStyleOrChildren() bool {
	return c.Translate != "" ||
		len(c.With) > 0 ||
		c.Score != nil ||
		c.Selector != "" ||
		len(c.Extra) > 0 ||
		c.Color != "" ||
		c.Bold != nil ||
		c.Italic != nil ||
		c.Underlined != nil ||
		c.Strikethrough != nil ||
		c.Obfuscated != nil ||
		c.Insertion != ""
}

// ToNBT converts the ChatComponent into a network NBT Tag matching 26.1.2
// ComponentSerialization.TRUSTED_STREAM_CODEC. Simple unstyled text components
// serialize as nbt.String; styled or complex components serialize as *nbt.Compound.
func (c ChatComponent) ToNBT() nbt.Tag {
	if !c.hasStyleOrChildren() {
		return nbt.String(c.Text)
	}
	comp := nbt.NewCompound()
	if c.Translate != "" {
		comp.Set("type", nbt.String("translatable"))
		comp.Set("translate", nbt.String(c.Translate))
		if len(c.With) > 0 {
			elems := make([]nbt.Tag, 0, len(c.With))
			for _, w := range c.With {
				// Use Compound for heterogeneous list compatibility.
				elems = append(elems, w.toNBTCompound())
			}
			comp.Set("with", nbt.List{ElemID: nbt.TagCompound, Elems: elems})
		}
		if c.Text != "" {
			comp.Set("fallback", nbt.String(c.Text))
		}
	} else if c.Score != nil {
		comp.Set("type", nbt.String("score"))
		sc := nbt.NewCompound()
		sc.Set("name", nbt.String(c.Score.Name))
		sc.Set("objective", nbt.String(c.Score.Objective))
		comp.Set("score", sc)
	} else if c.Selector != "" {
		comp.Set("type", nbt.String("selector"))
		comp.Set("selector", nbt.String(c.Selector))
	} else {
		comp.Set("type", nbt.String("text"))
		comp.Set("text", nbt.String(c.Text))
	}

	if c.Color != "" {
		comp.Set("color", nbt.String(c.Color))
	}
	if c.Bold != nil {
		comp.Set("bold", boolToNBTByte(*c.Bold))
	}
	if c.Italic != nil {
		comp.Set("italic", boolToNBTByte(*c.Italic))
	}
	if c.Underlined != nil {
		comp.Set("underlined", boolToNBTByte(*c.Underlined))
	}
	if c.Strikethrough != nil {
		comp.Set("strikethrough", boolToNBTByte(*c.Strikethrough))
	}
	if c.Obfuscated != nil {
		comp.Set("obfuscated", boolToNBTByte(*c.Obfuscated))
	}
	if c.Insertion != "" {
		comp.Set("insertion", nbt.String(c.Insertion))
	}
	if len(c.Extra) > 0 {
		elems := make([]nbt.Tag, 0, len(c.Extra))
		for _, ex := range c.Extra {
			elems = append(elems, ex.toNBTCompound())
		}
		comp.Set("extra", nbt.List{ElemID: nbt.TagCompound, Elems: elems})
	}
	return comp
}

func (c ChatComponent) toNBTCompound() *nbt.Compound {
	tag := c.ToNBT()
	if comp, ok := tag.(*nbt.Compound); ok {
		return comp
	}
	comp := nbt.NewCompound()
	comp.Set("type", nbt.String("text"))
	comp.Set("text", nbt.String(c.Text))
	return comp
}

// MarshalNBT encodes the ChatComponent as an unnamed root NBT tag for the wire.
func (c ChatComponent) MarshalNBT() []byte {
	return nbt.Marshal(c.ToNBT())
}

// ChatComponentFromNBT decodes a ChatComponent from a network NBT Tag.
func ChatComponentFromNBT(tag nbt.Tag) ChatComponent {
	switch v := tag.(type) {
	case nbt.String:
		return TextComponent(string(v))
	case *nbt.Compound:
		if v == nil {
			return TextComponent("")
		}
		var out ChatComponent
		if s, ok := getNBTString(v, "text"); ok {
			out.Text = s
		}
		if s, ok := getNBTString(v, "translate"); ok {
			out.Translate = s
		}
		if s, ok := getNBTString(v, "selector"); ok {
			out.Selector = s
		}
		if scTag, ok := v.Get("score"); ok {
			if scComp, ok := scTag.(*nbt.Compound); ok && scComp != nil {
				name, _ := getNBTString(scComp, "name")
				obj, _ := getNBTString(scComp, "objective")
				out.Score = &ChatScoreRef{Name: name, Objective: obj}
			}
		}
		if withTag, ok := v.Get("with"); ok {
			if list, ok := withTag.(nbt.List); ok {
				for _, elem := range list.Elems {
					out.With = append(out.With, ChatComponentFromNBT(elem))
				}
			}
		}
		if extraTag, ok := v.Get("extra"); ok {
			if list, ok := extraTag.(nbt.List); ok {
				for _, elem := range list.Elems {
					out.Extra = append(out.Extra, ChatComponentFromNBT(elem))
				}
			}
		}
		if col, ok := getNBTString(v, "color"); ok {
			out.Color = col
		}
		if b, ok := getNBTBool(v, "bold"); ok {
			out.Bold = &b
		}
		if b, ok := getNBTBool(v, "italic"); ok {
			out.Italic = &b
		}
		if b, ok := getNBTBool(v, "underlined"); ok {
			out.Underlined = &b
		}
		if b, ok := getNBTBool(v, "strikethrough"); ok {
			out.Strikethrough = &b
		}
		if b, ok := getNBTBool(v, "obfuscated"); ok {
			out.Obfuscated = &b
		}
		if ins, ok := getNBTString(v, "insertion"); ok {
			out.Insertion = ins
		}
		return out
	default:
		return TextComponent("")
	}
}

func getNBTString(c *nbt.Compound, key string) (string, bool) {
	return nbtStringTag(c, key)
}

func getNBTInt(c *nbt.Compound, key string) (int32, bool) {
	return nbtIntTag(c, key)
}

func getNBTByte(c *nbt.Compound, key string) (int8, bool) {
	return nbtByteTag(c, key)
}

func getNBTCompound(c *nbt.Compound, key string) (*nbt.Compound, bool) {
	return nbtCompoundTag(c, key)
}

func getNBTBool(c *nbt.Compound, key string) (bool, bool) {
	if c == nil {
		return false, false
	}
	t, ok := c.Get(key)
	if !ok {
		return false, false
	}
	if b, ok := t.(nbt.Byte); ok {
		return b != 0, true
	}
	return false, false
}

// readAnonymousNBTTag reads one network-format (unnamed root) NBT tag from r,
// advancing r by the exact byte length of the tag.
func readAnonymousNBTTag(r *protocol.Reader) (nbt.Tag, error) {
	id, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	if id == nbt.TagEnd {
		return nil, nil
	}
	var buf bytes.Buffer
	buf.WriteByte(id)
	if err := copyNBTPayload(r, &buf, id, 0); err != nil {
		return nil, err
	}
	return nbt.Unmarshal(buf.Bytes())
}

func copyNBTBytes(r *protocol.Reader, buf *bytes.Buffer, n int) error {
	if n < 0 || r.Remaining() < n {
		return protocol.ErrShortBuffer
	}
	for i := 0; i < n; i++ {
		b, err := r.ReadByte()
		if err != nil {
			return err
		}
		buf.WriteByte(b)
	}
	return nil
}

func copyNBTPayload(r *protocol.Reader, buf *bytes.Buffer, id byte, depth int) error {
	if depth > 512 {
		return errors.New("nbt: nesting exceeds limit")
	}
	switch id {
	case nbt.TagByte:
		return copyNBTBytes(r, buf, 1)
	case nbt.TagShort:
		return copyNBTBytes(r, buf, 2)
	case nbt.TagInt, nbt.TagFloat:
		return copyNBTBytes(r, buf, 4)
	case nbt.TagLong, nbt.TagDouble:
		return copyNBTBytes(r, buf, 8)
	case nbt.TagByteArray:
		var hdr [4]byte
		for i := 0; i < 4; i++ {
			b, err := r.ReadByte()
			if err != nil {
				return err
			}
			hdr[i] = b
		}
		buf.Write(hdr[:])
		count := int(int32(binary.BigEndian.Uint32(hdr[:])))
		return copyNBTBytes(r, buf, count)
	case nbt.TagString:
		var hdr [2]byte
		for i := 0; i < 2; i++ {
			b, err := r.ReadByte()
			if err != nil {
				return err
			}
			hdr[i] = b
		}
		buf.Write(hdr[:])
		strLen := int(binary.BigEndian.Uint16(hdr[:]))
		return copyNBTBytes(r, buf, strLen)
	case nbt.TagIntArray:
		var hdr [4]byte
		for i := 0; i < 4; i++ {
			b, err := r.ReadByte()
			if err != nil {
				return err
			}
			hdr[i] = b
		}
		buf.Write(hdr[:])
		count := int(int32(binary.BigEndian.Uint32(hdr[:])))
		if count < 0 {
			return protocol.ErrShortBuffer
		}
		return copyNBTBytes(r, buf, count*4)
	case nbt.TagLongArray:
		var hdr [4]byte
		for i := 0; i < 4; i++ {
			b, err := r.ReadByte()
			if err != nil {
				return err
			}
			hdr[i] = b
		}
		buf.Write(hdr[:])
		count := int(int32(binary.BigEndian.Uint32(hdr[:])))
		if count < 0 {
			return protocol.ErrShortBuffer
		}
		return copyNBTBytes(r, buf, count*8)
	case nbt.TagList:
		elemID, err := r.ReadByte()
		if err != nil {
			return err
		}
		buf.WriteByte(elemID)
		var hdr [4]byte
		for i := 0; i < 4; i++ {
			b, err := r.ReadByte()
			if err != nil {
				return err
			}
			hdr[i] = b
		}
		buf.Write(hdr[:])
		count := int(int32(binary.BigEndian.Uint32(hdr[:])))
		if count < 0 {
			return protocol.ErrShortBuffer
		}
		for i := 0; i < count; i++ {
			if err := copyNBTPayload(r, buf, elemID, depth+1); err != nil {
				return err
			}
		}
		return nil
	case nbt.TagCompound:
		for {
			fieldID, err := r.ReadByte()
			if err != nil {
				return err
			}
			buf.WriteByte(fieldID)
			if fieldID == nbt.TagEnd {
				return nil
			}
			var hdr [2]byte
			for i := 0; i < 2; i++ {
				b, err := r.ReadByte()
				if err != nil {
					return err
				}
				hdr[i] = b
			}
			buf.Write(hdr[:])
			nameLen := int(binary.BigEndian.Uint16(hdr[:]))
			if err := copyNBTBytes(r, buf, nameLen); err != nil {
				return err
			}
			if err := copyNBTPayload(r, buf, fieldID, depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("nbt: unknown tag id 0x%02x", id)
	}
}

// ReadChatComponentNBT reads an unnamed root NBT tag from r and converts it to
// a ChatComponent.
func ReadChatComponentNBT(r *protocol.Reader) (ChatComponent, error) {
	tag, err := readAnonymousNBTTag(r)
	if err != nil {
		return ChatComponent{}, err
	}
	return ChatComponentFromNBT(tag), nil
}

// ParseChatComponentJSON parses a Minecraft JSON chat component (string, object,
// or array of components) as used by /tellraw, /title, /team, and scoreboard.dat.
func ParseChatComponentJSON(raw string) (ChatComponent, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return TextComponent(""), nil
	}
	var parsed any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return ChatComponent{}, fmt.Errorf("invalid chat component JSON: %w", err)
	}
	return chatComponentFromJSONValue(parsed)
}

// ParseChatComponentOrText parses raw as a JSON chat component if valid JSON,
// or falls back to a literal TextComponent(raw) for convenience in commands.
func ParseChatComponentOrText(raw string) ChatComponent {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return TextComponent("")
	}
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, "\"") {
		if comp, err := ParseChatComponentJSON(trimmed); err == nil {
			return comp
		}
	}
	return TextComponent(trimmed)
}

func chatComponentFromJSONValue(val any) (ChatComponent, error) {
	switch v := val.(type) {
	case string:
		return TextComponent(v), nil
	case float64:
		if v == math.Trunc(v) {
			return TextComponent(fmt.Sprintf("%.0f", v)), nil
		}
		return TextComponent(fmt.Sprintf("%g", v)), nil
	case bool:
		if v {
			return TextComponent("true"), nil
		}
		return TextComponent("false"), nil
	case []any:
		if len(v) == 0 {
			return TextComponent(""), nil
		}
		first, err := chatComponentFromJSONValue(v[0])
		if err != nil {
			return ChatComponent{}, err
		}
		for i := 1; i < len(v); i++ {
			child, err := chatComponentFromJSONValue(v[i])
			if err != nil {
				return ChatComponent{}, err
			}
			first.Extra = append(first.Extra, child)
		}
		return first, nil
	case map[string]any:
		var out ChatComponent
		if t, ok := v["text"].(string); ok {
			out.Text = t
		}
		if tr, ok := v["translate"].(string); ok {
			out.Translate = tr
		}
		if sel, ok := v["selector"].(string); ok {
			out.Selector = sel
		}
		if scMap, ok := v["score"].(map[string]any); ok {
			name, _ := scMap["name"].(string)
			obj, _ := scMap["objective"].(string)
			out.Score = &ChatScoreRef{Name: name, Objective: obj}
		}
		if withRaw, ok := v["with"].([]any); ok {
			for _, item := range withRaw {
				wc, err := chatComponentFromJSONValue(item)
				if err != nil {
					return ChatComponent{}, err
				}
				out.With = append(out.With, wc)
			}
		}
		if extraRaw, ok := v["extra"].([]any); ok {
			for _, item := range extraRaw {
				ec, err := chatComponentFromJSONValue(item)
				if err != nil {
					return ChatComponent{}, err
				}
				out.Extra = append(out.Extra, ec)
			}
		}
		if col, ok := v["color"].(string); ok {
			if norm, valid := NormalizeChatColor(col); valid {
				out.Color = norm
			} else {
				out.Color = col
			}
		}
		if b, ok := v["bold"].(bool); ok {
			out.Bold = &b
		}
		if b, ok := v["italic"].(bool); ok {
			out.Italic = &b
		}
		if b, ok := v["underlined"].(bool); ok {
			out.Underlined = &b
		}
		if b, ok := v["strikethrough"].(bool); ok {
			out.Strikethrough = &b
		}
		if b, ok := v["obfuscated"].(bool); ok {
			out.Obfuscated = &b
		}
		if ins, ok := v["insertion"].(string); ok {
			out.Insertion = ins
		}
		return out, nil
	default:
		return ChatComponent{}, errors.New("unsupported JSON chat component type")
	}
}

// ToJSON serializes the ChatComponent to its canonical JSON representation.
func (c ChatComponent) ToJSON() string {
	if !c.hasStyleOrChildren() {
		raw, _ := json.Marshal(c.Text)
		return string(raw)
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return `{"text":""}`
	}
	return string(raw)
}

// PlainText renders the ChatComponent to a human-readable plain-text string,
// expanding standard vanilla translation keys used for chat, whispers, emotes,
// announcements, join/leave, and death messages.
func (c ChatComponent) PlainText() string {
	var b strings.Builder
	c.writePlainText(&b)
	return b.String()
}

var vanillaTranslationTemplates = map[string]string{
	"chat.type.text":                    "<%s> %s",
	"chat.type.emote":                   "* %s %s",
	"chat.type.announcement":            "[%s] %s",
	"commands.message.display.incoming": "%s whispers to you: %s",
	"commands.message.display.outgoing": "You whisper to %s: %s",
	"chat.type.team.text":               "%s <%s> %s",
	"chat.type.team.sent":               "-> %s <%s> %s",
	"multiplayer.player.joined":         "%s joined the game",
	"multiplayer.player.left":           "%s left the game",
	"death.attack.mob":                  "%s was slain by %s",
	"death.attack.player":               "%s was slain by %s",
	"death.attack.arrow":                "%s was shot by %s",
	"death.attack.explosion.player":     "%s was blown up by %s",
	"death.attack.explosion":            "%s blew up",
	"death.fell.accident.generic":       "%s fell from a high place",
	"death.attack.fall":                 "%s hit the ground too hard",
	"death.attack.lava":                 "%s tried to swim in lava",
	"death.attack.inFire":               "%s went up in flames",
	"death.attack.onFire":               "%s burned to death",
	"death.attack.drown":                "%s drowned",
	"death.attack.starve":               "%s starved to death",
	"death.attack.cactus":               "%s was pricked to death",
	"death.attack.magic":                "%s was killed by magic",
	"death.attack.wither":               "%s withered away",
	"death.attack.inWall":               "%s suffocated in a wall",
	"death.attack.outOfWorld":           "%s fell out of the world",
	"death.attack.generic":              "%s died",
}

func (c ChatComponent) writePlainText(b *strings.Builder) {
	if c.Translate != "" {
		if tmpl, ok := vanillaTranslationTemplates[c.Translate]; ok {
			args := make([]any, len(c.With))
			for i, w := range c.With {
				args[i] = w.PlainText()
			}
			b.WriteString(fmt.Sprintf(tmpl, args...))
		} else {
			b.WriteString(c.Translate)
			if len(c.With) > 0 {
				b.WriteByte('(')
				for i, w := range c.With {
					if i > 0 {
						b.WriteString(", ")
					}
					w.writePlainText(b)
				}
				b.WriteByte(')')
			}
		}
	} else {
		b.WriteString(c.Text)
	}
	for _, ex := range c.Extra {
		ex.writePlainText(b)
	}
}

// EncodeSystemChatPacket encodes ClientboundSystemChatPacket (0x79 in protocol 775).
// Wire layout:
//   - Content (NBT Tag)
//   - Overlay (Bool: true = actionbar, false = chat box)
func EncodeSystemChatPacket(content ChatComponent, overlay bool) []byte {
	nbtBytes := content.MarshalNBT()
	w := protocol.NewWriter(len(nbtBytes) + 1)
	w.Raw(nbtBytes)
	w.Bool(overlay)
	return w.Bytes()
}

// DecodeSystemChatPacket decodes ClientboundSystemChatPacket (0x79).
func DecodeSystemChatPacket(r *protocol.Reader) (content ChatComponent, overlay bool, err error) {
	content, err = ReadChatComponentNBT(r)
	if err != nil {
		return ChatComponent{}, false, err
	}
	overlay, err = r.Bool()
	return content, overlay, err
}

// EncodeDisguisedChatPacket encodes ClientboundDisguisedChatPacket (0x21 in protocol 775).
// Wire layout:
//   - Message (NBT Tag)
//   - ChatType.Bound:
//     - ChatType Holder (VarInt: registryIndex + 1 for registry reference)
//     - Name (NBT Tag)
//     - HasTargetName (Bool)
//     - [TargetName (NBT Tag) if HasTargetName]
func EncodeDisguisedChatPacket(message ChatComponent, chatTypeIndex int32, senderName ChatComponent, targetName *ChatComponent) []byte {
	w := protocol.NewWriter(64)
	w.Raw(message.MarshalNBT())
	writeChatTypeBound(w, chatTypeIndex, senderName, targetName)
	return w.Bytes()
}

// DecodeDisguisedChatPacket decodes ClientboundDisguisedChatPacket (0x21).
func DecodeDisguisedChatPacket(r *protocol.Reader) (message ChatComponent, chatTypeIndex int32, senderName ChatComponent, targetName *ChatComponent, err error) {
	message, err = ReadChatComponentNBT(r)
	if err != nil {
		return ChatComponent{}, 0, ChatComponent{}, nil, err
	}
	chatTypeIndex, senderName, targetName, err = readChatTypeBound(r)
	return message, chatTypeIndex, senderName, targetName, err
}

// EncodeUnsignedPlayerChatPacket encodes ClientboundPlayerChatPacket (0x41 in
// protocol 775) for an unsigned chat message.
func EncodeUnsignedPlayerChatPacket(
	globalIndex int32,
	senderUUID [16]byte,
	index int32,
	plainContent string,
	timestampMs int64,
	salt int64,
	unsignedContent *ChatComponent,
	chatTypeIndex int32,
	senderName ChatComponent,
	targetName *ChatComponent,
) []byte {
	w := protocol.NewWriter(128)
	w.VarInt(globalIndex)
	w.UUID(senderUUID)
	w.VarInt(index)
	w.Bool(false) // HasSignature = false
	w.String(plainContent)
	w.Int64(timestampMs)
	w.Int64(salt)
	w.VarInt(0) // PreviousMessages count = 0
	if unsignedContent != nil {
		w.Bool(true)
		w.Raw(unsignedContent.MarshalNBT())
	} else {
		w.Bool(false)
	}
	w.VarInt(0) // FilterMask = PASS_THROUGH (0)
	writeChatTypeBound(w, chatTypeIndex, senderName, targetName)
	return w.Bytes()
}

func writeChatTypeBound(w *protocol.Writer, chatTypeIndex int32, senderName ChatComponent, targetName *ChatComponent) {
	if chatTypeIndex < 0 {
		chatTypeIndex = ChatTypeChat
	}
	// ByteBufCodecs.holderRegistry encodes a registry reference ID as index + 1.
	w.VarInt(chatTypeIndex + 1)
	w.Raw(senderName.MarshalNBT())
	if targetName != nil {
		w.Bool(true)
		w.Raw(targetName.MarshalNBT())
	} else {
		w.Bool(false)
	}
}

func readChatTypeBound(r *protocol.Reader) (chatTypeIndex int32, senderName ChatComponent, targetName *ChatComponent, err error) {
	holderID, err := r.VarInt()
	if err != nil {
		return 0, ChatComponent{}, nil, err
	}
	chatTypeIndex = holderID - 1
	senderName, err = ReadChatComponentNBT(r)
	if err != nil {
		return 0, ChatComponent{}, nil, err
	}
	hasTarget, err := r.Bool()
	if err != nil {
		return 0, ChatComponent{}, nil, err
	}
	if hasTarget {
		tn, err := ReadChatComponentNBT(r)
		if err != nil {
			return 0, ChatComponent{}, nil, err
		}
		targetName = &tn
	}
	return chatTypeIndex, senderName, targetName, nil
}

// FormatPlayerDisplayName returns the styled ChatComponent for session's name,
// applying any custom tab-list display name and scoreboard team prefix, color,
// and suffix.
func (s *Server) FormatPlayerDisplayName(session *PlayerSession) ChatComponent {
	if session == nil {
		return TextComponent("Server")
	}
	if s != nil {
		if custom := s.PlayerTabListDisplayName(session); custom != nil {
			return s.decorateWithTeam(session.Profile.Name, *custom)
		}
	}
	base := TextComponent(session.Profile.Name)
	if s == nil {
		return base
	}
	return s.decorateWithTeam(session.Profile.Name, base)
}

// FormatPlayerNameByString formats a player or entity name with its scoreboard
// team prefix, color, and suffix.
func (s *Server) FormatPlayerNameByString(name string) ChatComponent {
	if s != nil {
		if p := s.PlayerByName(name); p != nil {
			return s.FormatPlayerDisplayName(p)
		}
	}
	base := TextComponent(name)
	if s == nil {
		return base
	}
	return s.decorateWithTeam(name, base)
}

func (s *Server) decorateWithTeam(memberName string, base ChatComponent) ChatComponent {
	if s == nil {
		return base
	}
	team, ok := s.PlayerTeam(memberName)
	if !ok || team == nil {
		return base
	}
	teamColor := ChatColorName(team.Color)
	if teamColor != "" && teamColor != "reset" && base.Color == "" {
		base.Color = teamColor
	}
	if team.Prefix.IsEmpty() && team.Suffix.IsEmpty() {
		return base
	}
	out := TextComponent("")
	if teamColor != "" && teamColor != "reset" {
		out.Color = teamColor
	}
	if !team.Prefix.IsEmpty() {
		out.Extra = append(out.Extra, team.Prefix)
	}
	out.Extra = append(out.Extra, base)
	if !team.Suffix.IsEmpty() {
		out.Extra = append(out.Extra, team.Suffix)
	}
	return out
}

// SendChatComponent sends a rich ChatComponent to session via PlaySystemChatCB.
func (s *Server) SendChatComponent(session *PlayerSession, component ChatComponent, overlay bool) error {
	if session == nil || session.send == nil {
		return nil
	}
	return session.send(PlaySystemChatCB, EncodeSystemChatPacket(component, overlay))
}

// BroadcastChatComponent sends a rich ChatComponent to all connected players.
func (s *Server) BroadcastChatComponent(component ChatComponent, overlay bool) {
	if s == nil {
		return
	}
	s.Broadcast(PlaySystemChatCB, EncodeSystemChatPacket(component, overlay))
}

// FormatPlayerChatMessage builds the decorated ChatComponent for a chat message
// sent by sender using the given chatTypeIndex (e.g. ChatTypeChat).
func (s *Server) FormatPlayerChatMessage(sender *PlayerSession, message string, chatTypeIndex int32, target *ChatComponent) ChatComponent {
	def, ok := ChatTypeByIndex(chatTypeIndex)
	if !ok {
		def = vanillaChatTypes[ChatTypeChat]
	}
	senderComp := s.FormatPlayerDisplayName(sender)
	contentComp := TextComponent(message)
	return def.Chat.Decorate(senderComp, contentComp, target)
}

// BroadcastPlayerChat formats and broadcasts a public chat message from sender
// to all connected players, including team prefix/color/suffix.
func (s *Server) BroadcastPlayerChat(sender *PlayerSession, message string) {
	if s == nil {
		return
	}
	comp := s.FormatPlayerChatMessage(sender, message, ChatTypeChat, nil)
	s.BroadcastChatComponent(comp, false)
}

// FormatJoinMessage builds the vanilla yellow translatable join message
// ("multiplayer.player.joined") for session.
func (s *Server) FormatJoinMessage(session *PlayerSession) ChatComponent {
	nameComp := s.FormatPlayerDisplayName(session)
	comp := TranslationComponent("multiplayer.player.joined", nameComp)
	comp.Color = "yellow"
	return comp
}

// BroadcastJoinMessage broadcasts the player join message to all connected players.
func (s *Server) BroadcastJoinMessage(session *PlayerSession) {
	if s == nil || session == nil {
		return
	}
	s.BroadcastChatComponent(s.FormatJoinMessage(session), false)
}

// FormatLeaveMessage builds the vanilla yellow translatable leave message
// ("multiplayer.player.left") for session.
func (s *Server) FormatLeaveMessage(session *PlayerSession) ChatComponent {
	nameComp := s.FormatPlayerDisplayName(session)
	comp := TranslationComponent("multiplayer.player.left", nameComp)
	comp.Color = "yellow"
	return comp
}

// BroadcastLeaveMessage broadcasts the player leave message to all connected players.
func (s *Server) BroadcastLeaveMessage(session *PlayerSession) {
	if s == nil || session == nil {
		return
	}
	s.BroadcastChatComponent(s.FormatLeaveMessage(session), false)
}

// FormatDeathMessage constructs the vanilla translatable death message
// component for victim based on cause and optional attackerName.
func (s *Server) FormatDeathMessage(victim *PlayerSession, cause string, attackerName string) ChatComponent {
	victimComp := s.FormatPlayerDisplayName(victim)
	attackerTrimmed := strings.TrimSpace(attackerName)
	causeNorm := strings.ToLower(strings.TrimSpace(cause))

	if attackerTrimmed != "" {
		attackerComp := s.FormatPlayerNameByString(attackerTrimmed)
		switch causeNorm {
		case "arrow", "projectile":
			return TranslationComponent("death.attack.arrow", victimComp, attackerComp)
		case "explosion", "explosion.player", "creeper":
			return TranslationComponent("death.attack.explosion.player", victimComp, attackerComp)
		case "player":
			return TranslationComponent("death.attack.player", victimComp, attackerComp)
		default:
			return TranslationComponent("death.attack.mob", victimComp, attackerComp)
		}
	}

	switch causeNorm {
	case "fall":
		return TranslationComponent("death.fell.accident.generic", victimComp)
	case "lava":
		return TranslationComponent("death.attack.lava", victimComp)
	case "in_fire", "fire":
		return TranslationComponent("death.attack.inFire", victimComp)
	case "on_fire":
		return TranslationComponent("death.attack.onFire", victimComp)
	case "drown", "drowning":
		return TranslationComponent("death.attack.drown", victimComp)
	case "starve", "starvation":
		return TranslationComponent("death.attack.starve", victimComp)
	case "cactus", "sweet_berry_bush":
		return TranslationComponent("death.attack.cactus", victimComp)
	case "magic", "indirect_magic":
		return TranslationComponent("death.attack.magic", victimComp)
	case "wither":
		return TranslationComponent("death.attack.wither", victimComp)
	case "in_wall", "suffocation":
		return TranslationComponent("death.attack.inWall", victimComp)
	case "out_of_world", "void":
		return TranslationComponent("death.attack.outOfWorld", victimComp)
	case "explosion":
		return TranslationComponent("death.attack.explosion", victimComp)
	default:
		return TranslationComponent("death.attack.generic", victimComp)
	}
}

// BroadcastDeathMessage broadcasts victim's death message to online players,
// respecting the victim's team DeathMessageVisibility rule ("always", "never",
// "hideForOtherTeams", "hideForOwnTeam").
func (s *Server) BroadcastDeathMessage(victim *PlayerSession, cause string, attackerName string) {
	if s == nil || victim == nil {
		return
	}
	msg := s.FormatDeathMessage(victim, cause, attackerName)
	victimTeam, hasTeam := s.PlayerTeam(victim.Profile.Name)
	vis := TeamVisibilityAlways
	if hasTeam && victimTeam != nil && victimTeam.DeathMessageVisibility != "" {
		vis = victimTeam.DeathMessageVisibility
	}
	if vis == TeamVisibilityNever {
		return
	}
	pkt := EncodeSystemChatPacket(msg, false)
	for _, recipient := range s.onlinePlayersSnapshot() {
		if recipient == nil || recipient.send == nil {
			continue
		}
		if hasTeam && victimTeam != nil {
			recTeam, recHas := s.PlayerTeam(recipient.Profile.Name)
			sameTeam := recHas && recTeam != nil && recTeam.Name == victimTeam.Name
			if vis == TeamVisibilityHideForOtherTeams && !sameTeam {
				continue
			}
			if vis == TeamVisibilityHideForOwnTeam && sameTeam {
				continue
			}
		}
		_ = recipient.send(PlaySystemChatCB, pkt)
	}
}

// ResolveCommandTargets resolves a target selector (@a, @p, @s, @r, @e) or a
// player username into matching online PlayerSessions.
func (s *Server) ResolveCommandTargets(sender *PlayerSession, token string) []*PlayerSession {
	if s == nil {
		return nil
	}
	trimmed := strings.TrimSpace(token)
	lower := strings.ToLower(trimmed)
	switch {
	case lower == "@s":
		if sender != nil {
			return []*PlayerSession{sender}
		}
		return nil
	case lower == "@a" || strings.HasPrefix(lower, "@a[") || lower == "@e[type=player]" || lower == "@e[type=minecraft:player]":
		return s.onlinePlayersSnapshot()
	case lower == "@p" || strings.HasPrefix(lower, "@p["):
		if sender != nil {
			return []*PlayerSession{sender}
		}
		players := s.onlinePlayersSnapshot()
		if len(players) > 0 {
			return []*PlayerSession{players[0]}
		}
		return nil
	case lower == "@r" || strings.HasPrefix(lower, "@r["):
		players := s.onlinePlayersSnapshot()
		if len(players) == 0 {
			return nil
		}
		return []*PlayerSession{players[rand.Intn(len(players))]}
	default:
		if p := s.PlayerByName(trimmed); p != nil {
			return []*PlayerSession{p}
		}
		return nil
	}
}

// HandleMsgCommand implements /msg, /tell, and /w:
//
//	/msg <targets> <message...>
func (s *Server) HandleMsgCommand(sender *PlayerSession, args []string) string {
	if s == nil {
		return "Server unavailable"
	}
	if len(args) < 2 {
		return "Usage: /msg <target> <message>"
	}
	targets := s.ResolveCommandTargets(sender, args[0])
	if len(targets) == 0 {
		return fmt.Sprintf("No player was found matching %q", args[0])
	}
	msgText := strings.Join(args[1:], " ")
	senderComp := s.FormatPlayerDisplayName(sender)
	contentComp := TextComponent(msgText)

	inDef, _ := ChatTypeByIndex(ChatTypeMsgCommandIncoming)
	outDef, _ := ChatTypeByIndex(ChatTypeMsgCommandOutgoing)

	for _, target := range targets {
		targetComp := s.FormatPlayerDisplayName(target)
		inMsg := inDef.Chat.Decorate(senderComp, contentComp, &targetComp)
		_ = s.SendChatComponent(target, inMsg, false)

		if sender != nil {
			outMsg := outDef.Chat.Decorate(senderComp, contentComp, &targetComp)
			_ = s.SendChatComponent(sender, outMsg, false)
		}
	}
	return ""
}

// HandleMeCommand implements /me <action...>.
func (s *Server) HandleMeCommand(sender *PlayerSession, args []string) string {
	if s == nil {
		return "Server unavailable"
	}
	if len(args) == 0 {
		return "Usage: /me <action>"
	}
	action := strings.Join(args, " ")
	comp := s.FormatPlayerChatMessage(sender, action, ChatTypeEmoteCommand, nil)
	s.BroadcastChatComponent(comp, false)
	return ""
}

// HandleSayCommand implements /say <message...>.
func (s *Server) HandleSayCommand(sender *PlayerSession, args []string) string {
	if s == nil {
		return "Server unavailable"
	}
	if !s.HasAdminPermission(sender) {
		return "You do not have permission to use this command"
	}
	if len(args) == 0 {
		return "Usage: /say <message>"
	}
	msg := strings.Join(args, " ")
	comp := s.FormatPlayerChatMessage(sender, msg, ChatTypeSayCommand, nil)
	s.BroadcastChatComponent(comp, false)
	return ""
}

// HandleTeamMsgCommand implements /teammsg (/tm) <message...>.
func (s *Server) HandleTeamMsgCommand(sender *PlayerSession, args []string) string {
	if s == nil {
		return "Server unavailable"
	}
	if sender == nil {
		return "Only players on a team can use /teammsg"
	}
	if len(args) == 0 {
		return "Usage: /teammsg <message>"
	}
	team, ok := s.PlayerTeam(sender.Profile.Name)
	if !ok || team == nil {
		return "You are not on a team"
	}
	msgText := strings.Join(args, " ")
	senderComp := s.FormatPlayerDisplayName(sender)
	contentComp := TextComponent(msgText)
	teamComp := team.DisplayName
	if teamComp.IsEmpty() {
		teamComp = TextComponent(team.Name)
	}
	if cName := ChatColorName(team.Color); cName != "" && cName != "reset" {
		teamComp.Color = cName
	}

	inDef, _ := ChatTypeByIndex(ChatTypeTeamMsgCommandIncoming)
	outDef, _ := ChatTypeByIndex(ChatTypeTeamMsgCommandOutgoing)
	inMsg := inDef.Chat.Decorate(senderComp, contentComp, &teamComp)
	outMsg := outDef.Chat.Decorate(senderComp, contentComp, &teamComp)

	for _, p := range s.onlinePlayersSnapshot() {
		if p == nil {
			continue
		}
		pTeam, has := s.PlayerTeam(p.Profile.Name)
		if !has || pTeam == nil || pTeam.Name != team.Name {
			continue
		}
		if p.Profile.UUID == sender.Profile.UUID {
			_ = s.SendChatComponent(p, outMsg, false)
		} else {
			_ = s.SendChatComponent(p, inMsg, false)
		}
	}
	return ""
}

// HandleTellrawCommand implements /tellraw <targets> <raw json message>.
func (s *Server) HandleTellrawCommand(sender *PlayerSession, args []string) string {
	if s == nil {
		return "Server unavailable"
	}
	if !s.HasAdminPermission(sender) {
		return "You do not have permission to use this command"
	}
	if len(args) < 2 {
		return "Usage: /tellraw <targets> <message>"
	}
	targets := s.ResolveCommandTargets(sender, args[0])
	if len(targets) == 0 {
		return fmt.Sprintf("No player was found matching %q", args[0])
	}
	rawJSON := strings.Join(args[1:], " ")
	comp, err := ParseChatComponentJSON(rawJSON)
	if err != nil {
		return fmt.Sprintf("Invalid tellraw JSON: %v", err)
	}
	for _, target := range targets {
		_ = s.SendChatComponent(target, comp, false)
	}
	return ""
}
