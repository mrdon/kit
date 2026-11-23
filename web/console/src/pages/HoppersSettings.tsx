import { api } from '../api';
import { useSetChatContext } from '../chatContext';
import { FeedbackChannelPanel } from '../live/FeedbackChannelPanel';

const load = () => api.hoppersFeedbackChannel();
const save = (id: string) => api.saveHoppersFeedbackChannel(id);

// Hoppers' admin settings: where the end-of-night ratings go.
export default function HoppersSettings() {
  useSetChatContext('the Hoppers ratings settings');
  return (
    <>
      <div className="page-head">
        <h1>Hoppers ratings</h1>
      </div>
      <FeedbackChannelPanel load={load} save={save}>
        When a frog race ends, every phone asks for a star rating and an optional comment. Each rating
        is posted to this channel. Ratings are not stored anywhere else.
      </FeedbackChannelPanel>
    </>
  );
}
