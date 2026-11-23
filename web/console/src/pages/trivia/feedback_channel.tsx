import { api } from '../../api';
import { FeedbackChannelPanel as Shared } from '../../live/FeedbackChannelPanel';

const load = () => api.triviaFeedbackChannel();
const save = (id: string) => api.saveTriviaFeedbackChannel(id);

// Where trivia's end-of-night ratings go. The panel is shared with Hoppers.
export function FeedbackChannelPanel() {
  return (
    <Shared load={load} save={save}>
      When a game ends, every table&rsquo;s phone asks for a star rating and an optional comment.
      Each table&rsquo;s rating is posted to this channel. Ratings are not stored anywhere else.
    </Shared>
  );
}
