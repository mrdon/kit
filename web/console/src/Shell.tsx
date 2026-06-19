import { useEffect, useState } from 'react';
import { Outlet } from 'react-router-dom';
import { api, type Me } from './api';
import { MeContext } from './me';
import TopBar from './TopBar';
import ConsoleChat from './ConsoleChat';
import { ChatContextProvider } from './chatContext';

// Shell is the persistent console layout: the top bar plus a centered
// content column the routed pages render into via <Outlet/>. It fetches
// /me once (a 401 here bounces to login via the api client) and shares it
// through MeContext.
export default function Shell() {
  const [me, setMe] = useState<Me | null>(null);

  useEffect(() => {
    api.me().then(setMe).catch(() => setMe(null));
  }, []);

  return (
    <MeContext.Provider value={me}>
      <ChatContextProvider>
        <TopBar />
        <main className="content">
          <Outlet />
        </main>
        <ConsoleChat />
      </ChatContextProvider>
    </MeContext.Provider>
  );
}
