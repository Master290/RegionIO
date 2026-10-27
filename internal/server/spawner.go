package server

import (
	"context"
	"encoding/binary"
	"log/slog"
	"math"
	"math/bits"
	"math/rand"
	"sync"
	"time"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

const (
	// maxEntityTrackingChunks caps entity tracking radius in chunks (4 chunks = 64 blocks,
	// matching vanilla's default mob tracking range).
	maxEntityTrackingChunks = 4
	fixedPointScale         = 4096.0

	// Light-level thresholds for mob spawning (vanilla 1.18+ / 26.1.2 rules).
	HostileMaxSkyLight   byte = 7
	PassiveMinLightLevel byte = 9
)

var serverTrackers sync.Map // map[*Server]*EntityTracker

func (s *Server) entityTracker() *EntityTracker {
	if tr, ok := serverTrackers.Load(s); ok {
		return tr.(*EntityTracker)
	}
	actual, _ := serverTrackers.LoadOrStore(s, NewEntityTracker())
	return actual.(*EntityTracker)
}

// StartSpawning begins the entity tick and spawn loops. log matches
// Cache.StartAutosave's convention: these are background loops that have to be
// able to report a failure nobody is waiting on.
func (s *Server) StartSpawning(ctx context.Context, log *slog.Logger) {
	_ = s.LoadAllMaps()
	_ = s.LoadScoreboardFile()
	_ = s.LoadBossBarsFile()
	_ = s.LoadSignsAndLecternsFile()
	_ = s.LoadBannersFile()
	_ = s.LoadDragonFightFile()
	go s.entityTickLoop(ctx, log)
	go s.mobSpawnLoop(ctx)
}

func (s *Server) entityTickLoop(ctx context.Context, log *slog.Logger) {
	ticker := time.NewTicker(50 * time.Millisecond) // 20 TPS
	defer ticker.Stop()
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	tracker := s.entityTracker()
	for {
		select {
		case <-ctx.Done():
			_ = s.SaveFurnaces()
			_ = s.SaveDecorationsFile()
			_ = s.SaveAllMaps()
			_ = s.SaveScoreboardFile()
			_ = s.SaveBossBarsFile()
			_ = s.SaveSignsAndLecternsFile()
			_ = s.SaveBannersFile()
			_ = s.SaveDragonFightFile()
			return
		case <-ticker.C:
			// The world clock, weather cycle, and sleep check ride this 20 TPS loop.
			if gameTime, _ := s.advanceWorldTime(); gameTime%timeSyncTicks == 0 {
				s.Broadcast(protocol.PlaySetTime, s.SetTimePacket())
				if gameTime%timePersistTicks == 0 {
					if err := s.SaveWorldTime(); err != nil && log != nil {
						log.Warn("saving world clock", "err", err)
					}
					_ = s.SaveDecorationsFile()
					_ = s.SaveAllMaps()
					_ = s.SaveScoreboardFile()
					_ = s.SaveBossBarsFile()
					_ = s.SaveSignsAndLecternsFile()
					_ = s.SaveBannersFile()
					_ = s.SaveDragonFightFile()
				}
			}
			s.TickWeather(rng)
			s.TrySkipNightBySleep()
			s.TickAllPlayersHunger()
			s.TickVillagers()
			s.TickMaps()
			tracker.TickAndBroadcast(s, rng)
		}
	}
}

// TickEntities advances entity physics/AI by one tick and broadcasts visibility
// and delta-movement updates to connected players.
func (s *Server) TickEntities(rng *rand.Rand) {
	s.entityTracker().TickAndBroadcast(s, rng)
}

// BroadcastEntities synchronizes entity spawns, delta movements, teleports, and
// batched despawns to all players within each entity's tracking range.
func (s *Server) BroadcastEntities() {
	s.entityTracker().Sync(s)
}

type trackedEntity struct {
	id                  int32
	baseX, baseY, baseZ int64
	yaw, pitch          byte
	onGround            bool
	seenTick            uint64
}

type viewerEntityState struct {
	tracked  []trackedEntity
	index    map[int32]int
	seenTick uint64
}

type viewerTarget struct {
	uuid    [16]byte
	x, y, z float64
	cx, cz  int32
	radius  int32
	send    PacketSender
}

// EntityTracker maintains per-viewer entity visibility and fixed-point base
// coordinates so movement updates use compact delta packets, despawns are
// batched, and steady-state ticks avoid heap allocations.
type EntityTracker struct {
	mu             sync.Mutex
	viewers        map[[16]byte]*viewerEntityState
	mobStates      map[int32]*mobMemory
	viewerBuf      []viewerTarget
	playerTargets  []playerTarget
	allEntitiesBuf []world.Entity
	visibleBuf     []world.Entity
	removeBuf      []int32
	pktBuf         []byte
	tickID         uint64
	aiTickID       uint64
}

// NewEntityTracker constructs an EntityTracker with preallocated scratch buffers.
func NewEntityTracker() *EntityTracker {
	return &EntityTracker{
		viewers:        make(map[[16]byte]*viewerEntityState),
		mobStates:      make(map[int32]*mobMemory),
		viewerBuf:      make([]viewerTarget, 0, 16),
		playerTargets:  make([]playerTarget, 0, 16),
		allEntitiesBuf: make([]world.Entity, 0, 64),
		visibleBuf:     make([]world.Entity, 0, 64),
		removeBuf:      make([]int32, 0, 32),
		pktBuf:         make([]byte, 0, 128),
	}
}

// TickAndBroadcast steps all entities in s and broadcasts resulting visibility
// and delta-movement packets.
func (t *EntityTracker) TickAndBroadcast(s *Server, rng *rand.Rand) {
	t.mu.Lock()
	t.stepEntitiesLocked(s, rng)
	t.mu.Unlock()

	if s != nil {
		s.TickWithers(rng)
		s.TickEnderDragons(rng)
		s.TickFireworkRockets()
		s.TickBabyMobs()
		s.TickTamedMobs(rng)
		s.TickIronGolems(rng)
		s.TickCopperGolems(rng)
		s.TickVillagerGolemSpawning(rng)
		s.TickPhantomSpawner(rng)
		s.TickPhantoms(rng)
		s.TickSniffers(rng)
		s.TickSnifferEggs(rng)
		s.TickArmadillos(rng)
		s.TickCamels(rng)
		s.TickBreezes(rng)
		s.TickWindCharges()
		s.TickAllays(rng)
		s.TickFrogs(rng)
		s.TickTadpoles(rng)
		s.TickAxolotls(rng)
		s.TickGoats(rng)
		s.TickEquinesAndLlamas(rng)
		s.TickLlamaSpits()
		s.TickBees(rng)
		s.TickBeehives(rng)
		s.TickFoxesPandasAndOcelots(rng)
		s.TickPiglinsHoglinsAndStriders(rng)
		s.TickAquaticMobs(rng)
		s.TickIllagersWitchesRavagersAndVexes(rng)
		s.TickMobsInCauldrons()
	}

	t.mu.Lock()
	t.syncLocked(s)
	t.mu.Unlock()
}

// Sync performs one visibility and movement broadcast pass across all players.
func (t *EntityTracker) Sync(s *Server) {
	t.mu.Lock()
	t.syncLocked(s)
	t.mu.Unlock()
}

func (t *EntityTracker) stepEntitiesLocked(s *Server, rng *rand.Rand) {
	t.aiTickID++
	aiTick := t.aiTickID
	t.playerTargets = s.appendPlayerTargets(t.playerTargets[:0])
	t.allEntitiesBuf = s.entities.AppendAll(t.allEntitiesBuf[:0])

	for i := range t.allEntitiesBuf {
		snapshot := t.allEntitiesBuf[i]
		yBelow := int(math.Floor(snapshot.Y - 0.1))
		blockBelow := s.chunks.GetBlock(int(math.Floor(snapshot.X)), yBelow, int(math.Floor(snapshot.Z)))

		if snapshot.IsItem() {
			dim := snapshot.EffectiveDimension()
			cache := s.DimensionChunks(dim)
			bx, by, bz := int(math.Floor(snapshot.X)), int(math.Floor(snapshot.Y)), int(math.Floor(snapshot.Z))
			if cache != nil {
				curVX := float64(snapshot.VelocityX) / 8000.0
				curVY := float64(snapshot.VelocityY) / 8000.0
				curVZ := float64(snapshot.VelocityZ) / 8000.0
				if nvx, nvy, nvz, inBC := cache.BubbleColumnVelocityAt(bx, by, bz, curVX, curVY, curVZ); inBC {
					s.entities.Update(snapshot.ID, func(e *world.Entity) {
						e.OnGround = false
						e.VelocityX = clampVelocityShort(nvx)
						e.VelocityY = clampVelocityShort(nvy)
						e.VelocityZ = clampVelocityShort(nvz)
						e.Y += nvy
						if e.PickupDelay > 0 {
							e.PickupDelay--
						}
						e.Age++
					})
					continue
				}
			}
			s.entities.Update(snapshot.ID, func(e *world.Entity) {
				if isPassableBlock(blockBelow) || blockBelow == world.StateWater {
					e.OnGround = false
					if cache != nil && cache.FluidHeightAt(bx, by, bz) > 0 {
						// Items float upward gently when submerged in water.
						e.VelocityY += 40
						if e.VelocityY > 480 {
							e.VelocityY = 480
						}
					} else {
						e.VelocityY -= 80
						if e.VelocityY < -3000 {
							e.VelocityY = -3000
						}
					}
					e.Y += float64(e.VelocityY) / 8000.0
				} else {
					e.OnGround = true
					e.VelocityY = 0
					e.Y = float64(yBelow + 1)
				}
				if e.PickupDelay > 0 {
					e.PickupDelay--
				}
				e.Age++
			})
			continue
		}
		if snapshot.TypeName == EntityTypeFishingBobber || snapshot.IsExperienceOrb() || IsDecorationEntity(snapshot.TypeName) || IsWindChargeEntityType(snapshot.TypeName) || snapshot.TypeName == EntityTypeLlamaSpit || snapshot.TypeName == EntityTypeLightning || snapshot.TypeName == EntityTypeEvokerFangs {
			continue
		}
		if IsStandardProjectileEntityType(snapshot.TypeName) {
			if snapshot.VelocityX != 0 || snapshot.VelocityY != 0 || snapshot.VelocityZ != 0 {
				vx := float64(snapshot.VelocityX) / 8000.0
				vy := float64(snapshot.VelocityY) / 8000.0
				vz := float64(snapshot.VelocityZ) / 8000.0
				nx, ny, nz := snapshot.X+vx, snapshot.Y+vy, snapshot.Z+vz
				dim := snapshot.EffectiveDimension()
				if cache := s.DimensionChunks(dim); cache != nil {
					bx, by, bz := int(math.Floor(nx)), int(math.Floor(ny)), int(math.Floor(nz))
					if by >= world.MinY && by < world.MinY+world.WorldHeight {
						blk := cache.GetBlock(bx, by, bz)
						if (!isPassableBlock(blk) && blk != world.StateWater) || world.IsWoodenButtonBlock(blk) || world.IsBellBlock(blk) || world.IsChorusFlowerBlock(blk) {
							_, _ = s.OnProjectileBlockImpact(dim, bx, by, bz, snapshot.X, snapshot.Y, snapshot.Z, nx, ny, nz, snapshot.ID, snapshot.TypeName)
							s.entities.Update(snapshot.ID, func(e *world.Entity) {
								e.X, e.Y, e.Z = nx, ny, nz
								e.VelocityX, e.VelocityY, e.VelocityZ = 0, 0, 0
								e.OnGround = true
							})
							continue
						}
					}
				}
				vy = (vy - 0.05) * 0.99
				vx *= 0.99
				vz *= 0.99
				s.entities.Update(snapshot.ID, func(e *world.Entity) {
					e.X, e.Y, e.Z = nx, ny, nz
					e.VelocityX = clampVelocityShort(vx)
					e.VelocityY = clampVelocityShort(vy)
					e.VelocityZ = clampVelocityShort(vz)
				})
			}
			continue
		}
		if IsBossOrBossSupportEntity(snapshot.TypeName) {
			if snapshot.TypeName == MobTypeWither || snapshot.TypeName == MobTypeEnderDragon {
				_ = t.mobStateFor(snapshot.ID, aiTick)
			}
			continue
		}
		if snapshot.TypeName == MobTypePhantom || snapshot.TypeName == MobTypeBreeze || snapshot.TypeName == MobTypeAllay || snapshot.TypeName == MobTypeBee || snapshot.TypeName == MobTypeVex {
			mem := t.mobStateFor(snapshot.ID, aiTick)
			if !mem.healthInit {
				mem.healthInit = true
				mem.health = ResolveMobMaxHealth(snapshot.TypeName)
				mem.lastHealth = mem.health
			}
			if mem.breedCooldown > 0 {
				mem.breedCooldown--
			}
			if mem.inLoveTicks > 0 {
				mem.inLoveTicks--
			}
			continue
		}

		mem := t.mobStateFor(snapshot.ID, aiTick)
		if !mem.healthInit {
			mem.healthInit = true
			if IsEquineOrLlamaMob(snapshot.TypeName) {
				mem.health = s.EquineMaxHealth(snapshot.ID)
			} else {
				mem.health = ResolveMobMaxHealth(snapshot.TypeName)
			}
			mem.lastHealth = mem.health
		} else if mem.health < mem.lastHealth {
			mem.lastHealth = mem.health
			if snapshot.TypeName == MobTypeSpider {
				mem.provoked = true
			} else if IsPassiveAnimal(snapshot.TypeName) || snapshot.TypeName == MobTypeArmadillo || IsEquineOrLlamaMob(snapshot.TypeName) {
				mem.panicTicks = AnimalPanicDurationTicks
			}
		}
		if s.EntityMountedVehicle(snapshot.ID) > 0 {
			continue
		}
		if isPassableBlock(blockBelow) || blockBelow == world.StateWater {
			if snapshot.TypeName == MobTypeStrider && world.BlockName(blockBelow) == "minecraft:lava" {
				// Striders walk on top of lava without sinking!
				s.entities.Update(snapshot.ID, func(e *world.Entity) {
					e.OnGround = true
					e.VelocityY = 0
					e.Y = float64(yBelow + 1)
				})
			} else {
				if mem.attackCooldown > 0 {
					mem.attackCooldown--
				}
				dim := snapshot.EffectiveDimension()
				if cache := s.DimensionChunks(dim); cache != nil {
					bx, by, bz := int(math.Floor(snapshot.X)), int(math.Floor(snapshot.Y)), int(math.Floor(snapshot.Z))
					curVX := float64(snapshot.VelocityX) / 8000.0
					curVY := float64(snapshot.VelocityY) / 8000.0
					curVZ := float64(snapshot.VelocityZ) / 8000.0
					if nvx, nvy, nvz, inBC := cache.BubbleColumnVelocityAt(bx, by, bz, curVX, curVY, curVZ); inBC {
						s.entities.Update(snapshot.ID, func(e *world.Entity) {
							e.OnGround = false
							e.VelocityX = clampVelocityShort(nvx)
							e.VelocityY = clampVelocityShort(nvy)
							e.VelocityZ = clampVelocityShort(nvz)
							e.Y += nvy
						})
						continue
					}
				}
				minFallVel := int16(-3000)
				if snapshot.TypeName == MobTypeChicken {
					minFallVel = ChickenMaxFallVelocity
				}
				s.entities.Update(snapshot.ID, func(e *world.Entity) {
					e.OnGround = false
					e.VelocityY -= 80
					if e.VelocityY < minFallVel {
						e.VelocityY = minFallVel
					}
					e.Y += float64(e.VelocityY) / 8000.0
				})
				continue
			}
		}

		feetY := yBelow + 1
		var (
			nextX        = snapshot.X
			nextY        = float64(feetY)
			nextZ        = snapshot.Z
			nextYaw      = snapshot.Yaw
			attackTarget *PlayerSession
			shootTarget  *PlayerSession
			attackDmg    = ZombieAttackDamage
			swellChanged bool
			swellDir     int8
			exploded     bool
			leaped       bool
		)
		switch snapshot.TypeName {
		case MobTypeZombie:
			nextX, nextY, nextZ, nextYaw, attackTarget = stepHostileZombieAI(
				s.chunks, rng, mem, t.playerTargets,
				snapshot.X, feetY, snapshot.Z, snapshot.Yaw,
			)
			attackDmg = ZombieAttackDamage
		case MobTypeSkeleton:
			nextX, nextY, nextZ, nextYaw, shootTarget = stepHostileSkeletonAI(
				s.chunks, rng, mem, t.playerTargets,
				snapshot.X, feetY, snapshot.Z, snapshot.Yaw,
			)
		case MobTypeBogged:
			nextX, nextY, nextZ, nextYaw, shootTarget = s.stepHostileBoggedAI(
				s.chunks, rng, mem, t.playerTargets,
				snapshot.X, feetY, snapshot.Z, snapshot.Yaw,
			)
		case MobTypeCreeper:
			if _, fx, _, fz, ok := s.HasCatOrOcelotNearPosition(snapshot.EffectiveDimension(), snapshot.X, float64(feetY), snapshot.Z, CatCreeperRepelRange); ok {
				if mem.creeperFuseTicks > 0 || mem.creeperSwellDir != 0 {
					mem.creeperFuseTicks = 0
					mem.creeperSwellDir = 0
					swellChanged = true
					swellDir = 0
				}
				awayX := snapshot.X - fx
				awayZ := snapshot.Z - fz
				dist := math.Hypot(awayX, awayZ)
				if dist < 1e-4 {
					awayX = 1.0
					dist = 1.0
				}
				tx := snapshot.X + (awayX/dist)*CreeperChaseSpeed
				tz := snapshot.Z + (awayZ/dist)*CreeperChaseSpeed
				nextYaw = yawToward(awayX, awayZ)
				if s.chunks != nil {
					if nx, ny, nz, stepOK := tryApplyHorizontalStep(s.chunks, snapshot.X, feetY, snapshot.Z, tx, tz); stepOK {
						nextX, nextY, nextZ = nx, float64(ny), nz
					}
				} else {
					nextX, nextZ = tx, tz
				}
			} else {
				nextX, nextY, nextZ, nextYaw, swellChanged, exploded = stepHostileCreeperAI(
					s.chunks, rng, mem, t.playerTargets,
					snapshot.X, feetY, snapshot.Z, snapshot.Yaw,
				)
				swellDir = mem.creeperSwellDir
			}
		case MobTypeSpider:
			nextX, nextY, nextZ, nextYaw, attackTarget, leaped = s.stepHostileSpiderAI(
				s.chunks, rng, mem, t.playerTargets,
				snapshot.X, feetY, snapshot.Z, snapshot.Yaw,
			)
			attackDmg = SpiderAttackDamage
		default:
			if snapshot.TypeName == MobTypeArmadillo && s.IsArmadilloRolledUp(snapshot.ID) {
				if mem.breedCooldown > 0 {
					mem.breedCooldown--
				}
				if mem.inLoveTicks > 0 {
					mem.inLoveTicks--
				}
			} else if snapshot.TypeName == MobTypeCamel && (s.IsCamelSitting(snapshot.ID) || len(s.CamelPassengers(snapshot.ID)) > 0) {
				if mem.breedCooldown > 0 {
					mem.breedCooldown--
				}
				if mem.inLoveTicks > 0 {
					mem.inLoveTicks--
				}
			} else if snapshot.TypeName == MobTypeSniffer {
				if sst, ok := s.GetSnifferState(snapshot.ID); ok && sst.State != SnifferStateIdling {
					if mem.breedCooldown > 0 {
						mem.breedCooldown--
					}
					if mem.inLoveTicks > 0 {
						mem.inLoveTicks--
					}
				} else {
					nextX, nextY, nextZ, nextYaw = s.stepPassiveAnimalAI(
						snapshot.ID, snapshot.TypeName, s.chunks, rng, mem, t.playerTargets,
						snapshot.X, feetY, snapshot.Z, snapshot.Yaw,
					)
				}
			} else if IsTameableMob(snapshot.TypeName) && (s.IsMobSitting(snapshot.ID) || s.IsMobTamed(snapshot.ID)) {
				if mem.breedCooldown > 0 {
					mem.breedCooldown--
				}
				if mem.inLoveTicks > 0 {
					mem.inLoveTicks--
				}
			} else if snapshot.TypeName == MobTypeFox && s.IsFoxSleeping(snapshot.ID) {
				if mem.breedCooldown > 0 {
					mem.breedCooldown--
				}
				if mem.inLoveTicks > 0 {
					mem.inLoveTicks--
				}
			} else if snapshot.TypeName == MobTypePanda {
				if pst, ok := s.GetPandaState(snapshot.ID); ok && (pst.Flags&(PandaFlagSitting|PandaFlagRolling|PandaFlagOnBack) != 0 || pst.AngerTicks > 0) {
					if mem.breedCooldown > 0 {
						mem.breedCooldown--
					}
					if mem.inLoveTicks > 0 {
						mem.inLoveTicks--
					}
				} else {
					nextX, nextY, nextZ, nextYaw = s.stepPassiveAnimalAI(
						snapshot.ID, snapshot.TypeName, s.chunks, rng, mem, t.playerTargets,
						snapshot.X, feetY, snapshot.Z, snapshot.Yaw,
					)
				}
			} else if snapshot.TypeName == MobTypePiglin || snapshot.TypeName == MobTypePiglinBrute || snapshot.TypeName == MobTypeHoglin || snapshot.TypeName == MobTypeZoglin {
				if mem.breedCooldown > 0 {
					mem.breedCooldown--
				}
				if mem.inLoveTicks > 0 {
					mem.inLoveTicks--
				}
			} else if snapshot.TypeName == MobTypeIronGolem {
				if gst, ok := s.GetIronGolemState(snapshot.ID); !ok || gst.TargetMobID == 0 {
					nextX, nextY, nextZ, nextYaw = s.stepPassiveAnimalAI(
						snapshot.ID, snapshot.TypeName, s.chunks, rng, mem, t.playerTargets,
						snapshot.X, feetY, snapshot.Z, snapshot.Yaw,
					)
				}
			} else {
				nextX, nextY, nextZ, nextYaw = s.stepPassiveAnimalAI(
					snapshot.ID, snapshot.TypeName, s.chunks, rng, mem, t.playerTargets,
					snapshot.X, feetY, snapshot.Z, snapshot.Yaw,
				)
			}
		}

		if exploded {
			delete(t.mobStates, snapshot.ID)
			s.TriggerCreeperExplosion(snapshot.ID, nextX, nextY, nextZ)
			continue
		}

		s.entities.Update(snapshot.ID, func(e *world.Entity) {
			e.OnGround = !leaped
			if leaped {
				e.VelocityY = 2400
			} else {
				e.VelocityY = 0
			}
			e.X = nextX
			e.Y = nextY
			e.Z = nextZ
			e.Yaw = nextYaw
			e.HeadYaw = nextYaw
		})
		if s.chunks != nil {
			tbx, tby, tbz := int(math.Floor(nextX)), int(math.Floor(nextY)), int(math.Floor(nextZ))
			if tby >= world.MinY && tby < world.MinY+world.WorldHeight && world.BlockName(s.chunks.GetBlock(tbx, tby, tbz)) == "minecraft:tripwire" {
				s.OnEntityStepTripwire(snapshot.EffectiveDimension(), tbx, tby, tbz)
			}
		}

		if swellChanged {
			cx := int32(int64(math.Floor(nextX)) >> 4)
			cz := int32(int64(math.Floor(nextZ)) >> 4)
			s.BroadcastChunk(cx, cz, PlaySetEntityDataCB,
				EncodeCreeperSwellDataPacket(snapshot.ID, int32(swellDir), swellDir > 0))
			if swellDir > 0 {
				s.BroadcastChunk(cx, cz, PlaySoundPacketID,
					EncodeSoundPacket("minecraft:entity.creeper.primed", SoundSourceHostile, nextX, nextY, nextZ, 1.0, 0.5, 0))
			}
		}

		if shootTarget != nil && !shootTarget.IsDead() {
			if snapshot.TypeName == MobTypeBogged {
				s.ShootBoggedArrowAtPlayer(snapshot.ID, nextX, nextY, nextZ, shootTarget)
			} else {
				s.ShootSkeletonArrowAtPlayer(snapshot.ID, nextX, nextY, nextZ, shootTarget)
			}
		}
		if attackTarget != nil && !attackTarget.IsDead() {
			s.AttackPlayerFromMob(snapshot.ID, snapshot.TypeName, nextX, nextZ, attackTarget, attackDmg)
		}
	}

	t.pruneMobStates(aiTick)
	s.TickFishingBobbers(rng)
	s.TickDecorations()
}

func (s *Server) appendViewerTargets(dst []viewerTarget) []viewerTarget {
	maxView := s.cfg.MaxViewDistance
	if maxView < 2 {
		maxView = 2
	}
	s.playersMu.RLock()
	for _, session := range s.players {
		if session == nil || !session.IsJoined() {
			continue
		}
		session.mu.RLock()
		pos := session.position
		vd := session.viewDist
		send := session.send
		uuid := session.Profile.UUID
		session.mu.RUnlock()

		radius := vd
		if radius < 2 {
			radius = 2
		}
		if radius > maxView {
			radius = maxView
		}
		if radius > maxEntityTrackingChunks {
			radius = maxEntityTrackingChunks
		}
		dst = append(dst, viewerTarget{
			uuid:   uuid,
			x:      pos[0],
			y:      pos[1],
			z:      pos[2],
			cx:     int32(int64(math.Floor(pos[0])) >> 4),
			cz:     int32(int64(math.Floor(pos[2])) >> 4),
			radius: int32(radius),
			send:   send,
		})
	}
	s.playersMu.RUnlock()
	return dst
}

func (t *EntityTracker) syncLocked(s *Server) {
	t.tickID++
	tickID := t.tickID
	t.viewerBuf = s.appendViewerTargets(t.viewerBuf[:0])

	for i := range t.viewerBuf {
		viewer := &t.viewerBuf[i]
		state := t.viewers[viewer.uuid]
		if state == nil {
			state = &viewerEntityState{
				tracked: make([]trackedEntity, 0, 16),
				index:   make(map[int32]int, 16),
			}
			t.viewers[viewer.uuid] = state
		}
		state.seenTick = tickID

		// Query only the chunks within the viewer's tracking radius.
		t.visibleBuf = s.entities.AppendInChunkRange(t.visibleBuf[:0], viewer.cx, viewer.cz, viewer.radius)

		for j := range t.visibleBuf {
			ent := &t.visibleBuf[j]
			curX := encodeFixedCoord(ent.X)
			curY := encodeFixedCoord(ent.Y)
			curZ := encodeFixedCoord(ent.Z)
			curYaw := encodeAngleByte(ent.Yaw)
			curPitch := encodeAngleByte(ent.Pitch)

			idx, exists := state.index[ent.ID]
			if !exists {
				state.index[ent.ID] = len(state.tracked)
				state.tracked = append(state.tracked, trackedEntity{
					id:       ent.ID,
					baseX:    curX,
					baseY:    curY,
					baseZ:    curZ,
					yaw:      curYaw,
					pitch:    curPitch,
					onGround: ent.OnGround,
					seenTick: tickID,
				})
				if viewer.send != nil {
					if ent.TypeName == EntityTypeFishingBobber {
						t.pktBuf = s.EncodeFishingBobberAddEntityPacket(t.pktBuf[:0], ent)
					} else if IsDecorationEntity(ent.TypeName) {
						t.pktBuf = s.EncodeDecorationAddEntityPacket(t.pktBuf[:0], ent)
					} else {
						t.pktBuf = encodeAddEntityPacket(t.pktBuf[:0], ent)
					}
					_ = viewer.send(protocol.PlayAddEntity, t.pktBuf)
					if ent.TypeName == MobTypeVillager {
						_ = viewer.send(PlaySetEntityDataCB, s.VillagerEntityDataPacket(ent.ID))
					} else if ent.TypeName == EntityTypeFishingBobber {
						if metaPkt := s.FishingBobberEntityDataPacket(ent.ID); len(metaPkt) > 0 {
							_ = viewer.send(PlaySetEntityDataCB, metaPkt)
						}
					} else if IsDecorationEntity(ent.TypeName) {
						if metaPkt := s.DecorationEntityDataPacket(ent.ID); len(metaPkt) > 0 {
							_ = viewer.send(PlaySetEntityDataCB, metaPkt)
						}
						if ent.TypeName == EntityTypeArmorStand {
							if eqPkt := s.ArmorStandEquipmentPacket(ent.ID); len(eqPkt) > 0 {
								_ = viewer.send(protocol.PlaySetEquipment, eqPkt)
							}
						}
					} else if IsBossOrBossSupportEntity(ent.TypeName) {
						if metaPkt := s.BossOrSupportEntityDataPacket(ent.ID, ent.TypeName); len(metaPkt) > 0 {
							_ = viewer.send(PlaySetEntityDataCB, metaPkt)
						}
					} else if IsTameableMob(ent.TypeName) {
						if metaPkt := s.TamedMobEntityDataPacket(ent.ID, ent.TypeName); len(metaPkt) > 0 {
							_ = viewer.send(PlaySetEntityDataCB, metaPkt)
						}
						if eqPkt := s.EquineBodyEquipmentPacket(ent.ID); len(eqPkt) > 0 {
							_ = viewer.send(protocol.PlaySetEquipment, eqPkt)
						}
					} else if ent.TypeName == MobTypeIronGolem {
						_ = viewer.send(PlaySetEntityDataCB, EncodeIronGolemDataPacket(ent.ID, s.IsIronGolemPlayerCreated(ent.ID)))
					}
					if mem := t.mobStates[ent.ID]; mem != nil && mem.ageTicks < 0 {
						_ = viewer.send(PlaySetEntityDataCB, EncodeAgeableMobDataPacket(ent.ID, true))
					}
				}
				continue
			}

			tr := &state.tracked[idx]
			tr.seenTick = tickID
			dx := curX - tr.baseX
			dy := curY - tr.baseY
			dz := curZ - tr.baseZ
			moved := dx != 0 || dy != 0 || dz != 0
			rotated := curYaw != tr.yaw || curPitch != tr.pitch
			groundChanged := ent.OnGround != tr.onGround

			if !moved && !rotated && !groundChanged {
				continue
			}

			if dx < math.MinInt16 || dx > math.MaxInt16 ||
				dy < math.MinInt16 || dy > math.MaxInt16 ||
				dz < math.MinInt16 || dz > math.MaxInt16 {
				tr.baseX = curX
				tr.baseY = curY
				tr.baseZ = curZ
				tr.yaw = curYaw
				tr.pitch = curPitch
				tr.onGround = ent.OnGround
				if viewer.send != nil {
					t.pktBuf = encodeTeleportEntityPacket(t.pktBuf[:0], ent)
					_ = viewer.send(protocol.PlayTeleportEntity, t.pktBuf)
				}
				continue
			}

			if moved && rotated {
				tr.baseX = curX
				tr.baseY = curY
				tr.baseZ = curZ
				tr.yaw = curYaw
				tr.pitch = curPitch
				tr.onGround = ent.OnGround
				if viewer.send != nil {
					t.pktBuf = encodeMovePosRotPacket(t.pktBuf[:0], ent.ID, int16(dx), int16(dy), int16(dz), curYaw, curPitch, ent.OnGround)
					_ = viewer.send(protocol.PlayMoveEntityPosRot, t.pktBuf)
				}
			} else if moved {
				tr.baseX = curX
				tr.baseY = curY
				tr.baseZ = curZ
				tr.onGround = ent.OnGround
				if viewer.send != nil {
					t.pktBuf = encodeMovePosPacket(t.pktBuf[:0], ent.ID, int16(dx), int16(dy), int16(dz), ent.OnGround)
					_ = viewer.send(protocol.PlayMoveEntityPos, t.pktBuf)
				}
			} else {
				tr.yaw = curYaw
				tr.pitch = curPitch
				tr.onGround = ent.OnGround
				if viewer.send != nil {
					t.pktBuf = encodeMoveRotPacket(t.pktBuf[:0], ent.ID, curYaw, curPitch, ent.OnGround)
					_ = viewer.send(protocol.PlayMoveEntityRot, t.pktBuf)
				}
			}
		}

		// Collect all entities that left this viewer's range or were despawned,
		// and send a single batched PlayRemoveEntities packet.
		t.removeBuf = t.removeBuf[:0]
		for k := 0; k < len(state.tracked); {
			if state.tracked[k].seenTick == tickID {
				k++
				continue
			}
			removedID := state.tracked[k].id
			t.removeBuf = append(t.removeBuf, removedID)
			delete(state.index, removedID)
			last := len(state.tracked) - 1
			if k != last {
				movedEntry := state.tracked[last]
				state.tracked[k] = movedEntry
				state.index[movedEntry.id] = k
			}
			state.tracked = state.tracked[:last]
		}
		if len(t.removeBuf) > 0 && viewer.send != nil {
			t.pktBuf = encodeRemoveEntitiesPacket(t.pktBuf[:0], t.removeBuf)
			_ = viewer.send(protocol.PlayRemoveEntities, t.pktBuf)
		}
	}

	// Prune disconnected viewers so tracking state does not leak.
	for uuid, state := range t.viewers {
		if state.seenTick != tickID {
			delete(t.viewers, uuid)
		}
	}
}

func encodeFixedCoord(v float64) int64 {
	return int64(math.Round(v * fixedPointScale))
}

func encodeAngleByte(deg float32) byte {
	return byte(deg * 256.0 / 360.0)
}

func appendBoolByte(dst []byte, v bool) []byte {
	if v {
		return append(dst, 1)
	}
	return append(dst, 0)
}

func encodeAddEntityPacket(dst []byte, e *world.Entity) []byte {
	dst = protocol.AppendVarInt(dst, e.ID)
	dst = append(dst, e.UUID[:]...)
	dst = protocol.AppendVarInt(dst, int32(e.TypeID))
	dst = binary.BigEndian.AppendUint64(dst, math.Float64bits(e.X))
	dst = binary.BigEndian.AppendUint64(dst, math.Float64bits(e.Y))
	dst = binary.BigEndian.AppendUint64(dst, math.Float64bits(e.Z))
	if e.VelocityX == 0 && e.VelocityY == 0 && e.VelocityZ == 0 {
		dst = append(dst, 0)
	} else {
		var w protocol.Writer
		w.LPVec3(
			float64(e.VelocityX)/8000.0,
			float64(e.VelocityY)/8000.0,
			float64(e.VelocityZ)/8000.0,
		)
		dst = append(dst, w.Bytes()...)
	}
	dst = append(dst,
		encodeAngleByte(e.Pitch),
		encodeAngleByte(e.Yaw),
		encodeAngleByte(e.HeadYaw),
	)
	dst = protocol.AppendVarInt(dst, 0)
	return dst
}

func encodeMovePosPacket(dst []byte, id int32, dx, dy, dz int16, onGround bool) []byte {
	dst = protocol.AppendVarInt(dst, id)
	dst = binary.BigEndian.AppendUint16(dst, uint16(dx))
	dst = binary.BigEndian.AppendUint16(dst, uint16(dy))
	dst = binary.BigEndian.AppendUint16(dst, uint16(dz))
	return appendBoolByte(dst, onGround)
}

func encodeMovePosRotPacket(dst []byte, id int32, dx, dy, dz int16, yaw, pitch byte, onGround bool) []byte {
	dst = protocol.AppendVarInt(dst, id)
	dst = binary.BigEndian.AppendUint16(dst, uint16(dx))
	dst = binary.BigEndian.AppendUint16(dst, uint16(dy))
	dst = binary.BigEndian.AppendUint16(dst, uint16(dz))
	dst = append(dst, yaw, pitch)
	return appendBoolByte(dst, onGround)
}

func encodeMoveRotPacket(dst []byte, id int32, yaw, pitch byte, onGround bool) []byte {
	dst = protocol.AppendVarInt(dst, id)
	dst = append(dst, yaw, pitch)
	return appendBoolByte(dst, onGround)
}

func encodeTeleportEntityPacket(dst []byte, e *world.Entity) []byte {
	dst = protocol.AppendVarInt(dst, e.ID)
	dst = binary.BigEndian.AppendUint64(dst, math.Float64bits(e.X))
	dst = binary.BigEndian.AppendUint64(dst, math.Float64bits(e.Y))
	dst = binary.BigEndian.AppendUint64(dst, math.Float64bits(e.Z))
	dst = binary.BigEndian.AppendUint64(dst, math.Float64bits(float64(e.VelocityX)/8000.0))
	dst = binary.BigEndian.AppendUint64(dst, math.Float64bits(float64(e.VelocityY)/8000.0))
	dst = binary.BigEndian.AppendUint64(dst, math.Float64bits(float64(e.VelocityZ)/8000.0))
	dst = binary.BigEndian.AppendUint32(dst, math.Float32bits(e.Yaw))
	dst = binary.BigEndian.AppendUint32(dst, math.Float32bits(e.Pitch))
	dst = binary.BigEndian.AppendUint32(dst, 0)
	return appendBoolByte(dst, e.OnGround)
}

func encodeRemoveEntitiesPacket(dst []byte, ids []int32) []byte {
	dst = protocol.AppendVarInt(dst, int32(len(ids)))
	for _, id := range ids {
		dst = protocol.AppendVarInt(dst, id)
	}
	return dst
}

func (s *Server) mobSpawnLoop(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if s.PlayerCount() == 0 || s.entities.Count() >= 20 {
				continue
			}
			s.spawnRandomMobNearPlayer(rng)
		}
	}
}

