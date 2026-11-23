# Hoppers fun metrics

How "fun" a night of Hoppers is, measured headless by `cmd/hopsim -fun`. It covers the metrics and their targets, the baseline for what the live server runs today, and ratings for small rubber-band rule tweaks.

**TL;DR**
- Baseline fun score: **82.9** (4 / 8 / 15 people: 84.9 / 81.3 / 82.6).
- Weakest metrics: **spread** (the pack is half the track wide at lap ends), **stragglers** (30% of frogs are more than a third of the track behind at halfway), and **halfway-leader wins** (25%, *under* the ~35% target).
- Runaway leads are already rare (3%). Stacking (landing on a frog and riding it) already pulls hard against the leader, so knobs that punish the leader make the game worse. The useful knobs help the back of the pack instead.
- **Recommend:** tighter special placement + backward hops of 1–2 spots (**85.4**, +2.4). TurnBack 0.75 is optional on top. No live defaults were changed.

## How to run it

```sh
go run ./cmd/hopsim -fun -live -games 2000 -players 4,8,15            # baseline
go run ./cmd/hopsim -fun -live -games 2000 -players 4,8,15 -backroll 2 # one tweak
go run ./cmd/hopsim -fun -live ... -json                               # JSON (metrics, per-metric scores, score)
```

- `-live` plays what the live server runs. Each night it builds a fresh `engine.Generate(rand.NewPCG(seed, 0xc0b), DefaultPreferences(), nil)` cup, which is exactly what `main.go` does without `-config`. That cup opens with the shortest straight (Lake Sprint 13) with no specials, then two random levels with 1 launch + 1 chute each. hopsim's old default (`engine.Default()`) differs: it uses a fixed cup and has **no** specials. That is why this mode exists.
- **Crowd skill:** a cycled bar mix of 3 casual, 3 practiced and 2 expert per 8 seats. Bots fill races to 7 frogs and never flick perfectly.
- **Win shares** come from separate 8-person skill-mix nights (no bots), using the same `-games` count.
- Every tweak run uses the same seeds as the baseline, so the deltas compare like with like. Seed noise on the mean score is about **±0.5** (re-running the baseline on two other seed ranges gave 83.3 and 83.3).

## Metrics and targets

The code defines them in one table: `targets` in `cmd/hopsim/fun.go`. Each metric scores 1 inside its target and falls linearly to 0 at the "zero at" value. The fun score is the weighted mean × 100. The metric table always prints next to the score.

| metric | definition | target | zero at | weight |
|---|---|---|---|---|
| expert win share | race wins of the expert in 1 expert + 7 casual, ÷ fair 1/8 | 1.05–1.30× | 0.90× / 2.0× | **3** |
| casual win share | casuals' race wins in 2 expert, 3 practiced, 3 casual, ÷ fair | ≥ 0.90× | 0.50× | 2 |
| underdog wins | races won by a frog last or 2nd-to-last at halfway (by hops) | 10–15% | 2% / 30% | 1.5 |
| spread mean | first-to-last gap at every lap end, ÷ track length | ≤ 25% | 60% | 1.5 |
| spread p90 | 90th percentile of the same | ≤ 45% | 90% | 1 |
| stragglers | frog-races more than ⅓ track behind the leader at halfway | ≤ 10% | 40% | **3** |
| turns far behind | own turns per frog-race that start more than ⅓ track behind | ≤ 1.0 | 4 | 1 |
| runaway leads | races where, at some lap end, the leader is more than ⅓ track ahead of 2nd | ≤ 10% | 40% | **3** |
| lap-1 leader wins | the leader after lap 1 wins | 20–30% (~25) | 10% / 55% | **3** |
| halfway leader wins | the leader at the middle hop wins | 30–40% (~35) | 15% / 65% | **3** |
| lead changes | per race | 3–8 | 1 / 14 | 1 |
| late drama | races with a lead change in the last lap's worth of hops | 35–75% | 10% / 100% | 1 |
| 3+ back streaks | share of turnarounds that become 3+ backward hops in a row | ≤ 15% | 45% | 1 |
| 3+ back frog-races | frog-races that had such a streak | ≤ 5% | 20% | 0.5 |
| stuck mean | per frog-race, the longest run of own turns with no net forward progress | ≤ 1.5 | 4 | 1 |
| stuck p90 | 90th percentile of the same | ≤ 3 | 7 | 1 |

