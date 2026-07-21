package network

import (
	"errors"

	"regionio/internal/protocol"
	"regionio/internal/registry"
)

// beginConfiguration is called on entering the configuration phase. It mirrors
// the vanilla opening sequence: server brand, enabled feature flags, then the
// known-packs negotiation. The client's known-packs reply triggers the registry
// data (see handleKnownPacks).
func (h *handler) beginConfiguration() error {
	if err := h.sendBrand(); err != nil {
		return err
	}
	if err := h.sendEnabledFeatures(); err != nil {
		return err
	}
	return h.sendKnownPacks()
}

// sendBrand reports the server brand on the minecraft:brand plugin channel.
func (h *handler) sendBrand() error {
	w := protocol.NewWriter(32)
	w.String("minecraft:brand").String("RegionIO")
	return h.conn.SendWriter(protocol.ConfigCustomPayloadCB, w)
}

// sendEnabledFeatures enables the vanilla feature flag set. The client requires
// this so that vanilla content (blocks, items, registries) is active.
func (h *handler) sendEnabledFeatures() error {
	w := protocol.NewWriter(24)
	w.VarInt(1)
	w.String("minecraft:vanilla")
	return h.conn.SendWriter(protocol.ConfigUpdateEnabledFeatures, w)
}

// handleConfiguration drives the configuration phase:
//
//	S→C select_known_packs     (on entry)
//	C→S client_information     → record settings
//	C→S custom_payload (brand) → log the client brand
//	C→S select_known_packs     → send registry data, then finish_configuration
//	C→S finish_configuration   → switch to the Play phase
func (h *handler) handleConfiguration(pkt protocol.Packet) error {
	switch pkt.ID {
	case protocol.ConfigClientInformation:
		return h.handleClientInformation(pkt)

	case protocol.ConfigCustomPayload:
		return h.handleConfigCustomPayload(pkt)

	case protocol.ConfigKnownPacksServer:
		return h.handleKnownPacks(pkt)

	case protocol.ConfigKeepAliveServer, protocol.ConfigPong,
		protocol.ConfigResourcePackResp, protocol.ConfigCookieResponse:
		h.log.Debug("configuration packet ignored", "id", pkt.ID)
		return nil

	case protocol.ConfigFinishServerbound:
		h.conn.SetState(protocol.StatePlay)
		h.log.Info("entered play phase", "name", h.conn.Profile.Name)
		return h.beginPlay()

	default:
		h.log.Debug("unknown configuration packet", "id", pkt.ID)
		return nil
	}
}

// handleClientInformation records the client's settings (no longer the trigger
// to finish configuration; that is now driven by known-packs negotiation).
func (h *handler) handleClientInformation(pkt protocol.Packet) error {
	r := pkt.Body()
	locale, err := r.String()
	if err != nil {
		return err
	}
	vd, err := r.ReadByte()
	if err != nil {
		return err
	}
	chatMode, err := r.VarInt()
	if err != nil {
		return err
	}
	if _, err := r.Bool(); err != nil { // chat colors
		return err
	}
	if _, err := r.ReadByte(); err != nil { // displayed skin parts
		return err
	}
	mainHand, err := r.VarInt()
	if err != nil {
		return err
	}

	// view_distance drives the chunk streamer radius. The server owns the upper
	// bound because accepting the client's render distance can multiply cold
	// generation work into hundreds of chunks.
	vdInt := int(int8(vd))
	if vdInt < 2 {
		vdInt = 2
	}
	if max := h.srv.Config().MaxViewDistance; vdInt > max {
		vdInt = max
	}
	h.viewDistance = vdInt

	h.log.Info("client information",
		"locale", locale, "view_distance", vdInt,
		"chat_mode", chatMode, "main_hand", mainHand)
	return nil
}

// handleConfigCustomPayload logs the client brand and ignores other channels.
func (h *handler) handleConfigCustomPayload(pkt protocol.Packet) error {
	r := pkt.Body()
	channel, err := r.String()
	if err != nil {
		return err
	}
	if channel == "minecraft:brand" {
		brand, err := r.String()
		if err != nil {
			return err
		}
		h.log.Info("client brand", "brand", brand)
	} else {
		h.log.Debug("plugin message", "channel", channel)
	}
	return nil
}

// handleKnownPacks reads the client's known packs and, once we know what it
// already has, sends the registry data followed by finish_configuration.
func (h *handler) handleKnownPacks(pkt protocol.Packet) error {
	r := pkt.Body()
	count, err := r.VarInt()
	if err != nil {
		return err
	}
	if count < 0 || count > 1024 {
		return errors.New("implausible known-pack count")
	}
	hasCore := false
	for i := int32(0); i < count; i++ {
		ns, err := r.String()
		if err != nil {
			return err
		}
		id, err := r.String()
		if err != nil {
			return err
		}
		ver, err := r.String()
		if err != nil {
			return err
		}
		if ns == registry.CorePack.Namespace && id == registry.CorePack.ID &&
			ver == registry.CorePack.Version {
			hasCore = true
		}
		h.log.Debug("client known pack", "namespace", ns, "id", id, "version", ver)
	}
	if !hasCore {
		// Without the matching pack the client cannot fill in registry data
		// from its built-in copy; sending has_data=false would desync it.
		h.log.Warn("client lacks matching core pack; registry data may desync",
			"want", registry.CorePack.Version)
	}

	if err := h.sendRegistries(); err != nil {
		return err
	}
	if err := h.sendUpdateTags(); err != nil {
		return err
	}
	return h.sendFinishConfiguration()
}

// sendKnownPacks advertises the vanilla core pack to the client.
func (h *handler) sendKnownPacks() error {
	p := registry.CorePack
	w := protocol.NewWriter(32)
	w.VarInt(1)
	w.String(p.Namespace).String(p.ID).String(p.Version)
	return h.conn.SendWriter(protocol.ConfigKnownPacksCB, w)
}

// sendRegistries sends one registry_data packet per synchronized registry. Each
// entry is sent with has_data=false; the client supplies the contents from its
// matching known pack.
func (h *handler) sendRegistries() error {
	for _, reg := range registry.Synced() {
		w := protocol.NewWriter(64 + len(reg.Entries)*24)
		w.String(reg.Name)
		w.VarInt(int32(len(reg.Entries)))
		for _, entry := range reg.Entries {
			w.String(entry)
			w.Bool(false) // has_data: client uses its own copy
		}
		if err := h.conn.SendWriter(protocol.ConfigRegistryData, w); err != nil {
			return err
		}
	}
	h.log.Debug("sent registry data", "registries", len(registry.Synced()))
	return nil
}

// sendUpdateTags sends the captured vanilla tag set. Tags map registry entries
// (by numeric index) into named groups the client and gameplay rely on.
func (h *handler) sendUpdateTags() error {
	return h.conn.Send(protocol.ConfigUpdateTags, registry.Tags())
}

// sendFinishConfiguration signals configuration is complete; the client replies
// with its own finish_configuration to enter Play.
func (h *handler) sendFinishConfiguration() error {
	return h.conn.Send(protocol.ConfigFinishClientbound, nil)
}
