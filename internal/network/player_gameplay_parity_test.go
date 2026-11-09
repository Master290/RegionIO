package network

import (
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/server"
	"regionio/internal/world"
)

func TestPlayContainerClickDragAndStateSyncProtocol775(t *testing.T) {
	cfg := server.DefaultConfig()
	cfg.WorldDir = t.TempDir()
	srv, err := server.New(cfg)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	session, err := srv.RegisterPlayer(server.Profile{
		Name: "NetCrafter",
		UUID: server.OfflineUUID("NetCrafter"),
	}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(session, server.GameModeSurvival)

	h := &handler{
		srv:     srv,
		session: session,
	}

	// Put 8 oak_planks on the cursor and perform a 2-slot left-drag (QuickCraft mode 5) via handleContainerClick.
	session.SetCarriedItem(world.NewNamedItemStack("minecraft:oak_planks", 8))

	sendClick := func(stateID int32, slot int16, button int8, mode int32) {
		w := protocol.NewWriter(16)
		w.VarInt(0) // containerID 0
		w.VarInt(stateID)
		w.Uint16(uint16(slot))
		w.Byte(byte(button))
		w.VarInt(mode)
		w.VarInt(0)     // 0 changed slots
		w.Bool(false)   // empty carried hashed stack
		pkt := protocol.Packet{ID: protocol.PlayContainerClick, Data: w.Bytes()}
		if err := h.handleContainerClick(pkt); err != nil {
			t.Fatalf("handleContainerClick(slot=%d, btn=%d, mode=%d): %v", slot, button, mode, err)
		}
	}

	curState := session.ContainerStateID()
	sendClick(curState, -999, server.QuickcraftMask(server.QuickCraftHeaderStart, server.QuickCraftTypeCharitable), server.ContainerInputQuickCraft)
	sendClick(curState, 36, server.QuickcraftMask(server.QuickCraftHeaderContinue, server.QuickCraftTypeCharitable), server.ContainerInputQuickCraft)
	sendClick(curState, 37, server.QuickcraftMask(server.QuickCraftHeaderContinue, server.QuickCraftTypeCharitable), server.ContainerInputQuickCraft)
	sendClick(curState, -999, server.QuickcraftMask(server.QuickCraftHeaderEnd, server.QuickCraftTypeCharitable), server.ContainerInputQuickCraft)

	if s36 := session.InventorySlot(36); s36.ResolvedID() != "minecraft:oak_planks" || s36.Count != 4 {
		t.Fatalf("hotbar slot 36 after network QuickCraft = %+v, want 4 oak_planks", s36)
	}
	if s37 := session.InventorySlot(37); s37.ResolvedID() != "minecraft:oak_planks" || s37.Count != 4 {
		t.Fatalf("hotbar slot 37 after network QuickCraft = %+v, want 4 oak_planks", s37)
	}
	if !session.CarriedItem().IsEmpty() {
		t.Fatalf("expected cursor to be empty after even split, got %+v", session.CarriedItem())
	}
	if h.hotbar[0] <= 0 || h.hotbar[1] <= 0 {
		t.Fatalf("expected handler hotbar cache to sync slots 0 & 1, got %v, %v", h.hotbar[0], h.hotbar[1])
	}
}