- A **lap** is one turn for every frog. **Halfway** is the race's middle hop, the same as the existing `mid->win` column.
- **Why these weights:** the things a bar crowd feels most get the heaviest weights: being left behind (stragglers), a race that's decided early (runaways, early-leader wins), and one expert taking everything.
- The expert target has a floor (1.05×) so that a little skill still has to matter.
- **Shown but not scored:** the practiced player's share (1P+7C), a casual against 7 experts, the spread in spots, where the last frog and the leader are at lap ends, hops per frog, and race minutes.

## Baseline (live config, 2000 nights each)

| metric | 4 people | 8 people | 15 people |
|---|---|---|---|
| expert win share | 1.28× | 1.28× | 1.28× |
| casual win share | 0.87× ⚠ | 0.87× ⚠ | 0.87× ⚠ |
| underdog wins | 12.4% | 10.8% | 11.8% |
| spread mean / track | 47.6% ❌ | 50.5% ❌ | 49.0% ❌ |
| spread p90 / track | 75% ❌ | 77% ❌ | 75% ❌ |
| stragglers at halfway | 27.9% ❌ | 30.4% ❌ | 29.1% ❌ |
| turns far behind | 1.02 | 0.96 | 0.96 |
| runaway leads | 4.9% | 3.2% | 3.5% |
| lap-1 leader wins | 22.5% | 21.5% | 21.7% |
| halfway leader wins | 27.2% ⚠ | 25.2% ⚠ | 25.4% ⚠ |
| lead changes | 6.4 | 6.3 | 6.3 |
| late drama | 72.6% | 79.9% ⚠ | 77.4% ⚠ |
| 3+ back streaks | 6.2% | 5.2% | 5.2% |
| 3+ back frog-races | 1.5% | 1.1% | 1.1% |
| stuck mean / p90 | 0.50 / 2 | 0.37 / 1 | 0.40 / 1 |
| *(info)* practiced 1P+7C / casual vs 7 experts | 1.17× / 0.82× | | |
| *(info)* last frog / leader at lap end | 21% / 68% | 20% / 70% | 21% / 70% |
| *(info)* hops per frog · race minutes | 3.6 · 1.9 | 3.0 · 2.1 | 3.2 · 2.0 |
| **fun score** | **84.9** | **81.3** | **82.6** |

**What it says**
- **Races are short.** A frog makes only about 3 hops, so a race has about 3 laps. Long backward streaks and being stuck barely happen (5% and under 0.5 turns), and those metrics are already maxed.
- **The pack is wide, but nobody runs away.** The back of the pack sits at about 20% of the track and the leader at about 70%. Even so, a leader more than ⅓ of the track clear of 2nd is rare, because frogs land on the leader's stack and ride it.
- **The lead is too slippery, not too safe.** The halfway leader wins 25% of races (target ~35%), and 80% of races change leader in the last lap. More anti-leader rubber-banding pushes these the wrong way.
- **Spread mostly comes from 1–3 rolls over about 3 laps.** Even with slip at 0 the spread stays at 42% and stragglers at 26% (quick check, 1000 nights). No small knob gets spread near 25%, so read that row as a direction, not a pass/fail.

## Tweak ratings (2000 nights each, live config)

The "pack" placement is what `RandomTrack` already does: launches in the first half of the track and chutes in the second.

| # | tweak | flags |
|---|---|---|
| 1 | TurnBack 0.65 / 0.75 | `-turnback 0.65` / `0.75` |
| 2 | backward hops 1–2 | `-backroll 2` (new `rules.back_max_roll`) |
| 3 | leader slip 0.12 / 0.16 | `-leaderslip 0.12` / `0.16` (new `rules.leader_slip`) |
| 4 | tight placement: launches in the first 30% (where the back of the pack is), chutes past 60% (where the leaders are) | `-placement tight` (new `engine.RandomTrackIn` zones) |
| 4b | uniform placement, for comparison | `-placement uniform` |
| 5 | catch-up band: the green band widens by `catchup` × band per track length behind the leader (capped at 0.9). Experts still hit more often. | `-catchup 2` / `4` (new `rules.catch_up_band`) |
| 6 | leader hops 1–2 (suggested in the brief) | `-leaderroll 2` (new `rules.leader_max_roll`) |
| 7 | **extra:** last-place frog gets +1 on forward hops. Added because stragglers are the biggest problem, so this tests help aimed straight at the back. | `-trailbonus 1` (new `rules.trail_bonus`) |
| C2 | tight + backroll 2 | |
| C3 | tight + backroll 2 + TurnBack 0.75 | |

