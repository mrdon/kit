// Package livegame is the plumbing every live party game in Kit shares: the
// TV on the wall, the host's console, and the phones in the room.
//
// It was extracted from the trivia app so a second game (Hoppers) could run
// on the same transport instead of a copy of it, and so a reconnect fix
// reaches every game at once. What lives here is the part that knows nothing
// about any one game's rules:
//
//   - Broker and Relay: latest-wins fan-out of full snapshots to every
//     watcher in this process, and across web processes through Redis.
//   - Serve and StateSince: the SSE stream (with its liveness ping) and the
//     poll fallback, both keyed off one monotonically increasing version.
//   - RunSweeper: the loop that sleeps until a game's next deadline and wakes
//     on any publish.
//   - Join codes, the participant cookie, and host-issued reclaim codes.
//   - DisplayVersion and TVClientJS: the TV page's reload stamp and its
//     vanilla live client.
//
// The game owns everything else: its tables, its snapshot type, its
// projections, and which surface may see what.
package livegame