// LightAt returns the stored sky light (0..15) and block light (0..15) at world
// block coordinates (x, y, z).
func (s *Server) LightAt(x, y, z int) (skyLight, blockLight byte) {
	if s == nil || s.chunks == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return 0, 0
	}
	cx := int32(x >> 4)
	cz := int32(z >> 4)
	raw, err := s.chunks.LightUpdate(cx, cz)
	if err != nil {
		return 0, 0
	}
	sky, block, ok := decodeLightFromUpdate(raw, x, y, z)
	if !ok {
		return 0, 0
	}
	return sky, block
}

// EffectiveLightAt returns the raw sky light, raw block light, and the effective
// internal light level at (x, y, z) after applying time-of-day and weather sky
// darkening.
func (s *Server) EffectiveLightAt(x, y, z int) (skyLight, blockLight, internalLight byte) {
	skyLight, blockLight = s.LightAt(x, y, z)
	darken := s.SkyDarkening()
	var skyEffective byte
	if skyLight > darken {
		skyEffective = skyLight - darken
	}
	internalLight = skyEffective
	if blockLight > internalLight {
		internalLight = blockLight
	}
	return skyLight, blockLight, internalLight
}

// CanSpawnHostileMobAt reports whether a hostile mob (e.g. zombie) can spawn
// with its feet at (x, y, z): requires a solid supporting floor at (x, y-1, z),
// two passable blocks at (y, y+1), zero artificial block light (blockLight == 0),
// and darkness (skyLight <= 7 or night/thunderstorm).
func (s *Server) CanSpawnHostileMobAt(x, y, z int) bool {
	if s == nil || s.chunks == nil || y <= world.MinY || y >= world.MinY+world.WorldHeight-1 {
		return false
	}
	floor := s.chunks.GetBlock(x, y-1, z)
	if !isSolidGroundBlock(floor) || !isColumnPassableAt(s.chunks, x, y, z) {
		return false
	}
	skyLight, blockLight := s.LightAt(x, y, z)
	if blockLight > 0 {
		return false
	}
	darken := s.SkyDarkening()
	var skyEffective byte
	if skyLight > darken {
		skyEffective = skyLight - darken
	}
	return skyEffective <= HostileMaxSkyLight
}

