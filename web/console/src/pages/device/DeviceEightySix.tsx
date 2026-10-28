import { Link } from 'react-router-dom';
import MenuEightySix from '../MenuEightySix';
import { useSetChatContext } from '../../chatContext';

// The 86 checklist on a taproom device: the Menu page's panel, alone, with
// the checkboxes sized for a thumb. Each tick saves itself.
export default function DeviceEightySix() {
  useSetChatContext('the 86 checklist on a taproom device');
  return (
    <div className="page device-checklist">
      <div className="page-head">
        <nav className="crumbs">
          <Link to="/">Home</Link>
          <span className="crumb-sep">/</span>
          <span>86</span>
        </nav>
      </div>
      <MenuEightySix />
    </div>
  );
}