### Fun score

| tweak | 4 | 8 | 15 | mean | Δ mean |
|---|---|---|---|---|---|
| baseline | 84.9 | 81.3 | 82.6 | **82.9** | +0.0 |
| turnback65 | 85.2 | 81.2 | 82.9 | **83.1** | +0.2 |
| turnback75 | 85.5 | 81.2 | 83.1 | **83.3** | +0.3 |
| backroll2 | 85.5 | 81.6 | 83.7 | **83.6** | +0.7 |
| leaderslip12 | 84.4 | 80.8 | 82.1 | **82.4** | -0.5 |
| leaderslip16 | 83.9 | 79.8 | 81.4 | **81.7** | -1.3 |
| tight | 87.1 | 82.9 | 84.5 | **84.8** | +1.9 |
| uniform | 83.2 | 80.5 | 81.6 | **81.8** | -1.2 |
| catchup2 | 84.7 | 81.0 | 82.0 | **82.5** | -0.4 |
| catchup4 | 85.6 | 80.2 | 81.9 | **82.5** | -0.4 |
| leaderroll2 | 79.6 | 76.5 | 78.5 | **78.2** | -4.7 |
| trailbonus1 | 84.6 | 81.1 | 82.7 | **82.8** | -0.1 |
| combo2 | 87.3 | 83.7 | 85.1 | **85.4** | +2.4 |
| combo3 | 87.4 | 83.9 | 84.9 | **85.4** | +2.5 |

### Metrics at 8 people (baseline values, then deltas)

The baseline row holds the values. The other rows hold deltas: percentage points for % columns, raw differences otherwise. `·` means no change.

| tweak | expert | casual | underdog | spread | spread p90 | stragglers | far turns | runaway | lap-1 ldr | half ldr | lead chg | late drama | 3+ back | 3+ back frogs | stuck | stuck p90 | race min |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| baseline | 1.28x | 0.87x | 11% | 51% | 77% | 30% | 0.96 | 3% | 21% | 25% | 6.3 | 80% | 5% | 1% | 0.37 | 1 | 2.06 |
| turnback65 | -0.01 | · | · | -1 | -2 | · | · | · | +1 | · | · | · | -3 | -1 | · | · | · |
| turnback75 | · | · | · | -1 | -2 | · | · | · | · | -1 | · | +1 | -4 | -1 | · | · | · |
| backroll2 | -0.02 | +0.02 | · | -1 | -2 | -1 | · | -1 | · | -1 | · | · | +1 | · | · | · | · |
| leaderslip12 | +0.01 | · | · | · | · | · | · | · | -1 | -1 | +0.2 | +1 | +1 | · | · | · | · |
| leaderslip16 | +0.04 | +0.01 | · | · | · | · | · | · | -1 | -2 | +0.3 | +2 | +1 | · | · | · | · |
| tight | +0.05 | -0.01 | -1 | -1 | -2 | -4 | · | -2 | · | · | +0.1 | · | +1 | · | · | · | · |
| uniform | +0.09 | -0.01 | -1 | · | +2 | -1 | · | +1 | · | +1 | +0.1 | · | · | · | · | · | · |
| catchup2 | -0.01 | · | +1 | -1 | -2 | -1 | -0.06 | -1 | -1 | -1 | +0.1 | +2 | · | · | · | · | -0.06 |
| catchup4 | · | +0.03 | +2 | -1 | -2 | -2 | -0.08 | · | -2 | -2 | +0.2 | +5 | -1 | · | · | · | -0.10 |
| leaderroll2 | +0.06 | -0.01 | +1 | -1 | -2 | -2 | · | -1 | -2 | -5 | +0.6 | +6 | · | · | · | · | +0.09 |
| trailbonus1 | -0.06 | +0.01 | +4 | -3 | -2 | -4 | -0.11 | -1 | -2 | -2 | +0.3 | +3 | · | · | · | · | -0.13 |
| combo2 | +0.02 | -0.01 | · | -3 | -7 | -5 | · | -2 | +1 | · | +0.1 | · | +1 | · | · | · | · |
| combo3 | +0.03 | -0.01 | +1 | -4 | -8 | -6 | · | -2 | · | -1 | +0.1 | · | -4 | -1 | · | · | · |

