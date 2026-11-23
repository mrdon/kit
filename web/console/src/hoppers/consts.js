// The Hoppers constants the TV and the phone compare against, so neither
// scatters string literals. They mirror the engine's typed constants
// (internal/apps/hoppers/engine), and TestConstsMatchEngine holds them in
// step. Shared the way the art is (art/frog.js): an ES module for the phone,
// flattened into the TV page by Go.
export const HOP = Object.freeze({
  phase: Object.freeze({ lobby: "lobby", racing: "racing", final: "final" }),
  sub: Object.freeze({ ready: "ready", flick: "flick", hop: "hop", raceEnd: "raceEnd" }),
  cmd: Object.freeze({ join: "join", seen: "seen", leave: "leave", flick: "flick", host: "host" }),
  heat: Object.freeze({ waiting: "waiting", next: "next", racing: "racing", done: "done" }),
  result: Object.freeze({ place: "place" }),
  spot: Object.freeze({ launch: "launch", chute: "chute" }),
  layout: Object.freeze({ straight: "straight", switchback: "switchback" }),
  msg: Object.freeze({ state: "state" }),
  role: Object.freeze({ tv: "tv", player: "player", host: "host" }),
  flickBand: 0.12, // default green band width; the frame's flickBand wins
});
