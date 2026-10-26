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
	ConfigClientInformation   = 0x00
	ConfigCookieResponse      = 0x01
	ConfigCustomPayload       = 0x02
	ConfigFinishServerbound   = 0x03
	ConfigKeepAliveServer     = 0x04
	ConfigPong                = 0x05
	ConfigResourcePackResp    = 0x06
	ConfigKnownPacksServer    = 0x07
	ConfigCustomClickAction   = 0x08
	ConfigAcceptCodeOfConduct = 0x09
)

// Configuration, clientbound.
const (
	ConfigCookieRequest         = 0x00
	ConfigCustomPayloadCB       = 0x01
	ConfigDisconnect            = 0x02
	ConfigFinishClientbound     = 0x03
	ConfigKeepAliveCB           = 0x04
	ConfigPing                  = 0x05
	ConfigResetChat             = 0x06
	ConfigRegistryData          = 0x07
	ConfigResourcePackPop       = 0x08
	ConfigResourcePackPush      = 0x09
	ConfigStoreCookie           = 0x0a
	ConfigTransfer              = 0x0b
	ConfigUpdateEnabledFeatures = 0x0c
	ConfigUpdateTags            = 0x0d
	ConfigKnownPacksCB          = 0x0e
	ConfigCustomReportDetails   = 0x0f
	ConfigServerLinks           = 0x10
	ConfigClearDialog           = 0x11
	ConfigShowDialog            = 0x12
	ConfigCodeOfConduct         = 0x13
)

// Play, clientbound (protocol 775).
const (
	PlayBundleDelimiter          = 0x00
	PlayAddEntity                = 0x01
	PlayAnimate                  = 0x02
	PlayAwardStats               = 0x03
	PlayBlockChangedAck          = 0x04
	PlayBlockDestruction         = 0x05
	PlayBlockEntityData          = 0x06
	PlayBlockEvent               = 0x07
	PlayBlockUpdate              = 0x08
	PlayBossEvent                = 0x09
	PlayChangeDifficulty         = 0x0a
	PlayChunkBatchFinished       = 0x0b
	PlayChunkBatchStart          = 0x0c
	PlayChunksBiomes             = 0x0d
	PlayClearTitles              = 0x0e
	PlayCommandSuggestions       = 0x0f
	PlayCommands                 = 0x10
	PlayContainerClose           = 0x11
	PlayContainerSetContent      = 0x12
	PlayContainerSetData         = 0x13
	PlayContainerSetSlot         = 0x14
	PlayCookieRequest            = 0x15
	PlayCooldown                 = 0x16
	PlayCustomChatCompletions    = 0x17
	PlayCustomPayloadCB          = 0x18
	PlayDamageEvent              = 0x19
	PlayDeleteChat               = 0x1f
	PlayDisconnect               = 0x20
	PlayDisguisedChat            = 0x21
	PlayEntityEvent              = 0x22
	PlayEntityPositionSync       = 0x23
	PlayExplode                  = 0x24
	PlayForgetLevelChunk         = 0x25
	PlayGameEvent                = 0x26
	PlayGameRuleValues           = 0x27
	PlayGameTestHighlightPos     = 0x28
	PlayHorseScreenOpen          = 0x29
	PlayHurtAnimation            = 0x2a
	PlayInitializeBorder         = 0x2b
	PlayKeepAliveCB              = 0x2c
	PlayLevelChunk               = 0x2d
	PlayLevelEvent               = 0x2e
	PlayLevelParticles           = 0x2f
	PlayLightUpdate              = 0x30
	PlayLogin                    = 0x31
	PlayLowDiskSpaceWarning      = 0x32
	PlayMapItemData              = 0x33
	PlayMerchantOffers           = 0x34
	PlayMoveEntityPos            = 0x35
	PlayMoveEntityPosRot         = 0x36
	PlayMoveMinecartAlongTrack   = 0x37
	PlayMoveEntityRot            = 0x38
	PlayMoveVehicle              = 0x39
	PlayOpenBook                 = 0x3a
	PlayOpenScreen               = 0x3b
	PlayOpenSignEditor           = 0x3c
	PlayPing                     = 0x3d
	PlayPongResponse             = 0x3e
	PlayPlaceGhostRecipe         = 0x3f
	PlayAbilities                = 0x40
	PlayPlayerChat               = 0x41
	PlayPlayerCombatEnd          = 0x42
	PlayPlayerCombatEnter        = 0x43
	PlayPlayerCombatKill         = 0x44
	PlayPlayerInfoRemove         = 0x45
	PlayPlayerInfoUpdate         = 0x46
	PlayPlayerLookAt             = 0x47
	PlayPlayerPosition           = 0x48
	PlayPlayerRotation           = 0x49
	PlayRecipeBookAdd            = 0x4a
	PlayRecipeBookRemove         = 0x4b
	PlayRecipeBookSettings       = 0x4c
	PlayRemoveEntities           = 0x4d
	PlayRemoveMobEffect          = 0x4e
	PlayResetScore               = 0x4f
	PlayResourcePackPop          = 0x50
	PlayResourcePackPush         = 0x51
	PlayRespawn                  = 0x52
	PlayRotateHead               = 0x53
	PlaySectionBlocksUpdate      = 0x54
	PlaySelectAdvancementsTab    = 0x55
	PlayServerData               = 0x56
	PlaySetActionBarText         = 0x57
	PlaySetBorderCenter          = 0x58
	PlaySetBorderLerpSize        = 0x59
	PlaySetBorderSize            = 0x5a
	PlaySetBorderWarningDelay    = 0x5b
	PlaySetBorderWarningDistance = 0x5c
	PlaySetCamera                = 0x5d
	PlayChunkCacheCenter         = 0x5e
	PlayChunkCacheRadius         = 0x5f
	PlaySetCursorItem            = 0x60
	PlayDefaultSpawnPos          = 0x61
	PlaySetDisplayObjective      = 0x62
	PlaySetEntityData            = 0x63
	PlaySetEntityLink            = 0x64
	PlaySetEntityMotion          = 0x65
	PlaySetEquipment             = 0x66
	PlaySetExperience            = 0x67
	PlaySetHealth                = 0x68
	PlaySetHeldSlot              = 0x69
	PlaySetObjective             = 0x6a
	PlaySetPassengers            = 0x6b
	PlaySetPlayerInventory       = 0x6c
	PlaySetPlayerTeam            = 0x6d
	PlaySetScore                 = 0x6e
	PlaySetSimulationDistance    = 0x6f
	PlaySetSubtitleText          = 0x70
	PlaySetTime                  = 0x71
	PlaySetTitleText             = 0x72
	PlaySetTitlesAnimation       = 0x73
	PlaySoundEntity              = 0x74
	PlaySound                    = 0x75
	PlayStartConfiguration       = 0x76
	PlayStopSound                = 0x77
	PlayStoreCookie              = 0x78
	PlaySystemChat               = 0x79
	PlayTabList                  = 0x7a
	PlayTagQuery                 = 0x7b
	PlayTakeItemEntity           = 0x7c
	PlayTeleportEntity           = 0x7d
	PlayTestInstanceBlockStatus  = 0x7e
	PlayTickingState             = 0x7f
	PlayTickingStep              = 0x80
	PlayTransfer                 = 0x81
	PlayUpdateAdvancements       = 0x82
	PlayUpdateAttributes         = 0x83
	PlayUpdateMobEffect          = 0x84
	PlayUpdateRecipes            = 0x85
	PlayUpdateTags               = 0x86
	PlayProjectilePower          = 0x87
	PlayCustomReportDetails      = 0x88
	PlayServerLinks              = 0x89
	PlayWaypoint                 = 0x8a
	PlayClearDialog              = 0x8b
	PlayShowDialog               = 0x8c
)

