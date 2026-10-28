package server

import (
	"math/rand"
	"testing"

	"regionio/internal/world"
)

func TestBlazeChargedPathOnFireFireballBurstSmokeAndWaterSnowball(t *testing.T) {
	srv := newSurvivalTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.DimensionChunks(dim)
	rng := rand.New(rand.NewSource(42))

	col := &survivalPacketCollector{}
	player, err := srv.RegisterPlayer(Profile{
		UUID: [16]byte{31, 1, 1, 1},
		Name: "NetherHunter",
	}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)
	feetY := float64(world.FlatSurfaceY + 1)
	srv.SetPlayerTransform(player, 10.5, feetY, 10.5, 0, 0, true)

	// 1. Spawn Blaze 35 blocks away so all 3 burst fireballs remain in flight at tick 93.
	blazeID, err := srv.SpawnBlaze(dim, 45.5, feetY+2.0, 10.5)
	if err != nil || blazeID <= 0 {
		t.Fatalf("SpawnBlaze: id=%d, err=%v", blazeID, err)
	}
	if hp := srv.MobHealth(blazeID); hp != DefaultBlazeMaxHealth {
		t.Fatalf("Blaze initial health = %v, want %v", hp, DefaultBlazeMaxHealth)
	}
	if !IsFireImmuneMob(MobTypeBlaze) || !IsFireImmuneMob(MobTypeGhast) || !IsFireImmuneMob(MobTypeMagmaCube) {
		t.Fatal("expected Blaze, Ghast, and Magma Cube to be fire immune")
	}

	pkt := EncodeBlazeEntityDataPacket(blazeID, true)
	decID, decCharged, err := DecodeBlazeEntityDataPacket(pkt)
	if err != nil {
		t.Fatalf("DecodeBlazeEntityDataPacket: %v", err)
	}
	if decID != blazeID || !decCharged {
		t.Fatalf("decoded blaze metadata (%d, %v), want (%d, true)", decID, decCharged, blazeID)
	}

	// 2. Tick Blaze with player in range:
	//   - initial AttackTime = 20 -> at tick 20 enters AttackStep=1, Charged=true, AttackTime=60
	//   - at tick 80, 86, 92 fires 3 small_fireballs and enters 100-tick cooldown (AttackStep=0, Charged=false).
	for tick := 1; tick <= 30; tick++ {
		srv.TickBlazesGhastsAndMagmaCubes(rng)
	}
	if !srv.IsBlazeOnFire(blazeID) {
		t.Fatal("expected Blaze to be Charged (path on fire) during 60-tick charge-up")
	}
	for tick := 31; tick <= 93; tick++ {
		srv.TickBlazesGhastsAndMagmaCubes(rng)
	}
	bst, ok := srv.GetBlazeState(blazeID)
	if !ok {
		t.Fatal("expected GetBlazeState to succeed")
	}
	if bst.SmokeEmitted <= 0 {
		t.Fatalf("expected Blaze to emit large_smoke particles, got SmokeEmitted=%d", bst.SmokeEmitted)
	}
	if bst.AttackStep != 0 || bst.Charged || bst.FireballsShot != BlazeBurstFireballCount {
		t.Fatalf("expected Blaze to finish 3-shot burst and enter cooldown (got AttackStep=%d, Charged=%v, FireballsShot=%d)",
			bst.AttackStep, bst.Charged, bst.FireballsShot)
	}

	fireballs := srv.ActiveFireballs()
	smallCount := 0
	for _, fb := range fireballs {
		if fb.TypeName == EntityTypeSmallFireball && fb.ShooterID == blazeID {
			smallCount++
		}
	}
	if smallCount != BlazeBurstFireballCount {
		t.Fatalf("active small_fireball count after Blaze burst = %d, want %d", smallCount, BlazeBurstFireballCount)
	}

	// 3. Test small_fireball hitting a player deals 5.0 damage + 100 fire ticks,
	// and hitting a solid block ignites air when mobGriefing=true.
	initialPlayerHP := player.Snapshot().Health
	directFB, err := srv.SpawnBlazeSmallFireball(dim, blazeID, 10.5, feetY+1.0, 10.5, 0.1, 0, 0)
	if err != nil || directFB <= 0 {
		t.Fatalf("SpawnBlazeSmallFireball: id=%d, err=%v", directFB, err)
	}
	srv.TickBlazesGhastsAndMagmaCubes(rng)
	afterPlayerHP := player.Snapshot().Health
	if afterPlayerHP >= initialPlayerHP {
		t.Fatalf("expected small_fireball hit to damage player (before=%v, after=%v)", initialPlayerHP, afterPlayerHP)
	}
	if srv.PlayerFireTicks(player) < BlazeSmallFireballIgniteTicks-2 {
		t.Fatalf("expected player fire ticks >= %d, got %d", BlazeSmallFireballIgniteTicks-2, srv.PlayerFireTicks(player))
	}

	// Place a stone block at (30, FlatSurfaceY+2, 10) and fire a small_fireball into it so it ignites air at (30, FlatSurfaceY+2, 10) or adjacent.
	wallX, wallY, wallZ := 30, world.FlatSurfaceY+1, 10
	cache.SetBlock(wallX, wallY, wallZ, world.StateStone)
	cache.SetBlock(wallX, wallY+1, wallZ, world.StateAir)
	// Fire downward/horizontally so impact point (nx, ny, nz) lands in an air block or test impactFireballAt directly.
	fbSnap, _ := srv.GetFireballProjectileState(directFB)
	fbSnap.TypeName = EntityTypeSmallFireball
	srv.impactFireballAt(0, fbSnap, dim, float64(wallX)+0.5, float64(wallY+1)+0.1, float64(wallZ)+0.5, nil, nil)
	if gotBlk := world.BlockName(cache.GetBlock(wallX, wallY+1, wallZ)); gotBlk != "minecraft:fire" {
		t.Fatalf("expected small_fireball impact to ignite air into minecraft:fire, got %q", gotBlk)
	}

	// 4. Verify snowball deals 3.0 damage to Blaze and water deals 1.0 damage every 20 ticks.
	hpBeforeSnowball := srv.MobHealth(blazeID)
	dealt, killed := srv.OnBlazeHitBySnowball(blazeID, player)
	if dealt != BlazeSnowballDamage || killed {
		t.Fatalf("OnBlazeHitBySnowball = (dealt=%v, killed=%v), want (%v, false)", dealt, killed, BlazeSnowballDamage)
	}
	if gotHP := srv.MobHealth(blazeID); gotHP != hpBeforeSnowball-BlazeSnowballDamage {
		t.Fatalf("Blaze HP after snowball = %v, want %v", gotHP, hpBeforeSnowball-BlazeSnowballDamage)
	}

	ent := srv.entities.Get(blazeID)
	bx, by, bz := int(ent.X), int(ent.Y), int(ent.Z)
	cache.SetBlock(bx, by, bz, world.StateWater)
	hpBeforeWater := srv.MobHealth(blazeID)
	srv.TickBlazesGhastsAndMagmaCubes(rng)
	if gotHP := srv.MobHealth(blazeID); gotHP != hpBeforeWater-BlazeWaterDamage {
		t.Fatalf("Blaze HP after water contact = %v, want %v", gotHP, hpBeforeWater-BlazeWaterDamage)
	}

	// 5. Verify Blaze loot drops (only when killedByPlayer=true) and 10 XP reward.
	if drops := RollBlazeDeathLoot(rng, 0, false); len(drops) != 0 {
		t.Fatalf("expected 0 blaze_rod drops when killedByPlayer=false, got %v", drops)
	}
	foundRod := false
	for i := 0; i < 20; i++ {
		if drops := RollBlazeDeathLoot(rng, 3, true); len(drops) > 0 && drops[0].ResolvedID() == BlazeRodItemID {
			foundRod = true
			break
		}
	}
	if !foundRod {
		t.Fatal("expected RollBlazeDeathLoot with killedByPlayer=true to drop minecraft:blaze_rod")
	}
	if xp := NetherHostileXPReward(MobTypeBlaze, 0); xp != BlazeXPReward {
		t.Fatalf("NetherHostileXPReward(Blaze) = %d, want %d", xp, BlazeXPReward)
	}
}

