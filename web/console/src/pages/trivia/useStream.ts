import { useEffect, useRef } from 'react';
import { API_BASE } from '../../api';
import { useLiveStream, type LiveStream } from '../../liveStream';
import type { HostFrame } from './common';

// The host console's connection is the shared one in ../../liveStream, the
// same code the phone runs. It used to be a second copy of it, which is how
// the console kept two reconnect bugs the phone had already had fixed.

export type HostStream = LiveStream<HostFrame>;

export function useHostStream(gameId: string | undefined): HostStream {
  const stream = useLiveStream<HostFrame>({
    streamUrl: gameId ? `${API_BASE}/trivia/games/${gameId}/stream` : null,
    stateUrl: gameId ? `${API_BASE}/trivia/games/${gameId}/state` : null,
  });
  useHostBuildReload(stream.frame);
  return stream;
}

// A deploy while a game is running otherwise leaves this laptop driving the
// night from the bundle it loaded an hour ago. That is how a host ended up
// with no "Show the winner" button: the server had the awards phase, the
// console did not, and the primary action fell through to nothing.
//
// Immediate, unlike the phone's version. There is nothing half-typed to
// protect on a host's laptop mid-game, and the host is the one person who
// cannot afford to be a version behind.
function useHostBuildReload(frame: HostFrame | null) {
  const booted = useRef<string | null>(null);
  useEffect(() => {
    if (!frame?.build) return;
    if (booted.current === null) {
      booted.current = frame.build;
      return;
    }
    if (frame.build !== booted.current) window.location.reload();
  }, [frame]);
}