// CanSpawnPassiveMobAt reports whether a passive animal (e.g. pig) can spawn
// with its feet at (x, y, z): requires a grass_block floor at (x, y-1, z), two
// passable blocks at (y, y+1), and effective light >= PassiveMinLightLevel (9).
func (s *Server) CanSpawnPassiveMobAt(x, y, z int) bool {
	if s == nil || s.chunks == nil || y <= world.MinY || y >= world.MinY+world.WorldHeight-1 {
		return false
	}
	floor := s.chunks.GetBlock(x, y-1, z)
	if floor != world.StateGrass || !isColumnPassableAt(s.chunks, x, y, z) {
		return false
	}
	_, _, internalLight := s.EffectiveLightAt(x, y, z)
	return internalLight >= PassiveMinLightLevel
}

func (s *Server) resolveSpawnColumn(x, z int) (spawnY int, canPig, canZombie bool) {
	topY, ok := s.chunks.SafeSpawnY(x, z)
	if !ok {
		return 0, false, false
	}
	canPig = s.CanSpawnPassiveMobAt(x, topY, z)
	canZombie = s.CanSpawnHostileMobAt(x, topY, z)
	if canPig || canZombie {
		return topY, canPig, canZombie
	}
	// If the top surface is unsuitable (e.g. a sunlit stone roof above an
	// enclosed dark chamber), scan below for a valid interior spawn floor.
	for y := topY - 2; y > world.MinY; y-- {
		if !isSolidGroundBlock(s.chunks.GetBlock(x, y-1, z)) {
			continue
		}
		if !isColumnPassableAt(s.chunks, x, y, z) {
			continue
		}
		cp := s.CanSpawnPassiveMobAt(x, y, z)
		cz := s.CanSpawnHostileMobAt(x, y, z)
		if cp || cz {
			return y, cp, cz
		}
	}
	return 0, false, false
}

