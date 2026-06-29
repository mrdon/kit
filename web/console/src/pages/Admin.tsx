import { Link } from 'react-router-dom';
import { ADMIN_SECTIONS, visibleSections } from '../nav';
import { useMe } from '../me';
import { useSetChatContext } from '../chatContext';

// The Admin area groups the infrequent, admin-only setup surfaces (roles,
// integrations, website, chat widget) so they don't clutter the top nav.
// Tiles reuse the launcher's grid; the underlying pages keep their own
// routes and enforce admin access server-side.
export default function Admin() {
  useSetChatContext('the Admin area (workspace setup)');
  const me = useMe();
  const sections = visibleSections(ADMIN_SECTIONS, me?.disabled_apps);
  return (
    <div className="page">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <span>Admin</span>
        </nav>
        <h1>Admin</h1>
        <p className="page-sub">
          Workspace settings you set up once and rarely touch.
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
