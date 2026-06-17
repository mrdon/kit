import { Link } from 'react-router-dom';
import { ADMIN_SECTIONS } from '../nav';

// The Admin area groups the infrequent, admin-only setup surfaces (roles,
// integrations, website, chat widget) so they don't clutter the top nav.
// Tiles reuse the launcher's grid; the underlying pages keep their own
// routes and enforce admin access server-side.
export default function Admin() {
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
        {ADMIN_SECTIONS.map((s) => (
          <Link key={s.to} to={s.to} className="tile">
            <span className="tile-title">{s.label}</span>
            <span className="tile-blurb">{s.blurb}</span>
          </Link>
        ))}
      </section>
    </div>
  );
}