func (s *Server) spawnMobNearPlayer(rng *rand.Rand, pigType, zombieType int) bool {
	players := s.PlayerSnapshots()
	if len(players) == 0 {
		return false
	}
	player := players[rng.Intn(len(players))]
	for attempt := 0; attempt < 8; attempt++ {
		angle := rng.Float64() * 2 * math.Pi
		distance := 16.0 + rng.Float64()*16.0
		x := int(math.Floor(player.X + math.Cos(angle)*distance))
		z := int(math.Floor(player.Z + math.Sin(angle)*distance))
		y, canPig, canZombie := s.resolveSpawnColumn(x, z)
		if !canPig && !canZombie {
			continue
		}

		typeID := pigType
		typeName := MobTypePig
		if canZombie && canPig {
			if rng.Float32() < 0.5 {
				typeID = zombieType
				typeName = MobTypeZombie
			}
		} else if canZombie {
			typeID = zombieType
			typeName = MobTypeZombie
		}

		s.entities.Add(&world.Entity{
			TypeID:   typeID,
			TypeName: typeName,
			X:        float64(x) + 0.5,
			Y:        float64(y),
			Z:        float64(z) + 0.5,
			OnGround: true,
		})
		return true
	}
	return false
}

func (s *Server) spawnRandomMobNearPlayer(rng *rand.Rand) bool {
	players := s.PlayerSnapshots()
	if len(players) == 0 {
		return false
	}
	player := players[rng.Intn(len(players))]
	passiveTypes := AllPassiveAnimalTypes()
	hostileTypes := AllHostileMobTypes()
	for attempt := 0; attempt < 8; attempt++ {
		angle := rng.Float64() * 2 * math.Pi
		distance := 16.0 + rng.Float64()*16.0
		x := int(math.Floor(player.X + math.Cos(angle)*distance))
		z := int(math.Floor(player.Z + math.Sin(angle)*distance))
		y, canPassive, canHostile := s.resolveSpawnColumn(x, z)
		if !canPassive && !canHostile {
			continue
		}

		spawnHostile := canHostile && (!canPassive || rng.Float32() < 0.5)
		var typeName string
		if spawnHostile {
			typeName = hostileTypes[rng.Intn(len(hostileTypes))]
		} else {
			typeName = passiveTypes[rng.Intn(len(passiveTypes))]
		}

		if _, err := s.SpawnMob(float64(x)+0.5, float64(y), float64(z)+0.5, typeName); err != nil {
			continue
		}
		return true
	}
	return false
}

