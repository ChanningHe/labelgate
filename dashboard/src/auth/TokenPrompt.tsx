import { useState, type FormEvent } from 'react';
import { Button, Center, Paper, PasswordInput, Stack, Text, ThemeIcon, Title } from '@mantine/core';
import { IconLock } from '@tabler/icons-react';
import { ApiError, verifyToken } from '../api/client';
import { tokenStore } from '../api/token';

interface TokenPromptProps {
  onAccepted: () => void;
}

function describeFailure(err: unknown): string {
  if (err instanceof ApiError) return `The server answered ${err.status}. Try again in a moment.`;
  return 'Could not reach the Labelgate server. Check that it is running and try again.';
}

export function TokenPrompt({ onAccepted }: TokenPromptProps) {
  const [token, setToken] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [isChecking, setIsChecking] = useState(false);

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    if (!token.trim()) {
      setError('Paste the token from api.token in the server config.');
      return;
    }
    setIsChecking(true);
    setError(null);
    try {
      if (await verifyToken(token)) {
        tokenStore.save(token);
        onAccepted();
        return;
      }
      setError('That token was rejected. Check api.token in the server config.');
    } catch (err) {
      setError(describeFailure(err));
    } finally {
      setIsChecking(false);
    }
  };

  return (
    <Center mih="100vh" p="md">
      <Paper withBorder radius="md" p="xl" w="100%" maw={420}>
        <form onSubmit={handleSubmit}>
          <Stack gap="md">
            <ThemeIcon size={40} radius="md" color="orange">
              <IconLock size={22} />
            </ThemeIcon>
            <div>
              <Title order={3}>Enter API token</Title>
              <Text size="sm" c="dimmed" mt={4}>
                This Labelgate server requires a token. It is kept for this browser tab only.
              </Text>
            </div>
            <PasswordInput
              id="api-token"
              label="API token"
              placeholder="api.token value"
              value={token}
              onChange={(e) => setToken(e.currentTarget.value)}
              error={error}
              autoFocus
              autoComplete="current-password"
            />
            <Button type="submit" loading={isChecking} fullWidth>
              Unlock dashboard
            </Button>
          </Stack>
        </form>
      </Paper>
    </Center>
  );
}