func TestGhastChargingCryFireballExplodeAndReturnToSender(t *testing.T) {
	srv := newSurvivalTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.DimensionChunks(dim)
	rng := rand.New(rand.NewSource(99))

	col := &survivalPacketCollector{}
	player, err := srv.RegisterPlayer(Profile{
		UUID: [16]byte{32, 1, 1, 1},
		Name: "GhastDeflector",
	}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)
	feetY := float64(world.FlatSurfaceY + 1)
	// Player at (40.5, feetY, 40.5) looking +X (yaw = -90) toward Ghast at (55.5, feetY+2, 40.5).
	srv.SetPlayerTransform(player, 40.5, feetY, 40.5, -90, 0, true)

	ghastID, err := srv.SpawnGhast(dim, 55.5, feetY+2.0, 40.5)
	if err != nil || ghastID <= 0 {
		t.Fatalf("SpawnGhast: id=%d, err=%v", ghastID, err)
	}
	if hp := srv.MobHealth(ghastID); hp != DefaultGhastMaxHealth {
		t.Fatalf("Ghast initial HP = %v, want %v", hp, DefaultGhastMaxHealth)
	}

	// Verify Ghast metadata packet round-trip.
	gpkt := EncodeGhastEntityDataPacket(ghastID, true)
	decGID, decCharging, err := DecodeGhastEntityDataPacket(gpkt)
	if err != nil {
		t.Fatalf("DecodeGhastEntityDataPacket: %v", err)
	}
	if decGID != ghastID || !decCharging {
		t.Fatalf("decoded ghast metadata = (%d, %v), want (%d, true)", decGID, decCharging, ghastID)
	}

	// Tick Ghast 12 times: ChargeTicks reaches 12 >= 10 -> Charging == true (warn cry).
	for i := 0; i < 12; i++ {
		srv.TickBlazesGhastsAndMagmaCubes(rng)
	}
	gst, ok := srv.GetGhastState(ghastID)
	if !ok || !gst.Charging || gst.ChargeTicks != 12 || gst.CryCount < 1 {
		t.Fatalf("Ghast state at tick 12 = %+v, want Charging=true, ChargeTicks=12, CryCount>=1", gst)
	}

	// Tick 8 more times (to tick 20): Ghast shoots a fireball and resets ChargeTicks to -40, Charging=false.
	for i := 0; i < 8; i++ {
		srv.TickBlazesGhastsAndMagmaCubes(rng)
	}
	gst, _ = srv.GetGhastState(ghastID)
	if gst.Charging || gst.ChargeTicks != GhastPostShotCooldownTicks {
		t.Fatalf("Ghast state after shooting at tick 20 = %+v, want Charging=false, ChargeTicks=%d", gst, GhastPostShotCooldownTicks)
	}

	var ghastFB FireballProjectileState
	foundFB := false
	for _, fb := range srv.ActiveFireballs() {
		if fb.TypeName == EntityTypeFireball && fb.ShooterID == ghastID {
			ghastFB = fb
			foundFB = true
			break
		}
	}
	if !foundFB {
		t.Fatal("expected Ghast to spawn a minecraft:fireball at ChargeTicks == 20")
	}

	// Deflect the Ghast fireball back toward the Ghast (+X, yaw = -90, pitch = 0) and place it right in front of the Ghast.
	gEnt := srv.entities.Get(ghastID)
	srv.entities.Update(ghastFB.EntityID, func(e *world.Entity) {
		e.X = gEnt.X - 0.5
		e.Y = gEnt.Y + 1.0
		e.Z = gEnt.Z
	})
	srv.SetPlayerTransform(player, gEnt.X-2.0, gEnt.Y, gEnt.Z, -90, 0, true)
	if !srv.DeflectGhastFireball(ghastFB.EntityID, player) {
		t.Fatal("expected DeflectGhastFireball to succeed")
	}
	defFB, ok := srv.GetFireballProjectileState(ghastFB.EntityID)
	if !ok || !defFB.Deflected || defFB.DeflectedBy != player {
		t.Fatalf("expected fireball to be marked Deflected by player, got %+v", defFB)
	}

	// Tick: the deflected fireball hits the Ghast, dealing 1000.0 damage, killing it,
	// and awarding "minecraft:nether/return_to_sender".
	srv.TickBlazesGhastsAndMagmaCubes(rng)
	if srv.entities.Get(ghastID) != nil {
		t.Fatal("expected deflected Ghast fireball to kill the Ghast on impact")
	}
	if !player.HasCompletedAdvancement(AdvancementReturnToSender) {
		t.Fatalf("expected player to earn %q advancement after killing Ghast with deflected fireball", AdvancementReturnToSender)
	}

	// Verify Ghast fireball block impact triggers explosion (`ExplodeMob`).
	targetX, targetY, targetZ := 70, world.FlatSurfaceY+1, 70
	cache.SetBlock(targetX, targetY, targetZ, world.StateDirt)
	expFB, err := srv.SpawnGhastFireball(dim, 0, float64(targetX)+0.5, float64(targetY)+1.2, float64(targetZ)+0.5, 0, -0.8, 0, 1)
	if err != nil || expFB <= 0 {
		t.Fatalf("SpawnGhastFireball for block impact: %v", err)
	}
	srv.TickBlazesGhastsAndMagmaCubes(rng)
	if _, stillAlive := srv.GetFireballProjectileState(expFB); stillAlive {
		t.Fatal("expected Ghast fireball to detonate and be removed on block impact")
	}
}