func decodeLightFromUpdate(raw []byte, x, y, z int) (skyLight, blockLight byte, ok bool) {
	if y < world.MinY || y >= world.MinY+world.WorldHeight {
		return 0, 0, false
	}
	r := protocol.NewReader(raw)
	if _, err := r.VarInt(); err != nil {
		return 0, 0, false
	}
	if _, err := r.VarInt(); err != nil {
		return 0, 0, false
	}
	skyMask, err := readBitSetMask(r)
	if err != nil {
		return 0, 0, false
	}
	blockMask, err := readBitSetMask(r)
	if err != nil {
		return 0, 0, false
	}
	if _, err := readBitSetMask(r); err != nil {
		return 0, 0, false
	}
	if _, err := readBitSetMask(r); err != nil {
		return 0, 0, false
	}

	si := (y - world.MinY) >> 4
	targetBit := uint(si + 1)
	nibbleIdx := ((y & 15) << 8) | ((z & 15) << 4) | (x & 15)

	skyVal, err := readSectionNibble(r, skyMask, targetBit, nibbleIdx)
	if err != nil {
		return 0, 0, false
	}
	blockVal, err := readSectionNibble(r, blockMask, targetBit, nibbleIdx)
	if err != nil {
		return 0, 0, false
	}
	return skyVal, blockVal, true
}

