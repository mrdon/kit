import { Link } from 'react-router-dom';
import MenuGlutenReduced from '../MenuGlutenReduced';
import { useSetChatContext } from '../../chatContext';

// The gluten reduced checklist on a taproom device: the Menu page's panel,
// alone, with the checkboxes sized for a thumb. Each tick saves itself.
export default function DeviceGlutenReduced() {
  useSetChatContext('the gluten reduced checklist on a taproom device');
  return (
    <div className="page device-checklist">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <span>Gluten reduced</span>
        </nav>
      </div>
      <MenuGlutenReduced />
    </div>
  );
}
