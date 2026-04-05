# Cellarium - Artificial Life Simulator

## Overview
Single-file Go simulation (~1600 lines in `main.go`) of evolving cells in a bottle-shaped 2D environment. Uses Ebiten v2 for rendering.

## Build & Run
```bash
go build -o eva .
./eva
```

## Architecture
Everything lives in `main.go`. No packages, no tests.

### Key systems (in execution order per tick):
1. **Spatial hash** — rebuilt each tick for neighbor queries (bucket size 30px)
2. **Crowding map** — cell count per grid tile, used for oxygen scaling
3. **Light map** — sunlight attenuated top-down by cell density (8% per cell)
4. **Gene execution** — each cell evaluates its genes: sense → condition → action
5. **Physics** — velocity integration, drag, wall repulsion, separation forces
6. **Environment grids** — oxygen/nutrient/toxin diffusion, nutrient sinking, surface oxygen

### Environment model ("bottle"):
- **Bottle bounds**: walls at left (12px), right (1188px), top (3px), bottom (776px)
- **Sunlight**: full at top, zero at bottom. Blocked by cells above (light map)
- **Oxygen**: produced at surface + by photosynthesis. Consumed by all cells. Crowding multiplies consumption
- **Nutrients**: released on death, sink downward, diffuse, decay
- **Toxins**: deposited by actToxin gene, damage nearby cells, diffuse slowly, decay

### Genetic system:
- Genome = random int sequence (0-15)
- Parsed into genes: `[START, sense, condition, action, weight, STOP]`
- Invalid sense/condition/action values are skipped during parsing
- 9 senses (light, energy, neighbors, dist, nutrient, age, size, oxygen, toxin)
- 4 conditions (HIGH >0.6, LOW <0.4, MED 0.4-0.6, ALWAYS)
- 9 actions (photo, forward, turnLeft, turnRight, eat, grow, reproduce, toxin, defense)
- Mutation: 5% point, 2% insert, 2% delete, 1% duplication

### Movement model:
Cells have a heading angle. Three primitive actions:
- `forward` — thrust in facing direction
- `turnLeft` / `turnRight` — rotate heading
Complex navigation (phototaxis, chemotaxis) must evolve from sense+turn combinations.

## Controls
- **Space**: pause/resume
- **R**: restart simulation
- **Escape**: quit
- **Scroll wheel**: zoom (centered on cursor)
- **Right-click drag**: pan camera
- **Left-click cell**: inspect genes
- **Speed buttons**: 1x, 2x, 5x, 10x
- **Export/Import buttons**: save/load simulation state as JSON

## Constants to tune
All in the `const` block at top of `main.go`:
- `maxCells` (2000) — population hard cap
- `maxAge` (3000) — lifespan in ticks
- `oxygenBreathRate` (0.15) — oxygen consumed per cell per tick per size
- `lowOxygenPenalty` (2.0) — energy cost when oxygen is depleted
- `reproThreshold` (50.0) — energy needed = this * size
- `nutrientSinkRate` (0.08) — how fast dead matter settles to bottom
- `depthCostMax` (0.3) — extra maintenance at the very bottom

## Save format
JSON files named `cellarium_save_<unix_timestamp>.json`. Contains all cells (with genomes), tick count, and full nutrient/oxygen/toxin grids. Import loads the most recent save file in the working directory.