func readBitSetMask(r *protocol.Reader) (uint64, error) {
	n, err := r.VarInt()
	if err != nil {
		return 0, err
	}
	var mask uint64
	for i := int32(0); i < n; i++ {
		v, err := r.Int64()
		if err != nil {
			return 0, err
		}
		if i == 0 {
			mask = uint64(v)
		}
	}
	return mask, nil
}

func readSectionNibble(r *protocol.Reader, mask uint64, targetBit uint, nibbleIdx int) (byte, error) {
	count, err := r.VarInt()
	if err != nil {
		return 0, err
	}
	targetIdx := -1
	if (mask & (1 << targetBit)) != 0 {
		targetIdx = bits.OnesCount64(mask & ((1 << targetBit) - 1))
	}
	var val byte
	targetByteOffset := nibbleIdx >> 1
	for i := 0; i < int(count); i++ {
		arrLen, err := r.VarInt()
		if err != nil {
			return 0, err
		}
		for j := 0; j < int(arrLen); j++ {
			b, err := r.ReadByte()
			if err != nil {
				return 0, err
			}
			if i == targetIdx && int(arrLen) == 2048 && j == targetByteOffset {
				if nibbleIdx&1 == 0 {
					val = b & 0x0f
				} else {
					val = b >> 4
				}
			}
		}
	}
	return val, nil
}

