import { useCallback, useMemo, useState, type ReactNode } from 'react';
import { SWRConfig, mutate, type SWRConfiguration } from 'swr';
import { UnauthorizedError } from '../api/client';
import { TokenPrompt } from './TokenPrompt';

// AuthGate shows the token prompt whenever any API request comes back 401,
// and revalidates every cached request once a valid token is entered.
export function AuthGate({ children }: { children: ReactNode }) {
  const [needsToken, setNeedsToken] = useState(false);

  const onError = useCallback((err: unknown) => {
    if (err instanceof UnauthorizedError) setNeedsToken(true);
  }, []);

  const config = useMemo<SWRConfiguration>(
    () => ({
      onError,
      shouldRetryOnError: (err: unknown) => !(err instanceof UnauthorizedError),
    }),
    [onError],
  );

  const handleAccepted = useCallback(() => {
    setNeedsToken(false);
    void mutate(() => true);
  }, []);

  return (
    <SWRConfig value={config}>
      {needsToken ? <TokenPrompt onAccepted={handleAccepted} /> : children}
    </SWRConfig>
  );
}
