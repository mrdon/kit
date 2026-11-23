import { useLiveStream, type LiveStream } from '../live/liveStream';
import { base, type PlayerFrame } from './api';

// The phone's connection is the shared one in ../live/liveStream: everything
// about reconnecting, polling, the silence watchdog and the stuck-countdown
// check lives there and is shared with every live game. What is left here is
// what is genuinely trivia's phone's -- its URLs.

export type Stream = LiveStream<PlayerFrame>;

export function useStream(): Stream {
  return useLiveStream<PlayerFrame>({
    streamUrl: base + '/stream',
    stateUrl: base + '/state',
  });
}
