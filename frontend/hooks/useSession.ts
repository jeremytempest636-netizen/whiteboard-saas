"use client";

import { useCallback, useEffect, useState } from "react";
import { api, type Session } from "@/lib/api";
import { clearSession, loadSession, saveSession } from "@/lib/session";

type State = { ready: false; session: null } | { ready: true; session: Session | null };

/** Reads the saved guest session after hydration and lets callers sign in. */
export function useSession() {
  const [state, setState] = useState<State>({ ready: false, session: null });

  useEffect(() => {
    setState({ ready: true, session: loadSession() });
  }, []);

  const signIn = useCallback(async (name: string) => {
    const session = await api.guest(name);
    saveSession(session);
    setState({ ready: true, session });
    return session;
  }, []);

  const signOut = useCallback(() => {
    clearSession();
    setState({ ready: true, session: null });
  }, []);

  return { ready: state.ready, session: state.session, signIn, signOut };
}
