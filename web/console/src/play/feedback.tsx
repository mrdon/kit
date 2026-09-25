// The end-of-night rating: five stars, then a box for anything the table
// wants to say. Shown on BOTH ending screens -- under the honorable mentions,
// where the host is reading a list out and the table has nothing to do but
// listen, and again on the podium for whoever had not got round to it.
//
// Tapping a star only picks it. The rating goes to Slack once, with the
// comment, when the table presses Send, because nothing is stored to edit
// afterwards.
//
// The table always gets "thanks". If the send fails there is nothing useful
// it can do about it at the end of the night, so the failure is the server's
// to log, not the phone's to show.

import { useRef, useState } from 'react';
import { sendFeedback } from './api';

const KEY = 'kit.trivia.feedback';

// Remembered per game so a phone that locks and wakes shows "thanks" rather
// than asking again and posting twice.
function alreadySent(game: string): boolean {
  try {
    return window.localStorage.getItem(KEY) === game;
  } catch {
    return false;
  }
}

function markSent(game: string) {
  try {
    window.localStorage.setItem(KEY, game);
  } catch {
    /* private mode; worst case the phone asks again */
  }
}

export function RateNight({ game }: { game: string }) {
  const [stars, setStars] = useState(0);
  const [sent, setSent] = useState(() => alreadySent(game));
  const [text, setText] = useState('');
  const [busy, setBusy] = useState(false);
  const running = useRef(false);

  const send = async () => {
    if (running.current || stars === 0) return;
    running.current = true;
    setBusy(true);
    try {
      await sendFeedback(stars, text);
    } catch (e) {
      console.warn('trivia feedback not sent', e);
    } finally {
      markSent(game);
      setSent(true);
      running.current = false;
      setBusy(false);
    }
  };

  if (sent) {
    return (
      <div className="rate">
        <p className="sub" style={{ textAlign: 'center' }}>Thanks for the feedback.</p>
      </div>
    );
  }

  return (
    <div className="rate">
      <h2 style={{ textAlign: 'center' }}>How was tonight?</h2>
      <div className="stars" role="radiogroup" aria-label="Rating">
        {[1, 2, 3, 4, 5].map((n) => (
          <button
            key={n} type="button" role="radio" aria-checked={stars === n}
            aria-label={`${n} star${n === 1 ? '' : 's'}`}
            className={n <= stars ? 'star on' : 'star'}
            onClick={() => setStars(n)}
          >
            ★
          </button>
        ))}
      </div>
      {stars > 0 ? (
        <>
          <textarea
            className="field" rows={3} maxLength={1000}
            placeholder="Anything else? (optional)"
            value={text} onChange={(e) => setText(e.target.value)}
          />
          <button className="btn" disabled={busy} onClick={() => void send()}>
            Send
          </button>
        </>
      ) : null}
    </div>
  );
}
