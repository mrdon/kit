import {
  createContext,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from 'react';

// What the global chat launcher knows about the page the user is on. The
// description is sent to the agent as page context ("the Tasks page, viewing
// task …"); onTurnDone lets the active page refresh after the agent changes
// data. Built from non-secret UI state only — never decrypted vault material.
export type ChatPageContext = {
  description: string;
  onTurnDone?: () => void;
};

type Store = {
  current: ChatPageContext | null;
  setCurrent: (c: ChatPageContext | null) => void;
};

const Ctx = createContext<Store | null>(null);

// ChatContextProvider holds the current page's chat context. Mounted once in
// Shell so it spans every route; the global launcher reads it and pages write
// it via useSetChatContext.
export function ChatContextProvider({ children }: { children: ReactNode }) {
  const [current, setCurrent] = useState<ChatPageContext | null>(null);
  return <Ctx.Provider value={{ current, setCurrent }}>{children}</Ctx.Provider>;
}

// useConsoleChatContext reads the active page context (launcher side).
export function useConsoleChatContext(): ChatPageContext | null {
  return useContext(Ctx)?.current ?? null;
}

// useSetChatContext registers this page's chat context for as long as the
// page is mounted, clearing it on unmount. Re-registers whenever the
// description changes (encode the dynamic part — e.g. the open item's title —
// into the description string). onTurnDone is read through a ref so passing a
// fresh closure each render doesn't churn the registration.
export function useSetChatContext(description: string, onTurnDone?: () => void) {
  const store = useContext(Ctx);
  const setCurrent = store?.setCurrent;
  const onTurnDoneRef = useRef(onTurnDone);
  onTurnDoneRef.current = onTurnDone;

  useEffect(() => {
    if (!setCurrent) return;
    setCurrent({ description, onTurnDone: () => onTurnDoneRef.current?.() });
    return () => setCurrent(null);
  }, [description, setCurrent]);
}
