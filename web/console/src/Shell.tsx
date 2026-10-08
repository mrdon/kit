import { useCallback, useState } from 'react';
import { Outlet } from 'react-router-dom';
import { api, type Me } from './api';
import { MeContext, MeRefreshContext } from './me';
import TopBar from './TopBar';
import ConsoleChat from './ConsoleChat';
import { ChatContextProvider } from './chatContext';
import { DeviceBar } from './DeviceShell';
import { usePolled } from './usePolled';
import { useBuildReload } from './useBuildReload';

// Shell is the persistent console layout: the top bar plus a centered
// content column the routed pages render into via <Outlet/>. It fetches
// /me once (a 401 here bounces to login via the api client) and shares it
// through MeContext.
//
// A paired device (me.kind === 'device') gets a different chrome: its label
// in place of a person's name, no nav to other apps, no chat and no sign
// out (signing out would unpair it). The pages themselves are the same;
// the server refuses any API the device's capabilities don't cover.
export default function Shell() {
  // undefined while /me is in flight, so a device never flashes the
  // person's chrome before its own.
  const [me, setMe] = useState<Me | null | undefined>(undefined);

  const refresh = useCallback(() => {
    api.me().then(setMe).catch(() => setMe(null));
  }, []);

  // Re-read /me on the same cadence the device pages poll on, and the moment
  // the tab is looked at again. That keeps a device's capabilities current and
  // is what notices a deploy: a new build token reloads the tab.
  usePolled(refresh, 60000);
  useBuildReload(me?.build);

  if (me === undefined) return null;
  const device = me?.kind === 'device';

  return (
    <MeContext.Provider value={me}>
      <MeRefreshContext.Provider value={refresh}>
        <ChatContextProvider>
          {device ? <DeviceBar me={me} /> : <TopBar />}
          <main className={device ? 'content content-device' : 'content'}>
            <Outlet />
          </main>
          {!device && <ConsoleChat />}
        </ChatContextProvider>
      </MeRefreshContext.Provider>
    </MeContext.Provider>
  );
}
