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
	PlayAbilities           = 0x40
	PlaySetHealth           = 0x68
	PlaySetHeldSlot         = 0x69
	PlayDisconnect          = 0x20
	PlayBlockUpdate         = 0x08
	PlayBlockChangedAck     = 0x04
	PlayCommands            = 0x10
	PlayContainerClose      = 0x11
	PlayContainerSetContent = 0x12
	PlayContainerSetData    = 0x13
	PlayContainerSetSlot    = 0x14
	PlaySystemChat          = 0x79
	PlayLightUpdate         = 0x30
	PlayPlayerInfoRemove    = 0x45
	PlayPlayerInfoUpdate    = 0x46
	PlaySetTime             = 0x71
	PlayDamageEvent         = 0x19
	PlayEntityEvent           = 0x22
	PlayHorseScreenOpen       = 0x29
	PlayHurtAnimation         = 0x2a
	PlayOpenScreen            = 0x3b
	PlayMerchantOffers        = 0x34
	PlayPlayerCombatKill      = 0x44
	PlayRespawn               = 0x52
	PlayRemoveMobEffect       = 0x4e
	PlaySelectAdvancementsTab = 0x55
	PlaySetCamera             = 0x56
	PlaySetExperience         = 0x67
	PlayUpdateAdvancements    = 0x82
	PlayUpdateMobEffect       = 0x84
	PlayBossEvent             = 0x09
	PlayClearTitles           = 0x0e
	PlayDisguisedChat         = 0x21
	PlayMapItemData           = 0x32
	PlayPlayerChat            = 0x41
	PlayResetScore            = 0x4f
	PlaySetActionBarText      = 0x57
	PlaySetDisplayObjective   = 0x62
	PlaySetObjective          = 0x6a
	PlaySetPlayerTeam         = 0x6d
	PlaySetScore              = 0x6e
	PlaySetSubtitleText       = 0x70
	PlaySetTitleText             = 0x72
	PlaySetTitlesAnimation       = 0x73
	PlayTabList                  = 0x7a
	PlayInitializeBorder         = 0x2b
	PlaySetBorderCenter          = 0x58
	PlaySetBorderLerpSize        = 0x59
	PlaySetBorderSize            = 0x5a
	PlaySetBorderWarningDelay    = 0x5b
	PlaySetBorderWarningDistance = 0x5c
	PlayBlockEntityData          = 0x06
	PlayBlockEvent               = 0x07
	PlayLevelEvent               = 0x2e
	PlayLevelParticles           = 0x2f
	PlayOpenBook                 = 0x3a
	PlayOpenSignEditor           = 0x3c
	PlayAwardStats               = 0x03
	PlayChangeDifficulty         = 0x0a
	PlayPlaceGhostRecipe         = 0x3f
	PlayRecipeBookAdd            = 0x4a
	PlayRecipeBookRemove         = 0x4b
	PlayRecipeBookSettings       = 0x4c
	PlayUpdateRecipes            = 0x85

	// Entity packets
	PlayAddEntity        = 0x01
	PlayTeleportEntity   = 0x7d
	PlayMoveEntityPos    = 0x35
	PlayMoveEntityPosRot = 0x36
	PlayMoveEntityRot    = 0x38
	PlayMoveVehicle      = 0x39
	PlaySetEntityMotion  = 0x65
	PlayRemoveEntities   = 0x4d
	PlaySetEntityData    = 0x63
	PlaySetEquipment     = 0x66
	PlaySetPassengers    = 0x6b
	PlayTakeItemEntity   = 0x7c
)

// Play, serverbound (protocol 775).
const (
	PlayAcceptTeleport            = 0x00
	PlayAttack                    = 0x01
	PlayChangeDifficultySB        = 0x04
	PlayChatCommand               = 0x07
	PlayChatCommandSigned         = 0x08
	PlayClientCommand             = 0x0c
	PlayContainerButtonClick      = 0x11
	PlayContainerClick            = 0x12
	PlayContainerCloseSB          = 0x13
	PlayContainerSlotStateChanged = 0x14
	PlayEditBook                  = 0x18
	PlayInteract                  = 0x1a
	PlayKeepAliveServer           = 0x1c
	PlayLockDifficultySB          = 0x1d
	PlayClientTickEnd             = 0x0d
	PlayClientInformation         = 0x0e
	PlayCustomPayload             = 0x16
	PlayMovePos                   = 0x1e
	PlayMovePosRot                = 0x1f
	PlayMoveRot                   = 0x20
	PlayMoveStatusOnly            = 0x21
	PlayMoveVehicleSB             = 0x22
	PlayPaddleBoatSB              = 0x23
	PlayPickItemFromBlockSB       = 0x24
	PlayPickItemFromEntitySB      = 0x25
	PlayPlayerAbilitiesSB         = 0x26
	PlayPlaceRecipe               = 0x27
	PlayPlayerLoaded              = 0x2c
	PlayPlayerAction              = 0x29
	PlayPlayerCommand             = 0x2a
	PlayPlayerInputSB             = 0x2b
	PlayRecipeBookChangeSettings  = 0x2e
	PlayRecipeBookSeenRecipe      = 0x2f
	PlayRenameItem                = 0x30
	PlaySeenAdvancements          = 0x32
	PlaySelectTrade               = 0x33
	PlaySignUpdate                = 0x3c
	PlayTeleportToEntitySB        = 0x3d
	PlayUseItemOn                 = 0x42
	PlayUseItem                   = 0x43
	PlaySetCreativeSlot           = 0x38
	PlaySetCarriedItem            = 0x35
	PlayChatMessage               = 0x09
)

// Built-in entity_type registry IDs (protocol 775).
const (
	EntityTypeExperienceOrb = 44
	EntityTypeVillager      = 139
)

// GameEvent sub-IDs carried by the clientbound game_event packet.
const (
	GameEventChangeGameMode      = 3
	GameEventWinGame             = 4
	GameEventEnableRespawnScreen = 11
	GameEventLimitedCrafting     = 12
	GameEventStartWaitingChunks  = 13
)

// ClientboundRespawnPacket dataToKeep bitflags (protocol 775).
const (
	RespawnKeepNothing    byte = 0x00
	RespawnKeepAttributes byte = 0x01
	RespawnKeepEntityData byte = 0x02
	RespawnKeepAllData    byte = 0x03
)

// NextState values carried by the handshake packet.
const (
	NextStateStatus   = 1
	NextStateLogin    = 2
	NextStateTransfer = 3
)
