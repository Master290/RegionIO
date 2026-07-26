package protocol

// Packet IDs, grouped by state and direction. Serverbound = client→server,
// Clientbound = server→client. Values are for protocol 775 (26.1.2).

// Handshaking, serverbound.
const (
	HandshakeID = 0x00
)

// Status, serverbound.
const (
	StatusRequestID = 0x00
	PingRequestID   = 0x01
)

// Status, clientbound.
const (
	StatusResponseID = 0x00
	PongResponseID   = 0x01
)

// Login, serverbound.
const (
	LoginStartID        = 0x00
	EncryptionResponse  = 0x01
	LoginPluginResponse = 0x02
	LoginAcknowledgedID = 0x03
	CookieResponseLogin = 0x04
)

// Login, clientbound.
const (
	LoginDisconnectID  = 0x00
	EncryptionRequest  = 0x01
	LoginSuccessID     = 0x02
	SetCompressionID   = 0x03
	LoginPluginReq     = 0x04
	CookieRequestLogin = 0x05
)

// Configuration, serverbound.
const (
	ConfigClientInformation = 0x00
	ConfigCookieResponse    = 0x01
	ConfigCustomPayload     = 0x02
	ConfigFinishServerbound = 0x03
	ConfigKeepAliveServer   = 0x04
	ConfigPong              = 0x05
	ConfigResourcePackResp  = 0x06
	ConfigKnownPacksServer  = 0x07
)

// Configuration, clientbound.
const (
	ConfigCookieRequest         = 0x00
	ConfigCustomPayloadCB       = 0x01
	ConfigDisconnect            = 0x02
	ConfigFinishClientbound     = 0x03
	ConfigKeepAliveCB           = 0x04
	ConfigPing                  = 0x05
	ConfigRegistryData          = 0x07
	ConfigUpdateEnabledFeatures = 0x0c
	ConfigUpdateTags            = 0x0d
	ConfigKnownPacksCB          = 0x0e
)

// Play, clientbound (protocol 775).
const (
	PlayLogin            = 0x31
	PlayGameEvent        = 0x26
	PlayKeepAliveCB      = 0x2c
	PlayPlayerPosition   = 0x48
	PlayDefaultSpawnPos  = 0x61
	PlayChunkCacheCenter = 0x5e
	PlayLevelChunk       = 0x2d
	PlayForgetLevelChunk = 0x25
	PlayAbilities        = 0x40
	PlaySetHeldSlot      = 0x69
	PlayDisconnect       = 0x20
	PlayBlockUpdate      = 0x08
	PlayBlockChangedAck  = 0x04
	PlaySystemChat       = 0x79
	PlayLightUpdate      = 0x30
	PlayPlayerInfoRemove = 0x45
	PlayPlayerInfoUpdate = 0x46
	PlaySetTime          = 0x71

	// Entity packets
	PlayAddEntity        = 0x01
	PlayTeleportEntity   = 0x7d
	PlayMoveEntityPos    = 0x35
	PlayMoveEntityPosRot = 0x36
	PlayMoveEntityRot    = 0x38
	PlaySetEntityMotion  = 0x65
	PlayRemoveEntities   = 0x4d
	PlaySetEntityData    = 0x63
	PlaySetEquipment     = 0x66
)

// Play, serverbound (protocol 775).
const (
	PlayAcceptTeleport    = 0x00
	PlayKeepAliveServer   = 0x1c
	PlayClientTickEnd     = 0x0d
	PlayClientInformation = 0x0e
	PlayCustomPayload     = 0x16
	PlayMovePos           = 0x1e
	PlayMovePosRot        = 0x1f
	PlayMoveRot           = 0x20
	PlayMoveStatusOnly    = 0x21
	PlayPlayerLoaded      = 0x2c
	PlayPlayerAction      = 0x29
	PlayUseItemOn         = 0x42
	PlaySetCreativeSlot   = 0x38
	PlaySetCarriedItem    = 0x35
	PlayChatMessage       = 0x09
)

// GameEvent sub-IDs carried by the clientbound game_event packet.
const (
	GameEventStartWaitingChunks = 13
)

// NextState values carried by the handshake packet.
const (
	NextStateStatus   = 1
	NextStateLogin    = 2
	NextStateTransfer = 3
)