func TestMagmaCubeSizeSplitArmorLavaSwimAndJumpAI(t *testing.T) {
	srv := newSurvivalTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.DimensionChunks(dim)
	rng := rand.New(rand.NewSource(77))

	col := &survivalPacketCollector{}
	player, err := srv.RegisterPlayer(Profile{
		UUID: [16]byte{33, 1, 1, 1},
		Name: "BasaltExplorer",
	}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)
	feetY := float64(world.FlatSurfaceY + 1)
	srv.SetPlayerTransform(player, 100.5, feetY, 100.5, 0, 0, true)

	// 1. Spawn large (size 4) Magma Cube: verify 16 HP, 12 armor points, fire/lava immunity, and size metadata packet.
	largeID, err := srv.SpawnMagmaCube(dim, 101.2, feetY, 100.5, MagmaCubeSizeLarge)
	if err != nil || largeID <= 0 {
		t.Fatalf("SpawnMagmaCube(size 4): id=%d, err=%v", largeID, err)
	}
	if hp := srv.MobHealth(largeID); hp != 16.0 {
		t.Fatalf("Large Magma Cube HP = %v, want 16.0", hp)
	}
	if armor := MagmaCubeArmorPoints(MagmaCubeSizeLarge); armor != 12 {
		t.Fatalf("MagmaCubeArmorPoints(4) = %d, want 12", armor)
	}
	if !IsMagmaCubeImmuneToDamageCause("lava") || !IsMagmaCubeImmuneToDamageCause("on_fire") || !IsMagmaCubeImmuneToDamageCause("hot_floor") {
		t.Fatal("expected Magma Cube to be immune to lava, fire, and hot_floor")
	}

	spkt := EncodeSlimeSizeDataPacket(largeID, int32(MagmaCubeSizeLarge))
	decSID, decSize, err := DecodeSlimeSizeDataPacket(spkt)
	if err != nil {
		t.Fatalf("DecodeSlimeSizeDataPacket: %v", err)
	}
	if decSID != largeID || int(decSize) != MagmaCubeSizeLarge {
		t.Fatalf("decoded slime size = (%d, %d), want (%d, %d)", decSID, decSize, largeID, MagmaCubeSizeLarge)
	}

	// 2. Verify armor reduction when attacking Magma Cube (12 armor reduces 4.0 raw damage).
	srv.applyDamageToMob(srv.entities.Get(largeID), 4.0, player, 0, false, true)
	afterHitHP := srv.MobHealth(largeID)
	if afterHitHP <= 12.0 || afterHitHP >= 16.0 {
		t.Fatalf("expected Magma Cube 12 armor points to partially absorb 4.0 damage (got HP=%v)", afterHitHP)
	}

	// 3. Place Magma Cube in lava and verify it swims upward (`InLava == true`, Y increases).
	lavaState, ok := world.BlockStateByName("minecraft:lava", map[string]string{"level": "0"})
	if !ok {
		t.Fatal("expected minecraft:lava block state")
	}
	cache.SetBlock(101, world.FlatSurfaceY+1, 100, lavaState)
	beforeY := srv.entities.Get(largeID).Y
	srv.TickBlazesGhastsAndMagmaCubes(rng)
	mst, ok := srv.GetMagmaCubeState(largeID)
	if !ok || !mst.InLava {
		t.Fatalf("expected Magma Cube in lava to have InLava=true, got %+v", mst)
	}
	if afterY := srv.entities.Get(largeID).Y; afterY <= beforeY {
		t.Fatalf("expected Magma Cube to float/jump upward in lava (beforeY=%v, afterY=%v)", beforeY, afterY)
	}

	// 4. Verify contact melee damage (even small size 1 Magma Cube deals 3.0 damage, size 4 deals 6.0).
	if dmg := MagmaCubeAttackDamage(MagmaCubeSizeSmall); dmg != 3.0 {
		t.Fatalf("MagmaCubeAttackDamage(1) = %v, want 3.0", dmg)
	}
	if dmg := SlimeAttackDamage(MagmaCubeSizeSmall); dmg != 0 {
		t.Fatalf("SlimeAttackDamage(1) = %v, want 0 (small slimes deal 0 damage)", dmg)
	}

	// 5. Split large (size 4) -> medium (size 2) -> small (size 1).
	medChildren, drops := srv.SplitSlimeOrMagmaCubeOnDeath(largeID, rng)
	if len(medChildren) < 2 || len(medChildren) > 4 {
		t.Fatalf("large Magma Cube split into %d children, want 2..4", len(medChildren))
	}
	if len(drops) == 0 {
		t.Fatal("expected large Magma Cube death to drop minecraft:magma_cream")
	}
	medID := medChildren[0]
	if sz := srv.SlimeSize(medID); sz != MagmaCubeSizeMedium {
		t.Fatalf("child Magma Cube size = %d, want %d", sz, MagmaCubeSizeMedium)
	}
	if hp := srv.MobHealth(medID); hp != 4.0 {
		t.Fatalf("medium Magma Cube HP = %v, want 4.0", hp)
	}

	smallChildren, medDrops := srv.SplitSlimeOrMagmaCubeOnDeath(medID, rng)
	if len(smallChildren) < 2 || len(smallChildren) > 4 {
		t.Fatalf("medium Magma Cube split into %d children, want 2..4", len(smallChildren))
	}
	if len(medDrops) == 0 {
		t.Fatal("expected medium Magma Cube death to drop minecraft:magma_cream")
	}
	smallID := smallChildren[0]
	if sz := srv.SlimeSize(smallID); sz != MagmaCubeSizeSmall {
		t.Fatalf("small Magma Cube size = %d, want %d", sz, MagmaCubeSizeSmall)
	}
	if hp := srv.MobHealth(smallID); hp != 1.0 {
		t.Fatalf("small Magma Cube HP = %v, want 1.0", hp)
	}

	noneChildren, _ := srv.SplitSlimeOrMagmaCubeOnDeath(smallID, rng)
	if len(noneChildren) != 0 {
		t.Fatalf("expected small Magma Cube not to split further, got %d children", len(noneChildren))
	}
}

