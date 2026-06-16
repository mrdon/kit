import { Link, NavLink } from 'react-router-dom';
import { useMe } from './me';
import { SECTIONS } from './nav';

// TopBar is the console's persistent chrome — an indigo bar with the
// workspace identity + section nav on the left and the signed-in user +
// sign-out on the right. The nav is the primary way to move between
// sections; it mirrors the chrome.Header look of Kit's other pages so the
// console feels like the same product.
export default function TopBar() {
  const me = useMe();
  const sections = SECTIONS.filter((s) => !s.admin || me?.is_admin);

  return (
    <header className="topbar">
      <div className="topbar-left">
        <Link to="/" className="topbar-brand">
          {me?.workspace_icon_url && (
            <img
              className="topbar-icon"
              src={me.workspace_icon_url}
              alt=""
              width={24}
              height={24}
            />
          )}
          <span className="topbar-name">{me?.workspace_name ?? 'Kit'}</span>
          <span className="topbar-product">Console</span>
        </Link>
        <nav className="topbar-nav">
          <NavLink to="/" end className="topbar-link">
            Home
          </NavLink>
          {sections.map((s) => (
            <NavLink key={s.to} to={s.to} className="topbar-link">
              {s.label}
            </NavLink>
          ))}
        </nav>
      </div>
      <div className="topbar-user">
        {me?.display_name && (
          <span className="topbar-username">{me.display_name}</span>
        )}
        {me?.logout_url && (
          <a className="topbar-logout" href={me.logout_url}>
            Sign out
          </a>
        )}
      </div>
    </header>
  );
}
