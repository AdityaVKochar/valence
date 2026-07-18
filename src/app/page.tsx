"use client";

import { useState } from "react";
import { authClient } from "@/lib/auth-client";

export default function Home() {
  const { data: session, isPending } = authClient.useSession();
  const [isSigningIn, setIsSigningIn] = useState(false);

  const signInWithGoogle = async () => {
    setIsSigningIn(true);

    try {
      await authClient.signIn.social({
        provider: "google",
        callbackURL: "/",
      });
    } finally {
      setIsSigningIn(false);
    }
  };

  return (
    <main className="flex min-h-screen items-center justify-center bg-background px-6 text-foreground">
      <div className="flex w-full max-w-sm flex-col items-center gap-6 rounded-2xl border border-black/10 bg-white p-8 text-center shadow-sm dark:border-white/10 dark:bg-white/5">
        <div>
          <h1 className="text-2xl font-semibold">Welcome to Valence</h1>
          <p className="mt-2 text-sm text-black/60 dark:text-white/60">
            Sign in with your Google account to continue.
          </p>
        </div>

        {session?.user ? (
          <div className="text-sm">
            <p className="font-medium">Signed in as</p>
            <p className="text-black/60 dark:text-white/60">
              {session.user.email ?? session.user.name}
            </p>
          </div>
        ) : (
          <button
            type="button"
            onClick={signInWithGoogle}
            disabled={isPending || isSigningIn}
            className="w-full rounded-full bg-black px-5 py-3 text-sm font-medium text-white transition hover:bg-black/80 disabled:cursor-not-allowed disabled:opacity-60 dark:bg-white dark:text-black dark:hover:bg-white/80"
          >
            {isSigningIn ? "Opening Google..." : "Continue with Google"}
          </button>
        )}
      </div>
    </main>
  );
}