func TestCreeperChargedViaLightningRadiusSixIgniteAndSkullDrop(t *testing.T) {
	srv := newSurvivalTestServer(t)
	dim := world.DimensionOverworld
	rng := rand.New(rand.NewSource(123))

	creeperID, err := srv.SpawnMobInDimension(dim, 200.5, float64(world.FlatSurfaceY+1), 200.5, MobTypeCreeper)
	if err != nil || creeperID <= 0 {
		t.Fatalf("SpawnMobInDimension(Creeper): id=%d, err=%v", creeperID, err)
	}
	if srv.IsCreeperCharged(creeperID) {
		t.Fatal("expected newly spawned creeper not to be charged")
	}

	// 1. Verify Creeper SynchedEntityData packet (index 17 swell, index 18 DATA_IS_POWERED, index 19 DATA_IS_IGNITED).
	cpkt := EncodeCreeperEntityDataPacket(creeperID, 1, true, true)
	decCID, decSwell, decPowered, decIgnited, err := DecodeCreeperEntityDataPacket(cpkt)
	if err != nil {
		t.Fatalf("DecodeCreeperEntityDataPacket: %v", err)
	}
	if decCID != creeperID || decSwell != 1 || !decPowered || !decIgnited {
		t.Fatalf("decoded creeper metadata = (%d, %d, %v, %v), want (%d, 1, true, true)",
			decCID, decSwell, decPowered, decIgnited, creeperID)
	}

	// 2. Strike lightning near the creeper via SpawnLightningBolt -> charges the creeper!
	_, _, _, _, _ = srv.SpawnLightningBolt(dim, 201.0, float64(world.FlatSurfaceY+1), 200.5, rng)
	if !srv.IsCreeperCharged(creeperID) {
		t.Fatal("expected lightning bolt near creeper to set IsCreeperCharged(creeperID) = true")
	}

	// 3. Test manual ignition with flint_and_steel (`IgniteCreeper`).
	if !srv.IgniteCreeper(creeperID, nil) || !srv.IsCreeperIgnited(creeperID) {
		t.Fatal("expected IgniteCreeper to mark creeper as ignited")
	}
	_, swellDir := srv.CreeperFuseState(creeperID)
	if swellDir != 1 {
		t.Fatalf("expected ignited creeper swellDir = 1, got %d", swellDir)
	}

	// 4. Spawn a zombie and a skeleton next to the Charged Creeper, lower their health so the
	// explosion kills them, and trigger the Charged Creeper explosion (radius 6.0).
	zombieID, err := srv.SpawnMobInDimension(dim, 201.2, float64(world.FlatSurfaceY+1), 200.5, MobTypeZombie)
	if err != nil {
		t.Fatalf("SpawnMob(Zombie): %v", err)
	}
	skelID, err := srv.SpawnMobInDimension(dim, 199.8, float64(world.FlatSurfaceY+1), 200.5, MobTypeSkeleton)
	if err != nil {
		t.Fatalf("SpawnMob(Skeleton): %v", err)
	}
	srv.SetMobHealth(zombieID, 5.0)
	srv.SetMobHealth(skelID, 5.0)

	srv.TriggerCreeperExplosion(creeperID, 200.5, float64(world.FlatSurfaceY+1), 200.5)

	if srv.entities.Get(zombieID) != nil || srv.entities.Get(skelID) != nil {
		t.Fatal("expected Charged Creeper explosion (radius 6.0) to kill nearby zombie and skeleton")
	}

	var hasZombieHead, hasSkeletonSkull bool
	for _, ent := range srv.entities.All() {
		if !ent.IsItem() {
			continue
		}
		switch ent.Item.ResolvedID() {
		case ZombieHeadItemID:
			hasZombieHead = true
		case SkeletonSkullItemID:
			hasSkeletonSkull = true
		}
	}
	if !hasZombieHead || !hasSkeletonSkull {
		t.Fatalf("expected Charged Creeper explosion to drop zombie_head (%v) and skeleton_skull (%v)",
			hasZombieHead, hasSkeletonSkull)
	}

	// Verify CreeperHeadDropForVictim mapping for all 5 vanilla skull-dropping mobs.
	if got, ok := CreeperHeadDropForVictim(MobTypeWitherSkeleton); !ok || got != WitherSkeletonSkullItemID {
		t.Fatalf("CreeperHeadDropForVictim(wither_skeleton) = (%q, %v), want (%q, true)", got, ok, WitherSkeletonSkullItemID)
	}
	if got, ok := CreeperHeadDropForVictim(MobTypeCreeper); !ok || got != CreeperHeadItemID {
		t.Fatalf("CreeperHeadDropForVictim(creeper) = (%q, %v), want (%q, true)", got, ok, CreeperHeadItemID)
	}
	if got, ok := CreeperHeadDropForVictim(MobTypePiglin); !ok || got != PiglinHeadItemID {
		t.Fatalf("CreeperHeadDropForVictim(piglin) = (%q, %v), want (%q, true)", got, ok, PiglinHeadItemID)
	}
}
