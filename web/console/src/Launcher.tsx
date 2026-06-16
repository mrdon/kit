import { Link } from 'react-router-dom';
import { useMe } from './me';
import { SECTIONS } from './nav';

// The launcher renders a tile per section the caller can see. Same source
// (SECTIONS) as the top-nav, so they never drift.
export default function Launcher() {
  const me = useMe();
  const sections = SECTIONS.filter((s) => !s.admin || me?.is_admin);

  return (
    <div className="page">
      <div className="page-head">
        <h1>Home</h1>
        <p className="page-sub">
          Jump into a tool — or keep working in Slack and the card feed.
        </p>
      </div>
      <section className="tile-grid">
        {sections.map((s) => (
          <Link key={s.to} to={s.to} className="tile">
            <span className="tile-title">{s.label}</span>
            <span className="tile-blurb">{s.blurb}</span>
          </Link>
        ))}
      </section>
    </div>
  );
}