### Reading the tweaks
- **Tight placement (+1.9): the best single tweak.** Stragglers drop 4 points and runaways 2 points, and nothing else gets worse. The catch: the expert's share edges up to 1.32×, because a launch near the start rewards early +1s. A launch that sits where the trailing frogs are works as a comeback ladder.
- **Backward hops 1–2 (+0.7):** a small, clean gain. Spread, stragglers and runaways all improve a little, and casual share goes up.
- **TurnBack 0.65 / 0.75 (+0.2 / +0.3):** this cuts 3+ back streaks from 5% to 1–2%. But streaks were already rare in races this short, so the gain is within noise. It's harmless.
- **Leader slip (−0.5 / −1.3) and leader hops 1–2 (−4.7):** both make things worse. They lower halfway-leader wins, which are already below target, and pile on more late lead changes. Leading needs to mean *more*, not less.
- **Catch-up band (−0.4 at 2 and at 4):** neutral to slightly negative. It only helps people (bots never flick perfectly), and the casual players who trail hit the band rarely anyway. At ×4 it also raises late drama by 5 points. Experts still lead the skill table (1.28×). If the owner wants it for feel, the knob is live-ready: the phone's snapshot `flickBand` already carries the per-player width.
- **Trail bonus +1 (−0.1):** the biggest straggler and underdog effect of any tweak (stragglers −4, underdog wins +4 to 15%, far-behind turns −0.11). It costs halfway-leader wins (−2) and adds late drama (+3), which cancels out in the score. Worth a playtest if the owner cares most about nobody being left behind.
- **Uniform placement (−1.2):** confirms the current pack bias is better than random placement. Tight placement is better still.

## Recommendation (the owner decides; no defaults were changed)

1. **Adopt tight placement.** Move `RandomTrack`'s zones to launches in 2..30% and chutes in 60%..len−2 (`engine.Zones`). This is a track-generator change, not a rule.
2. **Adopt `back_max_roll: 2`.** It's small and safe, and it stacks well with (1): C2 scores **85.4** (+2.4), with stragglers −5, spread p90 −7 and runaways −2. Across three seed ranges C2 beat the baseline by +1.3 to +2.5.
3. **Optional:** `turn_back: 0.75` on top (C3, 85.4) mainly cuts long backward streaks. Take it if those annoy people at the bar.
4. **Don't adopt** leader slip or leader hop range. Keep catch-up band and trail bonus as playtest options, not defaults.
5. **Watch:** the expert share sits at 1.29–1.30× with C2/C3, right at the top of the target. Casual share stays at 0.86–0.87×, a little under 0.90.

## What changed in the code
- `engine.Rules`: new knobs `back_max_roll`, `leader_max_roll`, `leader_slip`, `catch_up_band`, `trail_bonus` (`engine/tweaks.go`). Each is off at 0. They are validated and left out of YAML/JSON when 0, and the golden nights are unchanged.
- `engine.RandomTrackIn(…, Zones)`. `RandomTrack` calls it with the current zones, consuming the RNG the same way.
- The phone view's `flickBand` is now `Game.FlickBandFor(player)`. It's the same value unless `catch_up_band` is set.
- `agents`: a per-race `FunStat` (`agents/fun.go`). Seat skill now scales with the frog's band width (as a Normal-aim model).
- `cmd/hopsim`: `-fun`, `-live`, `-placement tight`, and flags for every knob. Flag handling moved to `flags.go`, and the metrics and scoring live in `fun.go` and `funrun.go`.
