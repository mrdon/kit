import { Link } from 'react-router-dom';
import { useMe } from './me';
import { ADMIN_SECTIONS, PRIMARY_SECTIONS } from './nav';

// The launcher renders a tile per primary section, plus a single Admin tile
// (for admins) that opens the grouped admin area. Same source (nav.ts) as
// the top-nav, so they never drift.
export default function Launcher() {
  const me = useMe();
  const showAdmin = me?.is_admin && ADMIN_SECTIONS.length > 0;

  return (
    <div className="page">
      <div className="page-head">
        <h1>Home</h1>
        <p className="page-sub">
          Jump into a tool — or keep working in Slack and the card feed.
        </p>
      </div>
      <section className="tile-grid">
        {PRIMARY_SECTIONS.map((s) => (
          <Link key={s.to} to={s.to} className="tile">
            <span className="tile-title">{s.label}</span>
            <span className="tile-blurb">{s.blurb}</span>
          </Link>
        ))}
        {showAdmin && (
          <Link to="/admin" className="tile">
            <span className="tile-title">Admin</span>
            <span className="tile-blurb">
              Roles, integrations, and workspace settings.
            </span>
          </Link>
        )}
      </section>
    </div>
  );
}
