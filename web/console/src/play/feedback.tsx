import { sendFeedback } from './api';
import { RateNight as Shared } from '../live/RateNight';

// The end-of-night rating, on BOTH of trivia's ending screens -- under the
// honorable mentions, where the host is reading a list out and the table has
// nothing to do but listen, and again on the podium for whoever had not got
// round to it. The form itself is shared with Hoppers (live/RateNight).
export function RateNight({ game }: { game: string }) {
  return <Shared game={game} storageKey="kit.trivia.feedback" send={sendFeedback} />;
}
