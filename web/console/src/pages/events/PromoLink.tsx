import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api } from '../../api';

// PromoLink is the count and a way through to the promotion page. It renders
// nothing at all until there is something to say -- an unconfigured workspace
// should not carry a dead button, and neither should a week with nothing
// outstanding.
export default function PromoLink() {
  const [n, setN] = useState<number | null>(null);
  useEffect(() => {
    api
      .eventsPromo()
      .then((r) => setN(r.channels.length === 0 ? null : r.summary.outstanding))
      .catch(() => setN(null));
  }, []);
  if (n === null) return null;
  return (
    <Link className="btn btn-ghost" to="/events/promo" title="Where these events still need posting">
      Promotion
      {n > 0 && <span className="btn-badge">{n}</span>}
    </Link>
  );
}
