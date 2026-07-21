package protocol

import "testing"

func TestPlayPacketIDsProtocol775(t *testing.T) {
	clientbound := map[string]int32{
		"PlayAddEntity":        PlayAddEntity,
		"PlayBlockChangedAck":  PlayBlockChangedAck,
		"PlayBlockUpdate":      PlayBlockUpdate,
		"PlayDisconnect":       PlayDisconnect,
		"PlayForgetLevelChunk": PlayForgetLevelChunk,
		"PlayGameEvent":        PlayGameEvent,
		"PlayKeepAliveCB":      PlayKeepAliveCB,
		"PlayLevelChunk":       PlayLevelChunk,
		"PlayLogin":            PlayLogin,
		"PlayMoveEntityPos":    PlayMoveEntityPos,
		"PlayMoveEntityPosRot": PlayMoveEntityPosRot,
		"PlayMoveEntityRot":    PlayMoveEntityRot,
		"PlayAbilities":        PlayAbilities,
		"PlayPlayerPosition":   PlayPlayerPosition,
		"PlayRemoveEntities":   PlayRemoveEntities,
		"PlayChunkCacheCenter": PlayChunkCacheCenter,
		"PlayDefaultSpawnPos":  PlayDefaultSpawnPos,
		"PlaySetEntityData":    PlaySetEntityData,
		"PlaySetEntityMotion":  PlaySetEntityMotion,
		"PlaySetEquipment":     PlaySetEquipment,
		"PlaySetHeldSlot":      PlaySetHeldSlot,
		"PlaySystemChat":       PlaySystemChat,
		"PlayLightUpdate":      PlayLightUpdate,
		"PlayPlayerInfoRemove": PlayPlayerInfoRemove,
		"PlayPlayerInfoUpdate": PlayPlayerInfoUpdate,
		"PlayTeleportEntity":   PlayTeleportEntity,
	}
	wantClientbound := map[string]int32{
		"PlayAddEntity":        0x01,
		"PlayBlockChangedAck":  0x04,
		"PlayBlockUpdate":      0x08,
		"PlayDisconnect":       0x20,
		"PlayForgetLevelChunk": 0x25,
		"PlayGameEvent":        0x26,
		"PlayKeepAliveCB":      0x2c,
		"PlayLevelChunk":       0x2d,
		"PlayLogin":            0x31,
		"PlayMoveEntityPos":    0x35,
		"PlayMoveEntityPosRot": 0x36,
		"PlayMoveEntityRot":    0x38,
		"PlayAbilities":        0x40,
		"PlayPlayerPosition":   0x48,
		"PlayRemoveEntities":   0x4d,
		"PlayChunkCacheCenter": 0x5e,
		"PlayDefaultSpawnPos":  0x61,
		"PlaySetEntityData":    0x63,
		"PlaySetEntityMotion":  0x65,
		"PlaySetEquipment":     0x66,
		"PlaySetHeldSlot":      0x69,
		"PlaySystemChat":       0x79,
		"PlayLightUpdate":      0x30,
		"PlayPlayerInfoRemove": 0x45,
		"PlayPlayerInfoUpdate": 0x46,
		"PlayTeleportEntity":   0x7d,
	}
	assertIDs(t, "clientbound", clientbound, wantClientbound)

	serverbound := map[string]int32{
		"PlayAcceptTeleport":    PlayAcceptTeleport,
		"PlayKeepAliveServer":   PlayKeepAliveServer,
		"PlayClientTickEnd":     PlayClientTickEnd,
		"PlayClientInformation": PlayClientInformation,
		"PlayCustomPayload":     PlayCustomPayload,
		"PlayMovePos":           PlayMovePos,
		"PlayMovePosRot":        PlayMovePosRot,
		"PlayMoveRot":           PlayMoveRot,
		"PlayMoveStatusOnly":    PlayMoveStatusOnly,
		"PlayPlayerLoaded":      PlayPlayerLoaded,
		"PlayPlayerAction":      PlayPlayerAction,
		"PlayUseItemOn":         PlayUseItemOn,
		"PlaySetCreativeSlot":   PlaySetCreativeSlot,
		"PlaySetCarriedItem":    PlaySetCarriedItem,
		"PlayChatMessage":       PlayChatMessage,
	}
	wantServerbound := map[string]int32{
		"PlayAcceptTeleport":    0x00,
		"PlayKeepAliveServer":   0x1c,
		"PlayClientTickEnd":     0x0d,
		"PlayClientInformation": 0x0e,
		"PlayCustomPayload":     0x16,
		"PlayMovePos":           0x1e,
		"PlayMovePosRot":        0x1f,
		"PlayMoveRot":           0x20,
		"PlayMoveStatusOnly":    0x21,
		"PlayPlayerLoaded":      0x2c,
		"PlayPlayerAction":      0x29,
		"PlayUseItemOn":         0x42,
		"PlaySetCreativeSlot":   0x38,
		"PlaySetCarriedItem":    0x35,
		"PlayChatMessage":       0x09,
	}
	assertIDs(t, "serverbound", serverbound, wantServerbound)
}

func assertIDs(t *testing.T, direction string, got, want map[string]int32) {
	t.Helper()
	seen := make(map[int32]string, len(got))
	for name, gotID := range got {
		wantID, ok := want[name]
		if !ok {
			t.Fatalf("%s %s missing expected ID", direction, name)
		}
		if gotID != wantID {
			t.Fatalf("%s %s = %#x, want %#x", direction, name, gotID, wantID)
		}
		if previous, ok := seen[gotID]; ok {
			t.Fatalf("%s duplicate packet ID %#x: %s and %s", direction, gotID, previous, name)
		}
		seen[gotID] = name
	}
}