// Play, serverbound (protocol 775).
const (
	PlayAcceptTeleport             = 0x00
	PlayAttack                     = 0x01
	PlayBlockEntityTagQuerySB      = 0x02
	PlayBundleItemSelectedSB       = 0x03
	PlayChangeDifficultySB         = 0x04
	PlayChangeGameModeSB           = 0x05
	PlayChatAckSB                  = 0x06
	PlayChatCommand                = 0x07
	PlayChatCommandSigned          = 0x08
	PlayChatMessage                = 0x09
	PlayChatSessionUpdateSB        = 0x0a
	PlayChunkBatchReceivedSB       = 0x0b
	PlayClientCommand              = 0x0c
	PlayClientTickEnd              = 0x0d
	PlayClientInformation          = 0x0e
	PlayCommandSuggestionSB        = 0x0f
	PlayConfigurationAcknowledged  = 0x10
	PlayContainerButtonClick       = 0x11
	PlayContainerClick             = 0x12
	PlayContainerCloseSB           = 0x13
	PlayContainerSlotStateChanged  = 0x14
	PlayCookieResponseSB           = 0x15
	PlayCustomPayload              = 0x16
	PlayDebugSubscriptionRequestSB = 0x17
	PlayEditBook                   = 0x18
	PlayEntityTagQuerySB           = 0x19
	PlayInteract                   = 0x1a
	PlayJigsawGenerateSB           = 0x1b
	PlayKeepAliveServer            = 0x1c
	PlayLockDifficultySB           = 0x1d
	PlayMovePos                    = 0x1e
	PlayMovePosRot                 = 0x1f
	PlayMoveRot                    = 0x20
	PlayMoveStatusOnly             = 0x21
	PlayMoveVehicleSB              = 0x22
	PlayPaddleBoatSB               = 0x23
	PlayPickItemFromBlockSB        = 0x24
	PlayPickItemFromEntitySB       = 0x25
	PlayPingRequestSB              = 0x26
	PlayPlaceRecipe                = 0x27
	PlayPlayerAbilitiesSB          = 0x28
	PlayPlayerAction               = 0x29
	PlayPlayerCommand              = 0x2a
	PlayPlayerInputSB              = 0x2b
	PlayPlayerLoaded               = 0x2c
	PlayPongSB                     = 0x2d
	PlayRecipeBookChangeSettings   = 0x2e
	PlayRecipeBookSeenRecipe       = 0x2f
	PlayRenameItem                 = 0x30
	PlayResourcePackSB             = 0x31
	PlaySeenAdvancements           = 0x32
	PlaySelectTrade                = 0x33
	PlaySetBeaconSB                = 0x34
	PlaySetCarriedItem             = 0x35
	PlaySetCommandBlockSB          = 0x36
	PlaySetCommandMinecartSB       = 0x37
	PlaySetCreativeSlot            = 0x38
	PlaySetGameRuleSB              = 0x39
	PlaySetJigsawBlockSB           = 0x3a
	PlaySetStructureBlockSB        = 0x3b
	PlaySetTestBlockSB             = 0x3c
	PlaySignUpdate                 = 0x3d
	PlaySpectateEntitySB           = 0x3e
	PlaySwingSB                    = 0x3f
	PlayTeleportToEntitySB         = 0x40
	PlayTestInstanceBlockActionSB  = 0x41
	PlayUseItemOn                  = 0x42
	PlayUseItem                    = 0x43
	PlayCustomClickActionSB        = 0x44
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
