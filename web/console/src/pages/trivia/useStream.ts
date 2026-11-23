import { API_BASE } from '../../api';
import { useLiveStream, type LiveStream } from '../../live/liveStream';
import { useFrameBuildReload } from '../../live/useFrameBuildReload';
import type { HostFrame } from './common';

// The host console's connection is the shared one in ../../live/liveStream, the
// same code the phone runs. It used to be a second copy of it, which is how
// the console kept two reconnect bugs the phone had already had fixed.

export type HostStream = LiveStream<HostFrame>;

export function useHostStream(gameId: string | undefined): HostStream {
  const stream = useLiveStream<HostFrame>({
    streamUrl: gameId ? `${API_BASE}/trivia/games/${gameId}/stream` : null,
    stateUrl: gameId ? `${API_BASE}/trivia/games/${gameId}/state` : null,
  });
  // A deploy while a game is running otherwise leaves this laptop driving the
  // night from the bundle it loaded an hour ago. That is how a host ended up
  // with no "Show the winner" button: the server had the awards phase, the
  // console did not, and the primary action fell through to nothing. So the
  // host reloads immediately (no hold): there is nothing half-typed to protect
  // on a host's laptop mid-game.
  useFrameBuildReload(stream.frame);
  return stream;
}
