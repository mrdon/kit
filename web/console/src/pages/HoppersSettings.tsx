import { Link } from 'react-router-dom';
import { api } from '../api';
import { useSetChatContext } from '../chatContext';
import { FeedbackChannelPanel } from '../live/FeedbackChannelPanel';

const load = () => api.hoppersFeedbackChannel();
const save = (id: string) => api.saveHoppersFeedbackChannel(id);

// Hoppers' admin settings. Today that is where the end-of-night ratings go;
// more of the game's workspace setup belongs here as it grows.
export default function HoppersSettings() {
  useSetChatContext('the admin Hoppers page');
  return (
    <>
      <div className="crumbs"><Link to="/admin">Admin</Link> / Hoppers</div>
      <div className="page-head">
        <h1>Hoppers</h1>
      </div>
      <FeedbackChannelPanel load={load} save={save}>
        When a frog race ends, every phone asks for a star rating and an optional comment. Each rating
        is posted to this channel. Ratings are not stored anywhere else.
      </FeedbackChannelPanel>
    </>
  );
}
